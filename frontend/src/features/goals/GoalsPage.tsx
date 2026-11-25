import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAsync } from '../../hooks/useAsync';
import { goalsApi } from '../../api/goals';
import { accountsApi } from '../../api/accounts';
import { bagsApi } from '../../api/bags';
import { dollarsToCents, formatCents } from '../../lib/money';
import type { GoalStatus, GoalInput } from '../../types/domain';
import {
  Button,
  Card,
  Dialog,
  ErrorMessage,
  EmptyState,
  Field,
  Spinner,
  IconButton,
  IconEdit,
  IconTrash,
  ProgressBar,
} from '../../components/ui';

// A goal's tracker is either an account (progress = the account's live balance)
// or a bag (progress = the bag's reserved balance — money actually set aside for
// it). One flat shape for the bag selector: id, name, and the account it lives
// in (which decides the goal's currency).
export interface GoalBagOption {
  id: number;
  name: string;
  account_id: number;
  account_name: string;
  currency: string;
}

// Goals are named savings targets tracked against one account — or one bag.
// The account's (or bag's) live balance is the goal's progress — account
// contributions are transfers into it; a bag's balance is Σ allocations −
// tagged spend. The achieved state is derived, never stored.
export function GoalsPage({ embedded = false }: { embedded?: boolean }) {
  const { t } = useTranslation('goals');
  const { data, loading, error, reload } = useAsync(() => goalsApi.list(), []);
  const accounts = useAsync(() => accountsApi.list(), []);
  const bagsOverview = useAsync(() => bagsApi.overview(), []);

  const [name, setName] = useState('');
  const [amount, setAmount] = useState('');
  const [bagId, setBagId] = useState('');
  const [accountId, setAccountId] = useState('');
  const [targetDate, setTargetDate] = useState('');
  const [formError, setFormError] = useState<string | null>(null);

  const [editing, setEditing] = useState<GoalStatus | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<GoalStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [pageError, setPageError] = useState<string | null>(null);

  const bagOptions: GoalBagOption[] = (bagsOverview.data?.accounts ?? []).flatMap(
    (acct) =>
      (acct.bags ?? []).map((b) => ({
        id: b.id,
        name: b.name,
        account_id: b.account_id,
        account_name: acct.account_name,
        currency: acct.currency,
      })),
  );

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);

    const cents = dollarsToCents(amount); // parses the comma decimal separator too
    if (!Number.isFinite(cents) || cents <= 0) {
      setFormError(t('validation.positive_amount'));
      return;
    }
    if (!bagId && !accountId) {
      setFormError(t('validation.choose_tracker'));
      return;
    }
    try {
      await goalsApi.create(input(name, cents, accountId, bagId, targetDate));
      setName('');
      setAmount('');
      setBagId('');
      setAccountId('');
      setTargetDate('');
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.create'));
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
      setPageError(err instanceof Error ? err.message : t('errors.delete'));
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

  const content = (
    <>
      <Card title={t('add.title')} help={<>{t('hints.progress')} {t('hints.bag')} {t('hints.target_date')}</>}>
        <form className="form-row" onSubmit={handleCreate}>
          <Field label={t('add.label_name')}>
            <input
              placeholder={t('add.name_placeholder')}
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={200}
              aria-label={t('add.name_aria')}
              required
            />
          </Field>
          <Field label={t('add.label_amount')}>
            <input
              placeholder={t('add.amount_placeholder')}
              inputMode="decimal"
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              required
            />
          </Field>
          <Field label={t('add.label_bag')}>
            <select
              value={bagId}
              onChange={(e) => setBagId(e.target.value)}
              aria-label={t('add.bag_aria')}
            >
              <option value="">{t('add.bag_empty')}</option>
              {bagOptions.map((b) => (
                <option key={b.id} value={b.id}>
                  {b.name} — {b.account_name} ({b.currency})
                </option>
              ))}
            </select>
          </Field>
          {!bagId && (
            <Field label={t('add.label_account')}>
              <select
                value={accountId}
                onChange={(e) => setAccountId(e.target.value)}
                aria-label={t('add.account_aria')}
                required
              >
                <option value="">{t('add.account_empty')}</option>
                {(accounts.data ?? []).map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name} ({a.currency})
                  </option>
                ))}
              </select>
            </Field>
          )}
          <Field label={t('add.label_date')}>
            <input
              type="date"
              value={targetDate}
              onChange={(e) => setTargetDate(e.target.value)}
              aria-label={t('add.date_aria')}
            />
          </Field>
          <Button type="submit">{t('add.submit')}</Button>
        </form>
        {formError && <ErrorMessage message={formError} />}
      </Card>

      {pageError && <ErrorMessage message={pageError} />}

      {goals.length === 0 ? (
        <EmptyState message={t('empty')} />
      ) : (
        <div className="table-scroll">
          <table className="plan-table">
            <thead>
              <tr>
                <th>{t('table.goal')}</th>
                <th>{t('table.account')}</th>
                <th className="num">{t('table.progress')}</th>
                <th aria-hidden="true" />
                <th>{t('table.status')}</th>
                <th aria-hidden="true" />
              </tr>
            </thead>
            <tbody>
              {goals.map((g) => {
                const pct = g.target_cents > 0 ? (g.progress_cents / g.target_cents) * 100 : 0;
                const remaining = Math.max(0, g.target_cents - g.progress_cents);
                return (
                  <tr key={g.id}>
                    <td>
                      <span className="cell-entity">
                        <span
                          className={g.achieved ? 'budget-dot ok' : 'budget-dot'}
                          aria-hidden="true"
                        />
                        <span className="entity-name">{g.name}</span>
                      </span>
                    </td>
                    <td className="cell-muted">{g.bag_name ?? g.account_name}</td>
                    <td className="num">
                      <strong>{formatCents(g.progress_cents, g.currency)}</strong>
                      {' / '}
                      {formatCents(g.target_cents, g.currency)}
                    </td>
                    <td className="cell-progress">
                      <ProgressBar pct={pct} done={g.achieved} />
                    </td>
                    <td>
                      {g.achieved ? (
                        <span className="cell-status ok">
                          <span className="budget-dot ok" aria-hidden="true" />
                          {t('card.reached')}
                        </span>
                      ) : (
                        <span className="cell-status">
                          {g.target_date
                            ? t('card.to_go_by', {
                                amount: formatCents(remaining, g.currency),
                                date: g.target_date,
                              })
                            : t('card.to_go', { amount: formatCents(remaining, g.currency) })}
                        </span>
                      )}
                    </td>
                    <td className="cell-actions">
                      <IconButton
                        icon={<IconEdit />}
                        label={t('card.edit')}
                        onClick={() => setEditing(g)}
                      />
                      <IconButton
                        icon={<IconTrash />}
                        label={t('card.delete')}
                        variant="danger"
                        onClick={() => setConfirmDelete(g)}
                      />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {editing && (
        <Dialog title={t('edit.title')} onClose={() => setEditing(null)}>
          <GoalEditForm
            initial={editing}
            accounts={accounts.data ?? []}
            bagOptions={bagOptions}
            busy={busy}
            onSubmit={handleUpdate}
            onCancel={() => setEditing(null)}
          />
        </Dialog>
      )}

      {confirmDelete && (
        <Dialog title={t('delete.title')} onClose={() => setConfirmDelete(null)}>
          <p className="hint-text">{t('delete.body', { name: confirmDelete.name })}</p>
          <div className="dialog-actions">
            <Button variant="secondary" onClick={() => setConfirmDelete(null)} disabled={busy}>
              {t('delete.cancel')}
            </Button>
            <Button variant="danger" onClick={handleDelete} disabled={busy}>
              {busy ? t('delete.deleting') : t('delete.confirm')}
            </Button>
          </div>
        </Dialog>
      )}
    </>
  );

  if (embedded) return content;

  return (
    <div className="page">
      <h2 className="page-title">{t('title')}</h2>
      {content}
    </div>
  );
}

function input(name: string, cents: number, accountId: string, bagId: string, targetDate: string): GoalInput {
  const bag = bagId ? Number(bagId) : null;
  return {
    name,
    target_cents: cents,
    // A bag goal derives its account (and currency) from the bag — send 0.
    account_id: bag ? 0 : Number(accountId),
    bag_id: bag,
    target_date: targetDate || null,
  };
}

// GoalEditForm is the modal's body: a full-replace PUT of the editable fields.
// The goal's currency follows its account (or its bag's account), so
// re-pointing at an account/bag in a different currency is refused by the
// backend (the target would change meaning) — only same-currency options are
// offered.
function GoalEditForm({
  initial,
  accounts,
  bagOptions,
  busy,
  onSubmit,
  onCancel,
}: {
  initial: GoalStatus;
  accounts: { id: number; name: string; currency: string }[];
  bagOptions: GoalBagOption[];
  busy: boolean;
  onSubmit: (input: GoalInput) => Promise<void>;
  onCancel: () => void;
}) {
  const { t } = useTranslation('goals');
  const [name, setName] = useState(initial.name);
  const [amount, setAmount] = useState((initial.target_cents / 100).toString());
  const [bagId, setBagId] = useState(initial.bag_id ? String(initial.bag_id) : '');
  const [accountId, setAccountId] = useState(String(initial.account_id));
  const [targetDate, setTargetDate] = useState(initial.target_date ?? '');
  const [formError, setFormError] = useState<string | null>(null);

  const sameCurrencyBags = bagOptions.filter((b) => b.currency === initial.currency);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    const cents = dollarsToCents(amount);
    if (!Number.isFinite(cents) || cents <= 0) {
      setFormError(t('validation.positive_amount'));
      return;
    }
    if (!bagId && !accountId) {
      setFormError(t('validation.choose_tracker'));
      return;
    }
    try {
      await onSubmit(input(name, cents, accountId, bagId, targetDate));
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.save'));
    }
  }

  return (
    <form className="form-stack" onSubmit={handleSubmit}>
      <Field label={t('edit.name')}>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={200}
          required
        />
      </Field>
      <Field label={t('edit.target_amount', { currency: initial.currency })}>
        <input
          inputMode="decimal"
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
          required
        />
      </Field>
      <Field label={t('edit.bag')}>
        <select value={bagId} onChange={(e) => setBagId(e.target.value)}>
          <option value="">{t('add.bag_empty')}</option>
          {/* Same-currency bags only: the backend refuses a cross-currency
              re-point — the target would silently change meaning. */}
          {sameCurrencyBags.map((b) => (
            <option key={b.id} value={b.id}>
              {b.name} — {b.account_name}
            </option>
          ))}
        </select>
      </Field>
      {!bagId && (
        <Field label={t('edit.account')}>
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
        </Field>
      )}
      <Field
        label={t('edit.date')}
        hint={t('hints.target_date')}
      >
        <input
          type="date"
          value={targetDate}
          onChange={(e) => setTargetDate(e.target.value)}
        />
      </Field>
      {formError && <ErrorMessage message={formError} />}
      <div className="form-actions">
        {onCancel && (
          <Button type="button" variant="secondary" onClick={onCancel} disabled={busy}>
            {t('edit.cancel')}
          </Button>
        )}
        <Button type="submit" disabled={busy}>
          {busy ? t('edit.saving') : t('edit.save')}
        </Button>
      </div>
    </form>
  );
}
