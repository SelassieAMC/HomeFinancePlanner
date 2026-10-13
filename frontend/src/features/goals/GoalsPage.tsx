import { useState } from 'react';
import { useAsync } from '../../hooks/useAsync';
import { goalsApi } from '../../api/goals';
import { accountsApi } from '../../api/accounts';
import { dollarsToCents, formatCents } from '../../lib/money';
import type { GoalStatus, GoalInput } from '../../types/domain';
import {
  Button,
  Card,
  Dialog,
  ErrorMessage,
  EmptyState,
  Spinner,
} from '../../components/ui';

// Goals are named savings targets tracked against one account: the account's
// live balance is the goal's progress — contributions are transfers into it
// (Transactions → Transfer), so the balance stays the single source of truth
// the net-worth math already trusts. The achieved state is derived, never
// stored.
export function GoalsPage() {
  const { data, loading, error, reload } = useAsync(() => goalsApi.list(), []);
  const accounts = useAsync(() => accountsApi.list(), []);

  const [name, setName] = useState('');
  const [amount, setAmount] = useState('');
  const [accountId, setAccountId] = useState('');
  const [targetDate, setTargetDate] = useState('');
  const [formError, setFormError] = useState<string | null>(null);

  const [editing, setEditing] = useState<GoalStatus | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<GoalStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [pageError, setPageError] = useState<string | null>(null);

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);

    const cents = dollarsToCents(amount); // parses the comma decimal separator too
    if (!Number.isFinite(cents) || cents <= 0) {
      setFormError('Enter a positive target amount.');
      return;
    }
    if (!accountId) {
      setFormError('Choose the account the goal tracks.');
      return;
    }
    try {
      await goalsApi.create(input(name, cents, accountId, targetDate));
      setName('');
      setAmount('');
      setTargetDate('');
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to create goal.');
    }
  }

  async function handleUpdate(input: GoalInput) {
    if (!editing) return;
    setBusy(true);
    try {
      await goalsApi.update(editing.id, input);
      setEditing(null);
      reload();
    } finally {
      setBusy(false);
    }
  }

  async function handleDelete() {
    if (!confirmDelete) return;
    setBusy(true);
    setPageError(null);
    try {
      await goalsApi.remove(confirmDelete.id);
      setConfirmDelete(null);
      reload();
    } catch (err) {
      setPageError(err instanceof Error ? err.message : 'Failed to delete goal.');
    } finally {
      setBusy(false);
    }
  }

  if (loading) return <Spinner />;
  if (error) return <ErrorMessage message={error.message} />;

  const goals = [...(data ?? [])].sort((a, b) => {
    // Open goals first; achieved ones trail, newest name order among equals.
    const open = (a.achieved ? 1 : 0) - (b.achieved ? 1 : 0);
    if (open !== 0) return open;
    return a.name.localeCompare(b.name);
  });

  return (
    <div className="page">
      <h2 className="page-title">Goals</h2>

      <Card title="Add goal">
        <form className="form-row" onSubmit={handleCreate}>
          <input
            placeholder="Goal name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={200}
            aria-label="Goal name"
            required
          />
          <input
            placeholder="Target amount"
            inputMode="decimal"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            required
          />
          <select
            value={accountId}
            onChange={(e) => setAccountId(e.target.value)}
            aria-label="Tracked account"
            required
          >
            <option value="">Tracked account…</option>
            {(accounts.data ?? []).map((a) => (
              <option key={a.id} value={a.id}>
                {a.name} ({a.currency})
              </option>
            ))}
          </select>
          <input
            type="date"
            value={targetDate}
            onChange={(e) => setTargetDate(e.target.value)}
            aria-label="Target date (optional)"
          />
          <Button type="submit">Add</Button>
        </form>
        <p className="hint-text">
          Progress is the tracked account's current balance — contribute with a
          transfer into it. The currency follows that account.
        </p>
        {formError && <ErrorMessage message={formError} />}
      </Card>

      {pageError && <ErrorMessage message={pageError} />}

      {goals.length === 0 ? (
        <EmptyState message="No goals yet — add your first one above." />
      ) : (
        <div className="budget-list">
          {goals.map((g) => (
            <GoalCard
              key={g.id}
              goal={g}
              onEdit={() => setEditing(g)}
              onDelete={() => setConfirmDelete(g)}
            />
          ))}
        </div>
      )}

      {editing && (
        <Dialog title="Edit goal">
          <GoalEditForm
            initial={editing}
            accounts={accounts.data ?? []}
            busy={busy}
            onSubmit={handleUpdate}
            onCancel={() => setEditing(null)}
          />
        </Dialog>
      )}

      {confirmDelete && (
        <Dialog title="Delete goal?">
          <p className="hint-text">
            This permanently removes “{confirmDelete.name}”. The tracked account
            and its balances are untouched. This cannot be undone.
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
    </div>
  );
}

function input(name: string, cents: number, accountId: string, targetDate: string): GoalInput {
  return {
    name,
    target_cents: cents,
    account_id: Number(accountId),
    target_date: targetDate || null,
  };
}

// GoalCard mirrors the budget progress-card style: dot + name + progress/target
// up top, the bar, and a status line (remaining / achieved / milestone).
function GoalCard({
  goal,
  onEdit,
  onDelete,
}: {
  goal: GoalStatus;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const pct = goal.target_cents > 0 ? (goal.progress_cents / goal.target_cents) * 100 : 0;
  const remaining = Math.max(0, goal.target_cents - goal.progress_cents);
  return (
    <Card className="budget-card">
      <div className="budget-head">
        <span className={goal.achieved ? 'budget-dot ok' : 'budget-dot'} aria-hidden="true" />
        <span className="budget-name">{goal.name}</span>
        <span className="stat-label">{goal.account_name}</span>
        <span className="budget-amounts">
          <strong>{formatCents(goal.progress_cents, goal.currency)}</strong>
          {' / '}
          {formatCents(goal.target_cents, goal.currency)}
        </span>
      </div>
      <div className="progress-track">
        <div
          className={goal.achieved ? 'progress-fill done' : 'progress-fill'}
          style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
        />
      </div>
      <div className={goal.achieved ? 'budget-status ok' : 'budget-status'}>
        {goal.achieved
          ? 'Goal reached'
          : `${formatCents(remaining, goal.currency)} to go${goal.target_date ? ` · by ${goal.target_date}` : ''}`}
      </div>
      <div className="camera-row">
        <Button variant="secondary" className="btn-sm" onClick={onEdit}>
          Edit
        </Button>
        <Button variant="danger" className="btn-sm" onClick={onDelete}>
          Delete
        </Button>
      </div>
    </Card>
  );
}

// GoalEditForm is the modal's body: a full-replace PUT of the editable fields.
// The goal's currency follows its account, so re-pointing at an account in a
// different currency is refused by the backend (the target would change
// meaning) — only same-currency accounts are offered.
function GoalEditForm({
  initial,
  accounts,
  busy,
  onSubmit,
  onCancel,
}: {
  initial: GoalStatus;
  accounts: { id: number; name: string; currency: string }[];
  busy: boolean;
  onSubmit: (input: GoalInput) => Promise<void>;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial.name);
  const [amount, setAmount] = useState((initial.target_cents / 100).toString());
  const [accountId, setAccountId] = useState(String(initial.account_id));
  const [targetDate, setTargetDate] = useState(initial.target_date ?? '');
  const [formError, setFormError] = useState<string | null>(null);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    const cents = dollarsToCents(amount);
    if (!Number.isFinite(cents) || cents <= 0) {
      setFormError('Enter a positive target amount.');
      return;
    }
    try {
      await onSubmit(input(name, cents, accountId, targetDate));
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to save the goal.');
    }
  }

  return (
    <form className="form-stack" onSubmit={handleSubmit}>
      <label>
        Name
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={200}
          required
        />
      </label>
      <label>
        Target amount ({initial.currency})
        <input
          inputMode="decimal"
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
          required
        />
      </label>
      <label>
        Tracked account
        <select value={accountId} onChange={(e) => setAccountId(e.target.value)} required>
          {/* Same-currency accounts only: the backend refuses a cross-currency
              re-point — the target would silently change meaning. */}
          {accounts
            .filter((a) => a.currency === initial.currency)
            .map((a) => (
              <option key={a.id} value={a.id}>
                {a.name} ({a.currency})
              </option>
            ))}
        </select>
      </label>
      <label>
        Target date (optional)
        <input
          type="date"
          value={targetDate}
          onChange={(e) => setTargetDate(e.target.value)}
        />
      </label>
      {formError && <ErrorMessage message={formError} />}
      <div className="form-actions">
        {onCancel && (
          <Button type="button" variant="secondary" onClick={onCancel} disabled={busy}>
            Cancel
          </Button>
        )}
        <Button type="submit" disabled={busy}>
          {busy ? 'Saving…' : 'Save changes'}
        </Button>
      </div>
    </form>
  );
}