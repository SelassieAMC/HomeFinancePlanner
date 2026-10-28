import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useAsync } from '../../hooks/useAsync';
import { accountsApi } from '../../api/accounts';
import { settingsApi } from '../../api/settings';
import type { Account, AccountInput, AccountType } from '../../types/domain';
import { formatCents, dollarsToCents } from '../../lib/money';
import { COMMON_CURRENCIES, type CurrencyOption } from '../../lib/currencies';
import { Button, Card, Spinner, ErrorMessage, EmptyState, ItemPanel, ItemPanels, Dialog } from '../../components/ui';

const ACCOUNT_TYPES: AccountType[] = ['checking', 'savings', 'credit', 'cash', 'other'];

// The backend accepts any 3-letter ISO currency, which can be wider than the
// picklist — always include the account's current currency so it stays
// selectable instead of rendering as a blank option. The account dropdown is
// alphabetical by code (unlike the bill/base selects, which keep EUR/USD on
// top).
function currencyOptions(current: string): CurrencyOption[] {
  const all = COMMON_CURRENCIES.some((c) => c.code === current)
    ? COMMON_CURRENCIES
    : [{ code: current, label: current }, ...COMMON_CURRENCIES];
  return [...all].sort((a, b) => a.code.localeCompare(b.code));
}

export function AccountsPage() {
  const { t } = useTranslation('accounts');
  const { data, loading, error, reload } = useAsync(() => accountsApi.list(), []);
  // New accounts default to the user's base display currency.
  const baseCurrency = useAsync(() => settingsApi.getBaseCurrency(), []);
  const [name, setName] = useState('');
  const [type, setType] = useState<AccountType>('checking');
  const [balance, setBalance] = useState('');
  const [formError, setFormError] = useState<string | null>(null);

  // Edit-in-place state: only one account is edited at a time, so a single
  // set of fields keyed by editingId is enough.
  const [editingId, setEditingId] = useState<number | null>(null);
  const [editName, setEditName] = useState('');
  const [editType, setEditType] = useState<AccountType>('checking');
  const [editBalance, setEditBalance] = useState('');
  const [editCardDigits, setEditCardDigits] = useState('');

  // New accounts default to the user's base display currency (falls back to
  // USD until the setting loads).
  const defaultCurrency = baseCurrency.data?.currency ?? 'USD';
  const [currency, setCurrency] = useState(defaultCurrency);
  const [currencyTouched, setCurrencyTouched] = useState(false);
  useEffect(() => {
    if (!currencyTouched) setCurrency(defaultCurrency);
  }, [defaultCurrency, currencyTouched]);

  const [editCurrency, setEditCurrency] = useState('USD');

  function startEdit(a: Account) {
    setEditingId(a.id);
    setEditName(a.name);
    setEditType(a.type);
    setEditCurrency(a.currency);
    setEditBalance((a.opening_balance_cents / 100).toFixed(2));
    setEditCardDigits(a.card_last_digits ?? '');
    setFormError(null);
  }

  function cancelEdit() {
    setEditingId(null);
    setFormError(null);
  }

  async function handleUpdate(e: React.FormEvent) {
    e.preventDefault();
    if (editingId === null) return;
    setFormError(null);

    const cents = dollarsToCents(editBalance); // the opening balance (may be negative)
    if (Number.isNaN(cents)) {
      setFormError(t('errors.invalidBalance'));
      return;
    }
    const input: AccountInput = {
      name: editName,
      type: editType,
      currency: editCurrency,
      opening_balance_cents: cents,
      card_last_digits: editCardDigits || undefined,
    };
    try {
      await accountsApi.update(editingId, input);
      setEditingId(null);
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.update'));
    }
  }

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);

    const cents = dollarsToCents(balance);
    if (Number.isNaN(cents)) {
      setFormError(t('errors.invalidStartingBalance'));
      return;
    }
    const input: AccountInput = { name, type, currency, opening_balance_cents: cents };
    try {
      await accountsApi.create(input);
      setName('');
      setBalance('');
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.create'));
    }
  }

  async function handleDelete(id: number) {
    try {
      await accountsApi.remove(id);
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.delete'));
    }
  }

  // Reconcile: the derived balance drifted from the bank — type what the bank
  // says and the backend adjusts the opening balance so the ledger's facts
  // stay untouched.
  const [reconcilingId, setReconcilingId] = useState<number | null>(null);
  const [reconcileAmount, setReconcileAmount] = useState('');
  const [reconcileError, setReconcileError] = useState<string | null>(null);

  function reconcileAccount() {
    return accounts.find((a) => a.id === reconcilingId);
  }
  function reconcileCurrency() {
    return reconcileAccount()?.currency ?? '';
  }
  function reconcileCurrentValue() {
    return reconcileAccount()?.balance_cents ?? 0;
  }

  function startReconcile(a: Account) {
    setReconcilingId(a.id);
    setReconcileAmount('');
    setReconcileError(null);
  }

  async function handleReconcile(e: React.FormEvent) {
    e.preventDefault();
    if (reconcilingId === null) return;
    const cents = dollarsToCents(reconcileAmount);
    if (Number.isNaN(cents)) {
      setReconcileError(t('errors.invalidBalance'));
      return;
    }
    try {
      await accountsApi.reconcile(reconcilingId, cents);
      setReconcilingId(null);
      reload();
    } catch (err) {
      setReconcileError(err instanceof Error ? err.message : t('errors.reconcile'));
    }
  }

  if (loading) return <Spinner />;
  if (error) return <ErrorMessage message={error.message} />;

  const accounts: Account[] = data ?? [];

  return (
    <div className="page">
      <h2 className="page-title">{t('title')}</h2>

      <Card title={t('add.title')}>
        <form className="form-row" onSubmit={handleCreate}>
          <input
            placeholder={t('add.namePlaceholder')}
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
          />
          <select value={type} onChange={(e) => setType(e.target.value as AccountType)}>
            {ACCOUNT_TYPES.map((ty) => (
              <option key={ty} value={ty}>
                {t(`types.${ty}`)}
              </option>
            ))}
          </select>
          <select
            value={currency}
            onChange={(e) => {
              setCurrency(e.target.value);
              setCurrencyTouched(true);
            }}
            aria-label={t('add.currencyAria')}
          >
            {currencyOptions(currency).map((c) => (
              <option key={c.code} value={c.code}>
                {c.code}
              </option>
            ))}
          </select>
          <input
            placeholder={t('add.balancePlaceholder', { currency })}
            inputMode="decimal"
            value={balance}
            onChange={(e) => setBalance(e.target.value)}
            required
          />
          <Button type="submit">{t('actions.add')}</Button>
        </form>
        {formError && <ErrorMessage message={formError} />}
      </Card>

      {accounts.length === 0 ? (
        <EmptyState message={t('empty')} />
      ) : (
        <ItemPanels>
          {accounts.map((a) => (
            <ItemPanel
              key={a.id}
              title={a.name}
              subtitle={t(`types.${a.type}`)}
              value={formatCents(a.balance_cents, a.currency)}
            >
              {editingId === a.id ? (
                <form className="form-stack" onSubmit={handleUpdate}>
                  <input
                    placeholder={t('add.namePlaceholder')}
                    value={editName}
                    onChange={(e) => setEditName(e.target.value)}
                    required
                  />
                  <select
                    value={editType}
                    onChange={(e) => setEditType(e.target.value as AccountType)}
                    aria-label={t('edit.typeAria')}
                  >
                    {ACCOUNT_TYPES.map((ty) => (
                      <option key={ty} value={ty}>
                        {t(`types.${ty}`)}
                      </option>
                    ))}
                  </select>
                  <select
                    value={editCurrency}
                    onChange={(e) => setEditCurrency(e.target.value)}
                    aria-label={t('add.currencyAria')}
                  >
                    {currencyOptions(editCurrency).map((c) => (
                      <option key={c.code} value={c.code}>
                        {c.code}
                      </option>
                    ))}
                  </select>
                  <input
                    placeholder={t('edit.balancePlaceholder', { currency: editCurrency })}
                    inputMode="decimal"
                    value={editBalance}
                    onChange={(e) => setEditBalance(e.target.value)}
                    required
                  />
                  <input
                    placeholder={t('edit.cardDigitsPlaceholder')}
                    inputMode="numeric"
                    maxLength={4}
                    value={editCardDigits}
                    onChange={(e) => setEditCardDigits(e.target.value.replace(/\D/g, ''))}
                  />
                  <div className="camera-row">
                    <Button type="submit">{t('actions.save')}</Button>
                    <Button type="button" variant="secondary" onClick={cancelEdit}>
                      {t('actions.cancel')}
                    </Button>
                  </div>
                </form>
              ) : (
                <>
                  <div className="item-field-grid">
                    <div className="item-field">
                      <span>{t('labels.type')}</span>
                      <span>{t(`types.${a.type}`)}</span>
                    </div>
                    <div className="item-field">
                      <span>{t('labels.currency')}</span>
                      <span>{a.currency}</span>
                    </div>
                    <div className="item-field">
                      <span>{t('labels.balance')}</span>
                      <strong>{formatCents(a.balance_cents, a.currency)}</strong>
                    </div>
                  </div>
                  {a.card_last_digits && (
                    <div className="item-field">
                      <span>{t('labels.card')}</span>
                      <span>•••• {a.card_last_digits}</span>
                    </div>
                  )}
                  <div className="item-field">
                    <span>{t('labels.openingBalance')}</span>
                    <strong>{formatCents(a.opening_balance_cents, a.currency)}</strong>
                  </div>
                  <div className="camera-row">
                    <Link className="btn btn-secondary" to={`/transactions?account=${a.id}`}>
                      {t('actions.register')}
                    </Link>
                    <Button onClick={() => startEdit(a)}>{t('actions.edit')}</Button>
                    <Button variant="secondary" onClick={() => startReconcile(a)}>
                      {t('actions.reconcile')}
                    </Button>
                    <Button variant="danger" onClick={() => handleDelete(a.id)}>
                      {t('actions.delete')}
                    </Button>
                  </div>
                </>
              )}
            </ItemPanel>
          ))}
        </ItemPanels>
      )}

      {reconcilingId !== null && (
        <Dialog title={t('dialogs.reconcile.title')}>
          <form className="form-stack" onSubmit={handleReconcile}>
            <p className="hint-text">
              {t('dialogs.reconcile.hint', {
                balance: formatCents(reconcileCurrentValue(), reconcileCurrency()),
              })}
            </p>
            <label>
              {t('dialogs.reconcile.statedLabel', { currency: reconcileCurrency() })}
              <input
                inputMode="decimal"
                value={reconcileAmount}
                onChange={(e) => setReconcileAmount(e.target.value)}
                required
                autoFocus
              />
            </label>
            {reconcileError && <ErrorMessage message={reconcileError} />}
            <div className="form-actions">
              <Button type="button" variant="secondary" onClick={() => setReconcilingId(null)}>
                {t('actions.cancel')}
              </Button>
              <Button type="submit">{t('actions.save')}</Button>
            </div>
          </form>
        </Dialog>
      )}
    </div>
  );
}