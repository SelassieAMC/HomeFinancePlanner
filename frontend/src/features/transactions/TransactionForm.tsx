import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { Account, Category, Store, Transaction, TransactionInput, TransactionKind } from '../../types/domain';
import { productsApi } from '../../api/products';
import { bagsApi } from '../../api/bags';
import { dollarsToCents, formatCents, parseDecimalInput } from '../../lib/money';
import { useAsync } from '../../hooks/useAsync';
import {
  Button,
  CategorySelect,
  ErrorMessage,
  ProductAutocomplete,
  UnitSelect,
} from '../../components/ui';

// TransactionForm is the create form and the edit modal's body in one: header
// fields (kind, date, amount, account, category, description, store) plus the
// item lines of a manual purchase with product autocomplete. Unknown product
// names are find-or-created server-side on save, like the bill flow.
interface Props {
  accounts: Account[];
  categories: Category[];
  stores: Store[];
  mode: 'create' | 'edit';
  initial?: Transaction | null;
  /** Submits the payload; throws to surface the error inline. */
  onSubmit: (input: TransactionInput) => Promise<void>;
  onCancel?: () => void;
}

// A session-local line being edited; keys exist only for React lists.
interface LineDraft {
  key: number;
  name: string;
  brand: string;
  unit: string;
  /** Printed size magnitude ("500" for "500ml"); '' = unknown. */
  unit_value: string;
  category_id: number | null;
  qty: string;
  price: string;
  discount: string;
  /** Name the last normalization lookup ran for (repeat blurs are free). */
  normalizedFor?: string;
  /** Standardized name the raw text maps to — display-only hint. */
  normalizedName?: string;
  /** Generic product family the raw text maps to — display-only hint. */
  normalizedGeneric?: string;
}

let nextLineKey = 1;

function newLine(): LineDraft {
  return {
    key: nextLineKey++,
    name: '',
    brand: '',
    unit: '',
    unit_value: '',
    category_id: null,
    qty: '1',
    price: '',
    discount: '',
  };
}

function lineFromItem(item: NonNullable<Transaction['items']>[number]): LineDraft {
  return {
    key: nextLineKey++,
    name: item.name,
    brand: item.brand ?? '',
    unit: item.unit ?? '',
    unit_value: item.unit_value != null ? String(item.unit_value) : '',
    category_id: item.category_id ?? null,
    qty: String(item.quantity),
    price: (item.unit_price_cents / 100).toString(),
    discount: (item.discount_cents / 100).toString(),
  };
}

// lineAllowsNegative mirrors the backend rule: "Leergut" lines and lines
// filed under an allows_negative category (the "Deposit & Returns" / Pfand
// family) are money back — their price may go negative.
function lineAllowsNegative(line: LineDraft, categories: Category[]): boolean {
  if (line.name.toLowerCase().includes('leergut')) return true;
  return Boolean(line.category_id && categories.find((c) => c.id === line.category_id)?.allows_negative);
}

// lineCents recomputes a line's total the same way the backend does
// (quantity × unit price − discount, clamped to ≥ 0 unless the line allows
// negatives); NaN until filled in.
function lineCents(line: LineDraft, categories: Category[]): number {
  // parseDecimalInput accepts the comma decimal separator mobile numeric
  // keypads type on non-US locales (Number('1,5') is NaN there).
  const qty = parseDecimalInput(line.qty);
  const price = dollarsToCents(line.price);
  const discount = line.discount.trim() === '' ? 0 : dollarsToCents(line.discount);
  if (!Number.isFinite(qty) || qty <= 0 || Number.isNaN(price) || Number.isNaN(discount) || discount < 0) {
    return NaN;
  }
  const total = Math.round(qty * price - discount);
  if (total < 0 && !lineAllowsNegative(line, categories)) return 0;
  return total;
}

export function TransactionForm({ accounts, categories, stores, mode, initial, onSubmit, onCancel }: Props) {
  const { t } = useTranslation('transactions');
  const [kind, setKind] = useState<TransactionKind>(initial?.kind ?? 'expense');
  const [date, setDate] = useState(initial?.date ?? new Date().toISOString().slice(0, 10));
  const [amount, setAmount] = useState(initial ? (initial.amount_cents / 100).toString() : '');
  const [accountId, setAccountId] = useState(initial ? String(initial.account_id) : '');
  // Transfers only: the destination account.
  const [counterAccountId, setCounterAccountId] = useState(
    initial?.counter_account_id ? String(initial.counter_account_id) : '',
  );
  const [categoryId, setCategoryId] = useState(initial?.category_id ? String(initial.category_id) : '');
  const [description, setDescription] = useState(initial?.description ?? '');
  // Expenses only: the bag (allocation envelope) the spend draws from. Income
  // and transfers write no reserve — the select hides and the field clears.
  const [bagId, setBagId] = useState(
    initial?.bag_id && initial.kind === 'expense' ? String(initial.bag_id) : '',
  );
  const bags = useAsync(() => bagsApi.overview(), []);

  // Store picker: a select of known stores with an escape hatch for a new
  // market (the backend find-or-creates it on save), like the bill editor.
  const [storeId, setStoreId] = useState(initial?.store_id ? String(initial.store_id) : '');
  const [customStore, setCustomStore] = useState(false);
  const [customStoreName, setCustomStoreName] = useState(initial?.store_name ?? '');

  const [lines, setLines] = useState<LineDraft[]>(
    initial?.items?.length ? initial.items.map(lineFromItem) : [],
  );
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  const sortedStores = useMemo(() => [...stores].sort((a, b) => a.name.localeCompare(b.name)), [stores]);
  const pickedStore = stores.find((s) => String(s.id) === storeId) ?? null;

  // Optional account: creating defaults to the primary (first listed) one —
  // the same default the backend applies when account_id is omitted on the
  // wire. Runs once the accounts arrive; the select still lists them all.
  useEffect(() => {
    const primary = accounts[0];
    if (!initial && !accountId && primary) setAccountId(String(primary.id));
  }, [initial, accountId, accounts]);

  // Display currency: the selected account's, falling back to the edited
  // transaction's. formatCents needs a real ISO code, so USD is the last resort.
  const currency =
    accounts.find((a) => String(a.id) === accountId)?.currency ?? initial?.currency ?? 'USD';

  // With item lines the committed amount IS the items total — Σ(qty × price
  // − discount), clamped ≥ 0 unless a line allows negatives — so it stays in
  // sync with the lines. Without lines the amount is typed manually (income,
  // or a quick expense).
  const itemsTotal = lines.reduce((sum, line) => {
    const cents = lineCents(line, categories);
    return Number.isNaN(cents) ? sum : sum + cents;
  }, 0);
  const hasLines = lines.length > 0;
  const amountCents = hasLines ? itemsTotal : dollarsToCents(amount);

  // Keep the amount field itself showing the computed total while lines
  // exist; removing all lines leaves its last value for manual editing.
  useEffect(() => {
    if (hasLines) setAmount((itemsTotal / 100).toFixed(2));
  }, [hasLines, itemsTotal]);

  const isTransfer = kind === 'transfer';
  const transferAccount =
    accounts.find((a) => String(a.id) === counterAccountId) ?? null;

  // Open bags of the chosen account (a bag lives inside one account — the
  // backend refuses a cross-account tag, so the select filters up front).
  const accountBags = useMemo(
    () =>
      (bags.data?.accounts ?? [])
        .find((v) => String(v.account_id) === accountId)
        ?.bags.filter((b) => b.status === 'open') ?? [],
    [bags.data, accountId],
  );
  // A bag belonging to a different account than the newly picked one silently
  // unselects — the submit would otherwise be refused.
  useEffect(() => {
    if (bagId && !accountBags.some((b) => String(b.id) === bagId)) setBagId('');
  }, [bagId, accountBags]);

  function updateLine(key: number, patch: Partial<LineDraft>) {
    setLines((ls) => ls.map((line) => (line.key === key ? { ...line, ...patch } : line)));
  }

  // Normalization-memory lookup when the name field is left: shows the
  // standardized name the raw text maps to and fills the product category
  // when unset. The typed name is never changed — unknown names are recorded
  // as identity mappings server-side on save. Deposit/return lines are
  // never normalized.
  async function lookupNormalization(line: LineDraft) {
    const name = line.name.trim();
    if (
      !name ||
      name.toLowerCase().includes('leergut') ||
      line.normalizedFor === name
    ) {
      return;
    }
    updateLine(line.key, {
      normalizedFor: name,
      normalizedName: undefined,
      normalizedGeneric: undefined,
    });
    try {
      const res = await productsApi.normalizeName(name);
      if (!res.matched) return;
      // Apply only when the name is still the one looked up — the line may
      // have been edited (or picked from the autocomplete) in the meantime.
      setLines((ls) =>
        ls.map((l) =>
          l.key === line.key && l.name.trim() === name
            ? {
                ...l,
                normalizedName: res.standard_name,
                normalizedGeneric: res.generic_name,
                category_id: l.category_id ?? (res.category_id ?? null),
              }
            : l,
        ),
      );
    } catch {
      // Best-effort hint; failures just leave it hidden.
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);

    if (!Number.isFinite(amountCents) || amountCents <= 0) {
      setFormError(
        hasLines
          ? t('form.error.linesIncomplete')
          : t('form.error.positiveAmount'),
      );
      return;
    }
    if (!accountId) {
      setFormError(t('form.error.chooseAccount'));
      return;
    }
    if (isTransfer && !counterAccountId) {
      setFormError(t('form.error.transferDestination'));
      return;
    }
    if (isTransfer && counterAccountId === accountId) {
      setFormError(t('form.error.twoAccounts'));
      return;
    }
    const items = [];
    for (const line of lines) {
      if (line.name.trim() === '') {
        setFormError(t('form.error.lineName'));
        return;
      }
      const total = lineCents(line, categories);
      if (Number.isNaN(total)) {
        setFormError(t('form.error.numbers', { name: line.name || t('form.unnamed') }));
        return;
      }
      const price = dollarsToCents(line.price);
      const discount = line.discount.trim() === '' ? 0 : dollarsToCents(line.discount);
      // Printed size magnitude, decimal comma tolerated; blank or non-positive
      // stays "unknown".
      const unitValueRaw = line.unit_value.trim();
      const unitValueNumber = unitValueRaw === '' ? NaN : parseDecimalInput(unitValueRaw);
      const unitValue = Number.isFinite(unitValueNumber) && unitValueNumber > 0 ? unitValueNumber : undefined;
      // "Leergut" and allows_negative categories are money back; anything
      // else must not go negative (the backend rejects it).
      if (!lineAllowsNegative(line, categories) && (price < 0 || discount < 0)) {
        setFormError(
          t('form.error.negativePrice', { name: line.name || t('form.unnamed') }),
        );
        return;
      }
      items.push({
        name: line.name.trim(),
        brand: line.brand.trim() || undefined,
        unit: line.unit || undefined,
        unit_value: unitValue,
        category_id: line.category_id,
        quantity: parseDecimalInput(line.qty),
        unit_price_cents: price,
        discount_cents: discount,
      });
    }

    setBusy(true);
    try {
      await onSubmit({
        account_id: Number(accountId),
        category_id: isTransfer ? null : (categoryId ? Number(categoryId) : null),
        kind,
        amount_cents: amountCents,
        description,
        date,
        store_name: isTransfer ? '' : (customStore ? customStoreName : (pickedStore?.name ?? '')),
        counter_account_id: isTransfer ? Number(counterAccountId) : undefined,
        bag_id: kind === 'expense' && bagId ? Number(bagId) : undefined,
        items: items.length > 0 && !isTransfer ? items : undefined,
      });
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('form.error.save'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="form-stack" onSubmit={handleSubmit}>
      {/* The kind toggle replaces the old kind select — expense/income/transfer
          is a three-way switch, and item lines pin it to expense. */}
      <div className="segmented" role="group" aria-label={t('form.kindAria')}>
        <button
          type="button"
          className={kind === 'expense' ? 'active' : ''}
          aria-pressed={kind === 'expense'}
          onClick={() => setKind('expense')}
        >
          {t('form.kindExpense')}
        </button>
        <button
          type="button"
          className={kind === 'income' ? 'active' : ''}
          aria-pressed={kind === 'income'}
          disabled={hasLines}
          onClick={() => setKind('income')}
        >
          {t('form.kindIncome')}{hasLines ? t('form.noItems') : ''}
        </button>
        <button
          type="button"
          className={kind === 'transfer' ? 'active' : ''}
          aria-pressed={kind === 'transfer'}
          disabled={hasLines}
          onClick={() => setKind('transfer')}
        >
          {t('form.kindTransfer')}{hasLines ? t('form.noItems') : ''}
        </button>
      </div>
      <div className="form-grid">
        <label>
          {t('form.label.date')}
          <input
            type="date"
            value={date}
            onChange={(e) => setDate(e.target.value)}
            required
            // No future dating: transactions record money that already moved.
            max={new Date().toLocaleDateString('en-CA')}
          />
        </label>
        <label>
          {t('form.label.amount')}
          <input
            placeholder={hasLines ? t('form.placeholder.fromItems') : '0.00'}
            inputMode="decimal"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            required
            readOnly={hasLines}
            title={hasLines ? t('form.amountCalcTitle') : undefined}
            aria-label={hasLines ? t('form.aria.amountCalc') : t('form.aria.amount')}
          />
        </label>
        <label>
          {isTransfer ? t('form.label.fromAccount') : t('form.label.account')}
          <select
            value={accountId}
            onChange={(e) => setAccountId(e.target.value)}
            required
          >
            <option value="">{t('form.placeholder.account')}</option>
            {accounts.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name} ({a.currency})
              </option>
            ))}
          </select>
        </label>
        {isTransfer ? (
          <label>
            {t('form.label.toAccount')}
            <select
              value={counterAccountId}
              onChange={(e) => setCounterAccountId(e.target.value)}
              aria-label={t('form.aria.destinationAccount')}
              required
            >
              <option value="">{t('form.placeholder.toAccount')}</option>
              {accounts.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name} ({a.currency})
                </option>
              ))}
            </select>
          </label>
        ) : (
          <label>
            {t('form.label.category')}
            <CategorySelect
              categories={categories}
              kind="expense"
              value={categoryId ? Number(categoryId) : null}
              onChange={(id) => setCategoryId(id ? String(id) : '')}
              ariaLabel={t('form.label.category')}
              emptyLabel={t('form.placeholder.category')}
            />
          </label>
        )}
        {kind === 'expense' && accountBags.length > 0 && (
          <label>
            {t('form.label.fromBag')}
            <select
              value={bagId}
              onChange={(e) => setBagId(e.target.value)}
              aria-label={t('form.aria.fromBag')}
            >
              <option value="">{t('form.option.noBag')}</option>
              {accountBags.map((b) => (
                <option key={b.id} value={b.id}>
                  {b.name}
                </option>
              ))}
            </select>
          </label>
        )}
        {!isTransfer && (
          <label>
            {t('form.label.store')}
          {customStore ? (
            <input
              autoFocus
              placeholder={t('form.placeholder.newMarket')}
              value={customStoreName}
              onChange={(e) => setCustomStoreName(e.target.value)}
              aria-label={t('form.aria.newMarket')}
              onBlur={() => {
                // Nothing typed → back to the store list.
                if (customStoreName.trim() === '') setCustomStore(false);
              }}
            />
          ) : (
            <select
              value={storeId}
              onChange={(e) => {
                if (e.target.value === '__custom__') {
                  setCustomStore(true);
                  return;
                }
                setStoreId(e.target.value);
              }}
              aria-label={t('form.aria.store')}
            >
              <option value="">{t('form.option.noStore')}</option>
              {sortedStores.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
              {/* Keep the stored (unmatched) name selectable when editing —
                  the backend find-or-creates unknown names on save. */}
              {initial?.store_name && !pickedStore && (
                <option value={initial.store_name}>{initial.store_name}</option>
              )}
              <option value="__custom__">{t('form.option.addMarket')}</option>
            </select>
          )}
        </label>
        )}
        <label>
          {t('form.label.description')}
          <input
            placeholder={t('form.placeholder.note')}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            maxLength={500}
            aria-label={t('form.aria.description')}
          />
        </label>
      </div>

      {isTransfer && transferAccount &&
        accounts.find((a) => String(a.id) === accountId)?.currency !== transferAccount.currency && (
          <p className="hint-inline">
            {t('form.crossCurrency', { currency })}
          </p>
        )}

      {/* Transfers move money, they do not buy anything: no item lines. */}
      {!isTransfer && (
      <div className="transaction-lines">
        <div className="transaction-lines-header">
          <span>{t('form.lines.header')} {hasLines ? '' : t('form.lines.optional')}</span>
          <Button type="button" variant="secondary" className="btn-sm" onClick={() => setLines((ls) => [...ls, newLine()])}>
            {t('form.lines.addItem')}
          </Button>
        </div>
        {lines.map((line, i) => {
          const total = lineCents(line, categories);
          return (
            <div className="transaction-line" key={line.key}>
              <ProductAutocomplete
                value={line.name}
                onValueChange={(name) => updateLine(line.key, { name })}
                onPick={(p) => {
                  // Picking an existing product seeds its brand/unit and
                  // canonicalizes the name; an Escape-kept name is
                  // auto-created on save. A pick wins over the
                  // normalization hint, which is cleared.
                  updateLine(line.key, {
                    name: p ? p.name : line.name,
                    brand: p?.brand || line.brand,
                    unit: p?.unit || line.unit,
                    unit_value: p?.unit_value != null ? String(p.unit_value) : line.unit_value,
                    category_id: p?.category_id ?? line.category_id,
                    normalizedFor: undefined,
                    normalizedName: undefined,
                    normalizedGeneric: undefined,
                  });
                }}
                onBlur={() => void lookupNormalization(line)}
                currency={currency}
                placeholder={t('form.placeholder.itemName', { n: i + 1 })}
                ariaLabel={t('form.placeholder.itemName', { n: i + 1 })}
              />
              <input
                className="transaction-line-num"
                type="text"
                inputMode="decimal"
                value={line.qty}
                onChange={(e) => updateLine(line.key, { qty: e.target.value })}
                aria-label={t('form.lines.qtyAria', { n: i + 1 })}
                placeholder={t('form.lines.qty')}
              />
              <div className="unit-value-row">
                <UnitSelect
                  value={line.unit}
                  onChange={(unit) => updateLine(line.key, { unit })}
                  ariaLabel={t('form.lines.unitAria', { n: i + 1 })}
                />
                <input
                  type="text"
                  inputMode="decimal"
                  value={line.unit_value}
                  onChange={(e) => updateLine(line.key, { unit_value: e.target.value })}
                  aria-label={t('form.lines.sizeAria', { n: i + 1 })}
                  placeholder="500"
                />
              </div>
              <input
                className="transaction-line-num"
                inputMode="decimal"
                value={line.price}
                onChange={(e) => updateLine(line.key, { price: e.target.value })}
                aria-label={t('form.lines.unitPriceAria', { n: i + 1 })}
                placeholder={t('form.lines.price')}
              />
              <input
                className="transaction-line-num"
                inputMode="decimal"
                value={line.discount}
                onChange={(e) => updateLine(line.key, { discount: e.target.value })}
                aria-label={t('form.lines.discountAria', { n: i + 1 })}
                placeholder={t('form.lines.discount')}
              />
              <CategorySelect
                categories={categories}
                kind="product"
                value={line.category_id}
                onChange={(id) => updateLine(line.key, { category_id: id })}
                ariaLabel={t('form.lines.categoryAria', { n: i + 1 })}
              />
              <span className="transaction-line-total">
                {Number.isNaN(total) ? '—' : formatCents(total, currency)}
              </span>
              <Button
                type="button"
                variant="ghost"
                aria-label={t('form.lines.removeAria', { n: i + 1 })}
                onClick={() => setLines((ls) => ls.filter((l) => l.key !== line.key))}
              >
                ✕
              </Button>
              {line.normalizedName &&
                line.normalizedName.toLowerCase() !== line.name.trim().toLowerCase() && (
                  <span className="transaction-line-normalized">
                    {t('form.lines.knownAs', { name: line.normalizedName })}
                    {line.normalizedGeneric && ` · ${t('form.lines.knownFamily', { family: line.normalizedGeneric })}`}
                  </span>
                )}
              {!line.normalizedName && line.normalizedGeneric && (
                <span className="transaction-line-normalized">
                  {t('form.lines.family', { family: line.normalizedGeneric })}
                </span>
              )}
            </div>
          );
        })}
        {hasLines && (
          <div className="transaction-lines-footer">
            <span>{t('form.lines.total')}</span>
            <strong>{formatCents(itemsTotal, currency)}</strong>
          </div>
        )}
      </div>
      )}

      {formError && <ErrorMessage message={formError} />}

      <div className="form-actions">
        {onCancel && (
          <Button type="button" variant="secondary" onClick={onCancel} disabled={busy}>
            {t('form.action.cancel')}
          </Button>
        )}
        <Button type="submit" disabled={busy}>
          {busy ? t('form.action.saving') : mode === 'create' ? t('form.action.add') : t('form.action.save')}
        </Button>
      </div>
    </form>
  );
}