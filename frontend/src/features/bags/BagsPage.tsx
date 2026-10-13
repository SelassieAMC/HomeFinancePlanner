import { useState } from 'react';
import { useAsync } from '../../hooks/useAsync';
import { bagsApi } from '../../api/bags';
import { accountsApi } from '../../api/accounts';
import { dollarsToCents, formatCents, formatSignedCents } from '../../lib/money';
import { todayISO } from '../../lib/date';
import type {
  BagStatus,
  BagInput,
  BagAllocation,
  BagAllocationInput,
  BagRebalanceInput,
  YearClosePreview,
  YearCloseBagRow,
} from '../../types/domain';
import {
  Button,
  Card,
  Dialog,
  ErrorMessage,
  EmptyState,
  Spinner,
} from '../../components/ui';

// Bags are named reserves INSIDE one account ("allocation envelopes"): the
// bank application is untouched, the bag's balance is derived per read as
// Σ allocations − tagged spend, and the account section shows the money still
// unbagged. Same card language as budgets (the closest cousin in the UI).
export function BagsPage({ embedded = false }: { embedded?: boolean }) {
  const overview = useAsync(() => bagsApi.overview(), []);
  const accounts = useAsync(() => accountsApi.list(), []);

  const [formError, setFormError] = useState<string | null>(null);
  const [pageError, setPageError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const [createName, setCreateName] = useState('');
  const [createAmount, setCreateAmount] = useState('');
  const [createAccount, setCreateAccount] = useState('');
  const [createTarget, setCreateTarget] = useState('');

  const [editing, setEditing] = useState<BagStatus | null>(null);
  const [allocating, setAllocating] = useState<BagStatus | null>(null);
  const [historyOf, setHistoryOf] = useState<{ bag: BagStatus; currency: string; rows: BagAllocation[] } | null>(null);
  const [rebalancing, setRebalancing] = useState<string>(''); // account id as string
  const [confirmDelete, setConfirmDelete] = useState<BagStatus | null>(null);
  const [yearClose, setYearClose] = useState<YearClosePreview | null>(null);

  async function refresh() {
    try {
      await overview.reload();
    } catch (err) {
      setPageError(err instanceof Error ? err.message : 'Failed to reload bags.');
    }
  }

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    if (!createName.trim()) {
      setFormError('Name the bag.');
      return;
    }
    if (!createAccount) {
      setFormError('Choose the account the bag partitions.');
      return;
    }
    const input: BagInput = {
      name: createName,
      account_id: Number(createAccount),
      target_cents: null,
    };
    if (createAmount) {
      const cents = dollarsToCents(createAmount);
      if (!Number.isFinite(cents) || cents <= 0) {
        setFormError('Target must be positive.');
        return;
      }
      input.target_cents = cents;
    }
    try {
      await bagsApi.create(input);
      setCreateName('');
      setCreateAmount('');
      setCreateTarget('');
      await refresh();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to create bag.');
    }
  }

  async function handleAllocate(bag: BagStatus, input: BagAllocationInput) {
    await bagsApi.allocate(bag.id, input);
    await refresh();
  }

  async function handleRebalance(input: BagRebalanceInput) {
    await bagsApi.rebalance(input);
    await refresh();
  }

  async function handleDelete() {
    if (!confirmDelete) return;
    setBusy(true);
    setPageError(null);
    try {
      await bagsApi.remove(confirmDelete.id);
      setConfirmDelete(null);
      await refresh();
    } catch (err) {
      setPageError(err instanceof Error ? err.message : 'Failed to delete bag.');
    } finally {
      setBusy(false);
    }
  }

  async function handleStatus(bag: BagStatus, status: 'open' | 'closed') {
    setBusy(true);
    setPageError(null);
    try {
      await bagsApi.setStatus(bag.id, status);
      await refresh();
    } catch (err) {
      setPageError(err instanceof Error ? err.message : 'Failed to change the bag.');
    } finally {
      setBusy(false);
    }
  }

  async function openHistory(bag: BagStatus) {
    const currency = (overview.data?.accounts ?? []).find((a) => a.account_id === bag.account_id)?.currency ?? '';
    setHistoryOf({ bag, currency, rows: [] });
    try {
      const rows = await bagsApi.allocations(bag.id);
      setHistoryOf({ bag, currency, rows });
    } catch (err) {
      setPageError(err instanceof Error ? err.message : 'Failed to load the ledger.');
      setHistoryOf(null);
    }
  }

  async function openYearClose(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const year = new FormData(e.currentTarget).get('year')?.toString() ?? '';
    setPageError(null);
    setBusy(true);
    try {
      setYearClose(await bagsApi.yearClose(year));
    } catch (err) {
      setPageError(err instanceof Error ? err.message : 'Failed to load the close preview.');
    } finally {
      setBusy(false);
    }
  }

  if (overview.loading) return <Spinner />;
  if (overview.error) return <ErrorMessage message={overview.error.message} />;

  const accountsData = accounts.data ?? [];
  const sections = overview.data?.accounts ?? [];

  const content = (
    <>
      <Card title="Add bag">
        <form className="form-row" onSubmit={handleCreate}>
          <input
            placeholder="Bag name"
            value={createName}
            onChange={(e) => setCreateName(e.target.value)}
            maxLength={100}
            aria-label="Bag name"
            required
          />
          <select
            value={createAccount}
            onChange={(e) => setCreateAccount(e.target.value)}
            aria-label="Account"
            required
          >
            <option value="">Account…</option>
            {accountsData.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name} ({a.currency})
              </option>
            ))}
          </select>
          <input
            placeholder="Target (optional)"
            inputMode="decimal"
            value={createTarget}
            onChange={(e) => setCreateTarget(e.target.value)}
            aria-label="Target amount (optional)"
          />
          <Button type="submit">Add</Button>
        </form>
        <p className="hint-text">
          A bag reserves money inside one account — funding it does not move
          money, it claims part of the account's balance. Tag expenses into the
          bag to spend the reserve.
        </p>
        {formError && <ErrorMessage message={formError} />}
      </Card>

      {pageError && <ErrorMessage message={pageError} />}

      <div className="form-row">
        <form className="form-row" onSubmit={openYearClose}>
          <input type="number" name="year" defaultValue={new Date().getFullYear()} min={2000} max={2999} aria-label="Year" />
          <Button type="submit" variant="secondary" disabled={busy}>
            Year-close preview
          </Button>
        </form>
      </div>

      {sections.length === 0 ? (
        <EmptyState message="No bags yet — add your first reserve above." />
      ) : (
        sections.map((view) => (
          <section key={view.account_id} className="bag-account-section">
            <div className="bag-account-head">
              <h3 className="bag-account-title">
                {view.account_name}
                <span className="stat-label"> · {formatCents(view.account_balance_cents, view.currency)}</span>
              </h3>
              <div className="bag-unbagged">
                <span className="stat-label">Unbagged</span>
                <strong className={view.oversubscribed ? 'budget-status over' : ''}>
                  {formatCents(view.unbagged_cents, view.currency)}
                </strong>
                {view.oversubscribed && (
                  <span className="budget-status over">bags claim more than the account holds</span>
                )}
              </div>
              {view.bags.some((b) => b.status === 'open') && (
                <Button variant="secondary" className="btn-sm" onClick={() => setRebalancing(String(view.account_id))}>
                  Rebalance
                </Button>
              )}
            </div>
            {view.bags.length === 0 ? (
              <p className="hint-text">No bags on this account.</p>
            ) : (
              <div className="budget-list">
                {view.bags.map((b) => (
                  <BagCard
                    key={b.id}
                    bag={b}
                    currency={view.currency}
                    busy={busy}
                    onAllocate={() => setAllocating(b)}
                    onHistory={() => void openHistory(b)}
                    onEdit={() => setEditing(b)}
                    onToggleStatus={() => void handleStatus(b, b.status === 'open' ? 'closed' : 'open')}
                    onDelete={() => setConfirmDelete(b)}
                  />
                ))}
              </div>
            )}
          </section>
        ))
      )}

      {editing && (
        <Dialog title="Edit bag">
          <BagEditForm
            initial={editing}
            currency={sections.find((a) => a.account_id === editing.account_id)?.currency ?? ''}
            busy={busy}
            onSubmit={async (input) => {
              await bagsApi.update(editing.id, input);
              setEditing(null);
              await refresh();
            }}
            onCancel={() => setEditing(null)}
            onError={setPageError}
          />
        </Dialog>
      )}

      {allocating && (
        <Dialog title={`Allocate — ${allocating.name}`}>
          <AllocateForm
            currency={sections.find((a) => a.account_id === allocating.account_id)?.currency ?? ''}
            busy={busy}
            onSubmit={async (input) => {
              await handleAllocate(allocating, input);
              setAllocating(null);
            }}
            onCancel={() => setAllocating(null)}
          />
        </Dialog>
      )}

      {historyOf && (
        <Dialog title={`Ledger — ${historyOf.bag.name}`}>
          <LedgerList rows={historyOf.rows} currency={historyOf.currency} />
          <div className="dialog-actions">
            <Button variant="secondary" onClick={() => setHistoryOf(null)}>
              Close
            </Button>
          </div>
        </Dialog>
      )}

      {rebalancing && (
        <Dialog title="Rebalance leftovers">
          <RebalanceForm
            account={sections.find((a) => String(a.account_id) === rebalancing)}
            busy={busy}
            onSubmit={async (input) => {
              await handleRebalance(input);
              setRebalancing('');
            }}
            onCancel={() => setRebalancing('')}
          />
        </Dialog>
      )}

      {yearClose && (
        <Dialog title={`Year close ${yearClose.year}`}>
          <YearCloseList
            preview={yearClose}
            onSubmit={async () => {
              setYearClose(null);
              await refresh();
            }}
            onError={setPageError}
            busy={busy}
          />
        </Dialog>
      )}

      {confirmDelete && (
        <Dialog title="Delete bag?">
          <p className="hint-text">
            This permanently removes “{confirmDelete.name}” with its ledger.
            Spend already tagged into it stays in your activity, read as plain
            expense (untagged). This cannot be undone.
          </p>
          <div className="dialog-actions">
            <Button variant="secondary" onClick={() => setConfirmDelete(null)} disabled={busy}>
              Cancel
            </Button>
            <Button variant="danger" onClick={handleDelete} disabled={busy}>
              {busy ? 'Deleting…' : 'Delete'}
            </Button>
          </div>
        </Dialog>
      )}
    </>
  );

  if (embedded) return content;

  return (
    <div className="page">
      <h2 className="page-title">Bags</h2>
      {content}
    </div>
  );
}

function bagHealth(b: BagStatus): string | undefined {
  if (b.status === 'closed') return 'closed';
  if (b.balance_cents < 0) return 'over';
  if (b.target_cents != null && b.balance_cents < b.target_cents) return 'low';
  if (b.balance_cents > 0) return 'ok';
  return 'spent-out';
}

// BagCard mirrors the budget progress-card: dot + name + balance amounts,
// target progress bar, a status line, and the action row.
function BagCard({
  bag,
  currency,
  busy,
  onAllocate,
  onHistory,
  onEdit,
  onToggleStatus,
  onDelete,
}: {
  bag: BagStatus;
  currency: string;
  busy: boolean;
  onAllocate: () => void;
  onHistory: () => void;
  onEdit: () => void;
  onToggleStatus: () => void;
  onDelete: () => void;
}) {
  const health = bagHealth(bag);
  const pct =
    bag.target_cents != null && bag.target_cents > 0
      ? (bag.balance_cents / bag.target_cents) * 100
      : 0;
  const dotLabel =
    health === 'ok' ? 'budget-dot ok' : health === 'over' ? 'budget-dot over' : 'budget-dot';
  return (
    <Card className="budget-card">
      <div className="budget-head">
        <span className={dotLabel} aria-hidden="true" />
        <span className="budget-name">{bag.name}</span>
        <span className="stat-label">{bag.account_name}</span>
        <span className="budget-amounts">
          <strong>{formatCents(bag.balance_cents, currency)}</strong>
          {bag.target_cents != null && (
            <span aria-label="target">{' / '}{formatCents(bag.target_cents, currency)}</span>
          )}
        </span>
      </div>
      {bag.target_cents != null && bag.target_cents > 0 && (
        <div className="progress-track">
          <div
            className={health === 'ok' ? 'progress-fill done' : 'progress-fill'}
            style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
          />
        </div>
      )}
      <div className="budget-status">
        Spent {formatCents(bag.tagged_spend_cents, currency)} ·{' '}
        {bag.status === 'closed'
          ? 'closed'
          : bag.balance_cents < 0
            ? `overspent ${formatCents(-bag.balance_cents, currency)}`
            : bag.balance_cents === 0
              ? 'spent out — free to close'
              : 'holds its reserve'}
      </div>
      <div className="camera-row">
        {bag.status === 'open' && (
          <Button variant="secondary" className="btn-sm" onClick={onAllocate} disabled={busy}>
            Allocate
          </Button>
        )}
        <Button variant="ghost" className="btn-sm" onClick={onHistory}>
          Ledger
        </Button>
        <Button variant="ghost" className="btn-sm" onClick={onEdit} disabled={busy}>
          Edit
        </Button>
        <Button variant="ghost" className="btn-sm" onClick={onToggleStatus} disabled={busy}>
          {bag.status === 'open' ? 'Close' : 'Reopen'}
        </Button>
        <Button variant="danger" className="btn-sm" onClick={onDelete} disabled={busy}>
          Delete
        </Button>
      </div>
    </Card>
  );
}

function BagEditForm({
  initial,
  currency,
  busy,
  onSubmit,
  onCancel,
  onError,
}: {
  initial: BagStatus;
  currency: string;
  busy: boolean;
  onSubmit: (input: BagInput) => Promise<void>;
  onCancel: () => void;
  onError: (msg: string | null) => void;
}) {
  const [name, setName] = useState(initial.name);
  const [target, setTarget] = useState(
    initial.target_cents != null ? (initial.target_cents / 100).toString() : '',
  );
  const [formError, setFormError] = useState<string | null>(null);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    onError(null);
    const input: BagInput = { name, account_id: initial.account_id, target_cents: null };
    if (target) {
      const cents = dollarsToCents(target);
      if (!Number.isFinite(cents) || cents <= 0) {
        setFormError('Target must be positive.');
        return;
      }
      input.target_cents = cents;
    }
    try {
      await onSubmit(input);
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to save the bag.');
    }
  }

  return (
    <form className="form-stack" onSubmit={handleSubmit}>
      <label>
        Name
        <input value={name} onChange={(e) => setName(e.target.value)} maxLength={100} required />
      </label>
      <label>
        Target ({currency}, optional — empty removes it)
        <input
          inputMode="decimal"
          value={target}
          onChange={(e) => setTarget(e.target.value)}
        />
      </label>
      <p className="hint-text">
        The account never changes — a reserve lives where it was opened.
      </p>
      {formError && <ErrorMessage message={formError} />}
      <div className="form-actions">
        <Button type="button" variant="secondary" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
        <Button type="submit" disabled={busy}>
          {busy ? 'Saving…' : 'Save changes'}
        </Button>
      </div>
    </form>
  );
}

function AllocateForm({
  currency,
  busy,
  onSubmit,
  onCancel,
}: {
  currency: string;
  busy: boolean;
  onSubmit: (input: BagAllocationInput) => Promise<void>;
  onCancel: () => void;
}) {
  const [kind, setKind] = useState<'fund' | 'withdraw' | 'adjust'>('fund');
  const [amount, setAmount] = useState('');
  const [note, setNote] = useState('');
  const [date, setDate] = useState(todayISO());
  const [formError, setFormError] = useState<string | null>(null);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    const cents = dollarsToCents(amount);
    if (kind === 'adjust') {
      if (!Number.isFinite(cents) || cents === 0) {
        setFormError('An adjustment must be non-zero.');
        return;
      }
    } else if (!Number.isFinite(cents) || cents <= 0) {
      setFormError('Enter a positive amount (the kind decides the direction).');
      return;
    }
    try {
      await onSubmit({ kind, amount_cents: cents, note, date });
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to allocate.');
    }
  }

  return (
    <form className="form-stack" onSubmit={handleSubmit}>
      <label>
        Action
        <select value={kind} onChange={(e) => setKind(e.target.value as typeof kind)}>
          <option value="fund">Fund — reserve into the bag</option>
          <option value="withdraw">Withdraw — release back to unbagged</option>
          <option value="adjust">Adjust — explicit correction</option>
        </select>
      </label>
      <label>
        Amount ({currency})
        <input inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} required />
      </label>
      <label>
        Note (optional)
        <input value={note} onChange={(e) => setNote(e.target.value)} maxLength={200} />
      </label>
      <label>
        Date
        <input type="date" value={date} onChange={(e) => setDate(e.target.value)} required />
      </label>
      {formError && <ErrorMessage message={formError} />}
      <div className="form-actions">
        <Button type="button" variant="secondary" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
        <Button type="submit" disabled={busy}>
          Post
        </Button>
      </div>
    </form>
  );
}

function LedgerList({ rows, currency }: { rows: BagAllocation[]; currency: string }) {
  if (rows.length === 0) {
    return <EmptyState message="No ledger entries yet." />;
  }
  return (
    <div className="ledger-list">
      {rows.map((a) => (
        <div key={a.id} className="ledger-row">
          <span className="ledger-kind">{a.kind}</span>
          <span className="ledger-amounts">{formatSignedCents(a.amount_cents, currency)}</span>
          <span className="ledger-meta">
            {a.date}
            {a.note ? ` · ${a.note}` : ''}
          </span>
        </div>
      ))}
    </div>
  );
}

function RebalanceForm({
  account,
  busy,
  onSubmit,
  onCancel,
}: {
  account:
    | {
        account_id: number;
        currency: string;
        bags: BagStatus[];
      }
    | undefined;
  busy: boolean;
  onSubmit: (input: BagRebalanceInput) => Promise<void>;
  onCancel: () => void;
}) {
  const open = (account?.bags ?? []).filter((b) => b.status === 'open');
  const [fromBag, setFromBag] = useState('');
  const [toBag, setToBag] = useState('');
  const [amount, setAmount] = useState('');
  const [note, setNote] = useState('');
  const [date, setDate] = useState(todayISO());
  const [formError, setFormError] = useState<string | null>(null);
  const from = open.find((b) => String(b.id) === fromBag);
  const others = open.filter((b) => String(b.id) !== fromBag);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    if (!fromBag || !toBag) {
      setFormError('Choose both bags.');
      return;
    }
    const cents = dollarsToCents(amount);
    if (!Number.isFinite(cents) || cents <= 0) {
      setFormError('Enter a positive amount.');
      return;
    }
    try {
      await onSubmit({
        from_bag_id: Number(fromBag),
        to_bag_id: Number(toBag),
        amount_cents: cents,
        note,
        date,
      });
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to rebalance.');
    }
  }

  if (open.length < 2) {
    return (
      <>
        <EmptyState message="Two open bags on the account are needed to rebalance between them." />
        <div className="dialog-actions">
          <Button variant="secondary" onClick={onCancel}>
            Close
          </Button>
        </div>
      </>
    );
  }

  return (
    <form className="form-stack" onSubmit={handleSubmit}>
      <div className="form-row">
        <select value={fromBag} onChange={(e) => setFromBag(e.target.value)} aria-label="From bag" required>
          <option value="">From bag…</option>
          {open.map((b) => (
            <option key={b.id} value={b.id}>
              {b.name} ({formatCents(b.balance_cents, account?.currency ?? '')})
            </option>
          ))}
        </select>
        <select value={toBag} onChange={(e) => setToBag(e.target.value)} aria-label="To bag" required>
          <option value="">To bag…</option>
          {others.map((b) => (
            <option key={b.id} value={b.id}>
              {b.name}
            </option>
          ))}
        </select>
      </div>
      <label>
        Amount ({account?.currency ?? ''})
        <input inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} required />
      </label>
      <label>
        Note (optional)
        <input value={note} onChange={(e) => setNote(e.target.value)} maxLength={200} />
      </label>
      <label>
        Date
        <input type="date" value={date} onChange={(e) => setDate(e.target.value)} required />
      </label>
      {from && from.balance_cents < 0 && (
        <p className="hint-text">The source bag is overspending — fix its tagging first.</p>
      )}
      {formError && <ErrorMessage message={formError} />}
      <div className="form-actions">
        <Button type="button" variant="secondary" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
        <Button type="submit" disabled={busy}>
          Move
        </Button>
      </div>
    </form>
  );
}

// YearCloseList turns the preview into actions: a leftover bag can move its
// remaining balance into another bag of the same account (a rebalance dated
// at the year end) or release it. The close-out posts through the regular
// actions — no bespoke state.
function YearCloseList({
  preview,
  busy,
  onSubmit,
  onError,
}: {
  preview: YearClosePreview;
  busy: boolean;
  onSubmit: () => Promise<void>;
  onError: (msg: string | null) => void;
}) {
  const anyRows = preview.bags.length > 0;
  return (
    <div className="year-close">
      {anyRows && (
        <p className="hint-text">
          Balances as they would stand after everything dated through Dec 31 of
          {preview.year}. Move a leftover into another bag, or release it — the
          posting uses the regular ledger, dated at the year end.
        </p>
      )}
      {!anyRows && <EmptyState message="No open bags for this year." />}
      <div className="budget-list">
        {preview.bags.map((row) => (
          <YearCloseRow
            key={row.bag_id}
            row={row}
            year={preview.year}
            bags={preview.bags.filter((b) => b.account_id === row.account_id && b.bag_id !== row.bag_id)}
            busy={busy}
            onDone={onSubmit}
            onError={onError}
          />
        ))}
      </div>
      {preview.conversion_warnings.length > 0 && (
        <ErrorMessage
          message={`Exchange rate missing for ${preview.conversion_warnings.join(', ')} — those amounts are shown 1:1.`}
        />
      )}
    </div>
  );
}

function YearCloseRow({
  row,
  year,
  bags,
  busy,
  onDone,
  onError,
}: {
  row: YearCloseBagRow;
  year: string;
  bags: YearCloseBagRow[];
  busy: boolean;
  onDone: () => Promise<void>;
  onError: (msg: string | null) => void;
}) {
  const closeDate = `${year}-12-31`;
  const [targetBag, setTargetBag] = useState('');
  const [formError, setFormError] = useState<string | null>(null);

  async function moveAll() {
    setFormError(null);
    onError(null);
    try {
      await bagsApi.rebalance({
        from_bag_id: row.bag_id,
        to_bag_id: Number(targetBag),
        amount_cents: row.balance_cents,
        note: `year close ${year}`,
        date: closeDate,
      });
      await onDone();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to post the move.');
    }
  }

  async function release() {
    setFormError(null);
    onError(null);
    try {
      await bagsApi.allocate(row.bag_id, {
        kind: 'withdraw',
        amount_cents: row.balance_cents,
        note: `year close ${year} — leftover released`,
        date: closeDate,
      });
      await onDone();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to release the leftover.');
    }
  }

  return (
    <Card className="budget-card">
      <div className="budget-head">
        <span className={row.shortfall_cents > 0 ? 'budget-dot' : 'budget-dot ok'} aria-hidden="true" />
        <span className="budget-name">{row.bag_name}</span>
        <span className="stat-label">{row.account_name}</span>
        <span className="budget-amounts">
          <strong>{formatCents(row.balance_cents, row.currency)}</strong>
          {row.target_cents > 0 && <span>/{formatCents(row.target_cents, row.currency)}</span>}
        </span>
      </div>
      <div className="budget-status">
        {row.shortfall_cents > 0
          ? `${formatCents(row.shortfall_cents, row.currency)} short of its target`
          : row.target_cents > 0
            ? 'Target covered'
            : row.balance_cents < 0
              ? `overspent ${formatCents(-row.balance_cents, row.currency)}`
              : 'No target'}
      </div>
      {row.balance_cents > 0 && (
        <div className="camera-row">
          {bags.length > 0 && (
            <>
              <select value={targetBag} onChange={(e) => setTargetBag(e.target.value)} aria-label="Move leftover to">
                <option value="">Move leftover to…</option>
                {bags.map((b) => (
                  <option key={b.bag_id} value={b.bag_id}>
                    {b.bag_name}
                  </option>
                ))}
              </select>
              <Button
                variant="secondary"
                className="btn-sm"
                disabled={busy || !targetBag}
                onClick={() => void moveAll()}
              >
                Move all
              </Button>
            </>
          )}
          <Button
            variant="ghost"
            className="btn-sm"
            onClick={() => void release()}
            disabled={busy}
          >
            Release leftover
          </Button>
        </div>
      )}
      {formError && <ErrorMessage message={formError} />}
    </Card>
  );
}