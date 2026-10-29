import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useAsync } from '../../hooks/useAsync';
import { accountGroupsApi, accountsApi } from '../../api/accounts';
import { bagsApi } from '../../api/bags';
import { settingsApi } from '../../api/settings';
import type { Account, AccountGroup, AccountInput, AccountType } from '../../types/domain';
import { formatCents, dollarsToCents } from '../../lib/money';
import { COMMON_CURRENCIES, type CurrencyOption } from '../../lib/currencies';
import { Button, Card, Spinner, ErrorMessage, EmptyState, ItemPanel, ItemPanels, Dialog, Field } from '../../components/ui';

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
  // One fetch for both lists: every mutation reloads accounts AND groups, so
  // a rename shows up in the membership selects the moment the page rebuilds.
  const { data, loading, error, reload } = useAsync(async () => {
    const [accounts, groups] = await Promise.all([accountsApi.list(), accountGroupsApi.list()]);
    return { accounts, groups };
  }, []);
  // New accounts default to the user's base display currency.
  const baseCurrency = useAsync(() => settingsApi.getBaseCurrency(), []);
  // Bag overview, for the split block a bag account shows in its expansion:
  // the overview already folds allocations and tagged spend per bag in the
  // account's own currency, so no second conversion happens here.
  const bagViews = useAsync(() => bagsApi.overview(), []);
  const [name, setName] = useState('');
  const [type, setType] = useState<AccountType>('checking');
  const [balance, setBalance] = useState('');
  const [groupId, setGroupId] = useState(0); // 0 = ungrouped
  const [formError, setFormError] = useState<string | null>(null);

  // Edit-in-place state: only one account is edited at a time, so a single
  // set of fields keyed by editingId is enough.
  const [editingId, setEditingId] = useState<number | null>(null);
  const [editName, setEditName] = useState('');
  const [editType, setEditType] = useState<AccountType>('checking');
  const [editBalance, setEditBalance] = useState('');
  const [editCardDigits, setEditCardDigits] = useState('');
  const [editGroupId, setEditGroupId] = useState(0);

  // Group management: an add row plus a rename dialog; deleting asks nothing
  // (the accounts only lose the folder, never their data).
  const [newGroupName, setNewGroupName] = useState('');
  const [groupError, setGroupError] = useState<string | null>(null);
  const [renameId, setRenameId] = useState<number | null>(null);
  const [renameName, setRenameName] = useState('');

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
    setEditGroupId(a.group_id ?? 0);
    setFormError(null);
  }

  function cancelEdit() {
    setEditingId(null);
    setFormError(null);
  }

  async function reloadAll() {
    setNewGroupName('');
    await reload();
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
      group_id: editGroupId > 0 ? editGroupId : undefined,
    };
    try {
      await accountsApi.update(editingId, input);
      setEditingId(null);
      reloadAll();
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
    if (groupId > 0) input.group_id = groupId;
    try {
      await accountsApi.create(input);
      setName('');
      setBalance('');
      setGroupId(0);
      reloadAll();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.create'));
    }
  }

  async function handleDelete(id: number) {
    try {
      await accountsApi.remove(id);
      reloadAll();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.delete'));
    }
  }

  async function handleCreateGroup(e: React.FormEvent) {
    e.preventDefault();
    if (!newGroupName.trim()) return;
    setGroupError(null);
    try {
      await accountGroupsApi.create(newGroupName.trim());
      await reloadAll();
    } catch (err) {
      setGroupError(err instanceof Error ? err.message : t('groups.errors.add'));
    }
  }

  async function handleRenameGroup(e: React.FormEvent) {
    e.preventDefault();
    if (renameId === null) return;
    setGroupError(null);
    try {
      await accountGroupsApi.update(renameId, renameName.trim());
      setRenameId(null);
      await reloadAll();
    } catch (err) {
      setGroupError(err instanceof Error ? err.message : t('groups.errors.rename'));
    }
  }

  async function handleDeleteGroup(id: number) {
    setGroupError(null);
    try {
      await accountGroupsApi.remove(id);
      if (groupId === id) setGroupId(0);
      await reloadAll();
    } catch (err) {
      setGroupError(err instanceof Error ? err.message : t('groups.errors.delete'));
    }
  }

  // Reconcile: the derived balance drifted from the bank — type what the bank
  // says and the backend adjusts the opening balance so the ledger's facts
  // stay untouched.
  const [reconcilingId, setReconcilingId] = useState<number | null>(null);
  const [reconcileAmount, setReconcileAmount] = useState('');
  const [reconcileError, setReconcileError] = useState<string | null>(null);

  function reconcileAccount() {
    return (data?.accounts ?? []).find((a) => a.id === reconcilingId);
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
      reloadAll();
    } catch (err) {
      setReconcileError(err instanceof Error ? err.message : t('errors.reconcile'));
    }
  }

  if (loading) return <Spinner />;
  if (error) return <ErrorMessage message={error.message} />;

  const accounts: Account[] = data?.accounts ?? [];
  const groups: AccountGroup[] = data?.groups ?? [];

  // Folder-style sections: one per group (groups arrive name-sorted), then
  // the ungrouped accounts last. A group without members is invisible — a
  // folder only shows when it holds an account.
  const sections = [
    ...groups.map((g) => ({ group: g, items: accounts.filter((a) => a.group_id === g.id) })),
    { group: null, items: accounts.filter((a) => !a.group_id || !groups.some((g) => g.id === a.group_id)) },
  ].filter((s) => s.items.length > 0);

  const groupSelect = (value: number, onChange: (v: number) => void, ariaLabel: string) => (
    <select value={value} onChange={(e) => onChange(Number(e.target.value))} aria-label={ariaLabel}>
      <option value={0}>{t('groups.none')}</option>
      {groups.map((g) => (
        <option key={g.id} value={g.id}>
          {g.name}
        </option>
      ))}
    </select>
  );

  // The bag-overview row for an account, if the account has bags at all.
  const bagViewFor = (id: number) =>
    (bagViews.data?.accounts ?? []).find((v) => v.account_id === id);

  const renderAccount = (a: Account) => (
    <ItemPanel key={a.id} title={a.name} subtitle={t(`types.${a.type}`)} value={formatCents(a.balance_cents, a.currency)}>
      {editingId === a.id ? (
        <form className="form-stack" onSubmit={handleUpdate}>
          <Field label={t('labels.name')}>
            <input
              placeholder={t('add.namePlaceholder')}
              value={editName}
              onChange={(e) => setEditName(e.target.value)}
              required
            />
          </Field>
          <div className="form-row">
            <Field label={t('labels.type')}>
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
            </Field>
            <Field label={t('labels.group')}>
              {groupSelect(editGroupId, setEditGroupId, t('groups.aria'))}
            </Field>
          </div>
          <Field label={t('labels.currency')}>
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
          </Field>
          <Field label={t('labels.openingBalance')}>
            <input
              placeholder={t('edit.balancePlaceholder', { currency: editCurrency })}
              inputMode="decimal"
              value={editBalance}
              onChange={(e) => setEditBalance(e.target.value)}
              required
            />
          </Field>
          <Field label={t('labels.card')}>
            <input
              placeholder={t('edit.cardDigitsPlaceholder')}
              inputMode="numeric"
              maxLength={4}
              value={editCardDigits}
              onChange={(e) => setEditCardDigits(e.target.value.replace(/\D/g, ''))}
            />
          </Field>
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
            {(() => {
              // Accounts with bags split the balance in three on the spot:
              // the total, what the bags currently hold (Σ their derived
              // balances — allocations minus tagged spend, the overview
              // folds it in the account's currency), and the unbagged rest.
              const view = bagViewFor(a.id);
              // Only accounts whose bags include at least one OPEN bag show
              // the split — an all-closed account has no reservations.
              if (!view || !view.bags.some((b) => b.status === 'open')) {
                return (
                  <div className="item-field">
                    <span>{t('labels.balance')}</span>
                    <strong>{formatCents(a.balance_cents, a.currency)}</strong>
                  </div>
                );
              }
              return (
                <>
                  <div className="item-field">
                    <span>{t('labels.total')}</span>
                    <strong>{formatCents(a.balance_cents, a.currency)}</strong>
                  </div>
                  <div className="item-field">
                    <span>{t('labels.inBags')}</span>
                    <strong>
                      {formatCents(
                        view.bags
                          .filter((b) => b.status === 'open')
                          .reduce((sum, b) => sum + b.balance_cents, 0),
                        a.currency,
                      )}
                    </strong>
                  </div>
                  <div className={`item-field${view.unbagged_cents < 0 || view.oversubscribed ? ' over' : ''}`}>
                    <span>{t('labels.available')}</span>
                    <strong>{formatCents(view.unbagged_cents, a.currency)}</strong>
                  </div>
                </>
              );
            })()}
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
  );

  return (
    <div className="page">
      <h2 className="page-title">{t('title')}</h2>

      <Card title={t('add.title')}>
        <form className="form-row" onSubmit={handleCreate}>
          <Field label={t('labels.name')}>
            <input
              placeholder={t('add.namePlaceholder')}
              value={name}
              onChange={(e) => setName(e.target.value)}
              required
            />
          </Field>
          <Field label={t('labels.type')}>
            <select value={type} onChange={(e) => setType(e.target.value as AccountType)}>
              {ACCOUNT_TYPES.map((ty) => (
                <option key={ty} value={ty}>
                  {t(`types.${ty}`)}
                </option>
              ))}
            </select>
          </Field>
          <Field label={t('labels.currency')}>
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
          </Field>
          <Field label={t('labels.startingBalance')}>
            <input
              placeholder={t('add.balancePlaceholder', { currency })}
              inputMode="decimal"
              value={balance}
              onChange={(e) => setBalance(e.target.value)}
              required
            />
          </Field>
          {groups.length > 0 && (
            <Field label={t('labels.group')}>
              {groupSelect(groupId, setGroupId, t('groups.aria'))}
            </Field>
          )}
          <Button type="submit">{t('actions.add')}</Button>
        </form>
        {formError && <ErrorMessage message={formError} />}
      </Card>

      <Card title={t('groups.title')}>
        <form className="form-row" onSubmit={handleCreateGroup}>
          <Field label={t('groups.nameLabel')}>
            <input
              placeholder={t('groups.namePlaceholder')}
              value={newGroupName}
              onChange={(e) => setNewGroupName(e.target.value)}
              maxLength={100}
            />
          </Field>
          <Button type="submit" variant="secondary">
            {t('groups.add')}
          </Button>
        </form>
        {groupError && <ErrorMessage message={groupError} />}
        {groups.length > 0 && (
          <div className="group-manage-list">
            {groups.map((g) => (
              <div className="group-manage-row" key={g.id}>
                <span>{g.name}</span>
                <span className="stat-label">
                  {accounts.filter((a) => a.group_id === g.id).length}{' '}
                  {t('groups.memberCount', { count: accounts.filter((a) => a.group_id === g.id).length })}
                </span>
                <span className="camera-row">
                  <Button variant="secondary" onClick={() => { setRenameId(g.id); setRenameName(g.name); }}>
                    {t('actions.edit')}
                  </Button>
                  <Button variant="danger" onClick={() => handleDeleteGroup(g.id)}>
                    {t('groups.delete')}
                  </Button>
                </span>
              </div>
            ))}
          </div>
        )}
      </Card>

      {accounts.length === 0 ? (
        <EmptyState message={t('empty')} />
      ) : (
        sections.map((s) => (
          <section key={s.group?.id ?? -1} className="acct-group-section">
            <h3 className="acct-group-title">
              {s.group ? s.group.name : t('groups.ungrouped')}
            </h3>
            <ItemPanels>{s.items.map(renderAccount)}</ItemPanels>
          </section>
        ))
      )}

      {reconcilingId !== null && (
        <Dialog
  onClose={() => {
    setReconcilingId(null);
    setReconcileAmount('');
    setReconcileError(null);
  }}
  title={t('dialogs.reconcile.title')}
  help={t('dialogs.reconcile.hint', {
    balance: formatCents(reconcileCurrentValue(), reconcileCurrency()),
  })}
>
          <form className="form-stack" onSubmit={handleReconcile}>
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

      {renameId !== null && (
        <Dialog title={t('groups.renameTitle')} onClose={() => setRenameId(null)}>
          <form className="form-stack" onSubmit={handleRenameGroup}>
            <Field label={t('groups.nameLabel')} hint={t('groups.nameHint')}>
              <input value={renameName} onChange={(e) => setRenameName(e.target.value)} required autoFocus />
            </Field>
            <div className="form-actions">
              <Button type="button" variant="secondary" onClick={() => setRenameId(null)}>
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