import { useState } from 'react';
import { useTranslation } from 'react-i18next';
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
  IconButton,
  IconEdit,
  IconTrash,
  ProgressBar,
} from '../../components/ui';

// Goals are named savings targets tracked against one account: the account's
// live balance is the goal's progress — contributions are transfers into it
// (Transactions → Transfer), so the balance stays the single source of truth
// the net-worth math already trusts. The achieved state is derived, never
// stored.
export function GoalsPage({ embedded = false }: { embedded?: boolean }) {
  const { t } = useTranslation('goals');
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
      setFormError(t('validation.positive_amount'));
      return;
    }
    if (!accountId) {
      setFormError(t('validation.choose_account'));
      return;
    }
    try {
      await goalsApi.create(input(name, cents, accountId, targetDate));
      setName('');
      setAmount('');
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
      <Card title={t('add.title')}>
        <form className="form-row" onSubmit={handleCreate}>
          <input
            placeholder={t('add.name_placeholder')}
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={200}
            aria-label={t('add.name_aria')}
            required
          />
          <input
            placeholder={t('add.amount_placeholder')}
            inputMode="decimal"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            required
          />
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
          <input
            type="date"
            value={targetDate}
            onChange={(e) => setTargetDate(e.target.value)}
            aria-label={t('add.date_aria')}
          />
          <Button type="submit">{t('add.submit')}</Button>
        </form>
        <p className="hint-text">
          {t('hints.progress')} {t('hints.target_date')}
        </p>
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
                    <td className="cell-muted">{g.account_name}</td>
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
        <Dialog title={t('edit.title')}>
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
        <Dialog title={t('delete.title')}>
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

function input(name: string, cents: number, accountId: string, targetDate: string): GoalInput {
  return {
    name,
    target_cents: cents,
    account_id: Number(accountId),
    target_date: targetDate || null,
  };
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
  const { t } = useTranslation('goals');
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
      setFormError(t('validation.positive_amount'));
      return;
    }
    try {
      await onSubmit(input(name, cents, accountId, targetDate));
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.save'));
    }
  }

  return (
    <form className="form-stack" onSubmit={handleSubmit}>
      <label>
        {t('edit.name')}
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={200}
          required
        />
      </label>
      <label>
        {t('edit.target_amount', { currency: initial.currency })}
        <input
          inputMode="decimal"
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
          required
        />
      </label>
      <label>
        {t('edit.account')}
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
        {t('edit.date')}
        <input
          type="date"
          value={targetDate}
          onChange={(e) => setTargetDate(e.target.value)}
        />
      </label>
      <p className="hint-text">{t('hints.target_date')}</p>
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