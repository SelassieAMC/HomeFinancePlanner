import { useEffect, useMemo, useState } from 'react';
import type { Account, Category, Store, Transaction, TransactionInput, TransactionKind } from '../../types/domain';
import { dollarsToCents, formatCents } from '../../lib/money';
import { categoriesBySection } from '../../lib/categories';
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
  category_id: number | null;
  qty: string;
  price: string;
  discount: string;
}

let nextLineKey = 1;

function newLine(): LineDraft {
  return { key: nextLineKey++, name: '', brand: '', unit: '', category_id: null, qty: '1', price: '', discount: '' };
}

function lineFromItem(item: NonNullable<Transaction['items']>[number]): LineDraft {
  return {
    key: nextLineKey++,
    name: item.name,
    brand: item.brand ?? '',
    unit: item.unit ?? '',
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
  const qty = Number(line.qty);
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
  const [kind, setKind] = useState<TransactionKind>(initial?.kind ?? 'expense');
  const [date, setDate] = useState(initial?.date ?? new Date().toISOString().slice(0, 10));
  const [amount, setAmount] = useState(initial ? (initial.amount_cents / 100).toString() : '');
  const [accountId, setAccountId] = useState(initial ? String(initial.account_id) : '');
  const [categoryId, setCategoryId] = useState(initial?.category_id ? String(initial.category_id) : '');
  const [description, setDescription] = useState(initial?.description ?? '');

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

  function updateLine(key: number, patch: Partial<LineDraft>) {
    setLines((ls) => ls.map((line) => (line.key === key ? { ...line, ...patch } : line)));
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);

    if (!Number.isFinite(amountCents) || amountCents <= 0) {
      setFormError(
        hasLines
          ? 'Complete the item lines so the amount is positive. Pure money back is recorded as an income transaction without item lines.'
          : 'Enter a positive amount.',
      );
      return;
    }
    if (!accountId) {
      setFormError('Choose an account.');
      return;
    }
    const items = [];
    for (const line of lines) {
      const total = lineCents(line, categories);
      if (Number.isNaN(total)) {
        setFormError(`Complete the numbers for “${line.name || 'unnamed item'}”.`);
        return;
      }
      const price = dollarsToCents(line.price);
      const discount = line.discount.trim() === '' ? 0 : dollarsToCents(line.discount);
      // "Leergut" and allows_negative categories are money back; anything
      // else must not go negative (the backend rejects it).
      if (!lineAllowsNegative(line, categories) && (price < 0 || discount < 0)) {
        setFormError(
          `Prices for “${line.name || 'unnamed item'}” must not be negative — file it under “Deposit & Returns” to record money back.`,
        );
        return;
      }
      items.push({
        name: line.name.trim(),
        brand: line.brand.trim() || undefined,
        unit: line.unit || undefined,
        category_id: line.category_id,
        quantity: Number(line.qty),
        unit_price_cents: price,
        discount_cents: discount,
      });
    }

    setBusy(true);
    try {
      await onSubmit({
        account_id: Number(accountId),
        category_id: categoryId ? Number(categoryId) : null,
        kind,
        amount_cents: amountCents,
        description,
        date,
        store_name: customStore ? customStoreName : (pickedStore?.name ?? ''),
        items: items.length > 0 ? items : undefined,
      });
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to save the transaction.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="form-grid" onSubmit={handleSubmit}>
      <div className="form-row">
        <select
          value={kind}
          onChange={(e) => setKind(e.target.value as TransactionKind)}
          aria-label="Kind"
        >
          <option value="expense">Expense</option>
          <option value="income" disabled={hasLines}>
            Income {hasLines ? '(no items on income)' : ''}
          </option>
        </select>
        <input type="date" value={date} onChange={(e) => setDate(e.target.value)} required aria-label="Date" />
        <input
          placeholder="Amount ($)"
          inputMode="decimal"
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
          required
          readOnly={hasLines}
          title={hasLines ? 'Calculated from the item lines' : undefined}
          aria-label={hasLines ? 'Amount (calculated from items)' : 'Amount'}
        />
        <select value={accountId} onChange={(e) => setAccountId(e.target.value)} required aria-label="Account">
          <option value="">Account…</option>
          {accounts.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name} ({a.currency})
            </option>
          ))}
        </select>
        <select value={categoryId} onChange={(e) => setCategoryId(e.target.value)} aria-label="Category">
          <option value="">Category…</option>
          {categoriesBySection(categories, 'expense').map(([section, cats]) => (
            <optgroup key={section} label={section}>
              {cats.map((c) => (
                <option key={c.id} value={c.id} title={c.description}>
                  {c.icon ? `${c.icon} ${c.name}` : c.name}
                </option>
              ))}
            </optgroup>
          ))}
        </select>
      </div>
      <div className="form-row">
        <input
          placeholder="Description"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          maxLength={500}
          aria-label="Description"
        />
        {customStore ? (
          <input
            autoFocus
            placeholder="New market…"
            value={customStoreName}
            onChange={(e) => setCustomStoreName(e.target.value)}
            aria-label="New market name"
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
            aria-label="Store (optional)"
          >
            <option value="">No store</option>
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
            <option value="__custom__">+ Add market…</option>
          </select>
        )}
      </div>

      <div className="transaction-lines">
        <div className="transaction-lines-header">
          <span>Items {hasLines ? '' : '(optional)'}</span>
          <Button type="button" variant="secondary" onClick={() => setLines((ls) => [...ls, newLine()])}>
            + Add item
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
                  // auto-created on save.
                  updateLine(line.key, {
                    name: p ? p.name : line.name,
                    brand: p?.brand || line.brand,
                    unit: p?.unit || line.unit,
                    category_id: p?.category_id ?? line.category_id,
                  });
                }}
                currency={currency}
                placeholder={`Item ${i + 1} name`}
                ariaLabel={`Item ${i + 1} name`}
              />
              <input
                className="transaction-line-num"
                type="number"
                min="0"
                step="any"
                value={line.qty}
                onChange={(e) => updateLine(line.key, { qty: e.target.value })}
                aria-label={`Item ${i + 1} quantity`}
                placeholder="Qty"
              />
              <UnitSelect
                value={line.unit}
                onChange={(unit) => updateLine(line.key, { unit })}
                ariaLabel={`Item ${i + 1} unit`}
              />
              <input
                className="transaction-line-num"
                inputMode="decimal"
                value={line.price}
                onChange={(e) => updateLine(line.key, { price: e.target.value })}
                aria-label={`Item ${i + 1} unit price`}
                placeholder="Price"
              />
              <input
                className="transaction-line-num"
                inputMode="decimal"
                value={line.discount}
                onChange={(e) => updateLine(line.key, { discount: e.target.value })}
                aria-label={`Item ${i + 1} discount`}
                placeholder="Disc."
              />
              <CategorySelect
                categories={categories}
                kind="product"
                value={line.category_id}
                onChange={(id) => updateLine(line.key, { category_id: id })}
                ariaLabel={`Item ${i + 1} product category`}
              />
              <span className="transaction-line-total">
                {Number.isNaN(total) ? '—' : formatCents(total, currency)}
              </span>
              <Button
                type="button"
                variant="ghost"
                aria-label={`Remove item ${i + 1}`}
                onClick={() => setLines((ls) => ls.filter((l) => l.key !== line.key))}
              >
                ✕
              </Button>
            </div>
          );
        })}
        {hasLines && (
          <div className="transaction-lines-footer">
            <span>Items total</span>
            <strong>{formatCents(itemsTotal, currency)}</strong>
          </div>
        )}
      </div>

      {formError && <ErrorMessage message={formError} />}

      <div className="camera-row">
        <Button type="submit" disabled={busy}>
          {busy ? 'Saving…' : mode === 'create' ? 'Add transaction' : 'Save changes'}
        </Button>
        {onCancel && (
          <Button type="button" variant="secondary" onClick={onCancel} disabled={busy}>
            Cancel
          </Button>
        )}
      </div>
    </form>
  );
}