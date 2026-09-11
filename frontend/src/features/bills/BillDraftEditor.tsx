import { useMemo, useState } from 'react';
import type { BillConfirmInput, BillDraft, BillDraftItem, Budget, Category, Store } from '../../types/domain';
import { formatCents, dollarsToCents } from '../../lib/money';
import { COMMON_CURRENCIES } from '../../lib/currencies';
import { useAsync } from '../../hooks/useAsync';
import { settingsApi } from '../../api/settings';
import { Button, Spinner, ErrorMessage, EmptyState } from '../../components/ui';

// BillDraftEditor edits the scan draft client-side — nothing is persisted
// until Confirm. The layout is mobile-first: colored summary cards on top,
// one collapsible panel per article, and a computed total that recalculates
// on every price edit (with a warning when it no longer matches the receipt).

export type BillBusyAction = 'extract' | 'confirm' | 'discard' | null;

/**
 * 'draft' = review of a fresh scan (account picker + confirm/re-read/discard);
 * 'saved' = corrections to an already-saved bill (single Save action).
 */
export type BillEditorMode = 'draft' | 'saved';

export interface BillDraftEditorProps {
  draft: BillDraft;
  /** Replace the whole draft after a cell edit (state lives in the page). */
  onChange: (draft: BillDraft) => void;
  onConfirm: (accountId?: number, createCardAccount?: boolean) => void;
  /** Draft mode only: re-run extraction / drop the scan. */
  onRetry?: () => void;
  onDiscard?: () => void;
  mode?: BillEditorMode;
  busy?: BillBusyAction;
  error?: string | null;
  /** Account options for the confirm flow (omit to hide the picker). */
  accounts?: { id: number; name: string; card_last_digits?: string }[];
  /** Fixed storage taxonomy used for classification. */
  categories?: Category[];
  /** Brands already recorded on bill items — dropdown options. */
  brands?: string[];
  /** Budgets for the bill's month — correlation options. */
  budgets?: Budget[];
  /** Known stores for the market picker (omit to fall back to a plain input). */
  stores?: Store[];
}

const paymentOptions = ['', 'cash', 'card', 'credit', 'debit', 'transfer', 'voucher', 'other'];

/** Deposit/bottle return (e.g. "Leergut") — money back, negative amounts allowed. */
function isDepositReturn(name: string): boolean {
  return name.toLowerCase().includes('leergut');
}

/** Fixed measure vocabulary for the unit dropdown. */
const MEASURE_OPTIONS: { value: string; label: string }[] = [
  { value: 'liters', label: 'Liters' },
  { value: 'mililiters', label: 'Mililiters' },
  { value: 'grams', label: 'Grams' },
  { value: 'kilograms', label: 'Kilograms' },
  { value: 'per unit', label: 'Per unit' },
  { value: 'onzas', label: 'Onzas' },
];

/** Maps a receipt measure (kg, g, l, pcs, …) onto the fixed dropdown values. */
function normalizeUnit(raw: string | undefined): string {
  const u = (raw ?? '').trim().toLowerCase();
  if (!u) return '';
  const known = MEASURE_OPTIONS.find((m) => m.value === u);
  if (known) return known.value;
  const map: Record<string, string> = {
    kg: 'kilograms',
    kgs: 'kilograms',
    kilo: 'kilograms',
    kilos: 'kilograms',
    g: 'grams',
    gr: 'grams',
    l: 'liters',
    lt: 'liters',
    ml: 'mililiters',
    oz: 'onzas',
    onza: 'onzas',
    unit: 'per unit',
    units: 'per unit',
    pc: 'per unit',
    pcs: 'per unit',
    un: 'per unit',
    u: 'per unit',
  };
  return map[u] ?? u;
}

export function buildConfirmInput(draft: BillDraft, accountId?: number): BillConfirmInput {
  return {
    market_name: draft.market_name,
    date: draft.date,
    payment_method: draft.payment_method,
    card_last_digits: draft.card_last_digits ?? '',
    currency: draft.currency || 'USD',
    discount_cents: draft.discount_cents,
    vat_cents: draft.vat_cents,
    printed_total_cents: draft.printed_total_cents,
    budget_id: draft.budget_id ?? undefined,
    items: draft.items.map((it) => ({
      name: it.name,
      brand: it.brand,
      unit: it.unit,
      category_id: it.category_id ?? undefined,
      quantity: it.quantity,
      unit_price_cents: it.unit_price_cents,
      discount_cents: it.discount_cents,
      budget_id: it.budget_id ?? undefined,
    })),
    account_id: accountId,
  };
}

/** Emoji for an item's category; a generic cart when unclassified. */
function itemIcon(categoryId: number | null | undefined, categories: Category[]): string {
  const cat = categoryId ? categories.find((c) => c.id === categoryId) : undefined;
  return cat?.icon || '🛒';
}

/** Dropdown label for a budget: its category (icon + name) + amount. */
function budgetLabel(b: Budget, categories: Category[]): string {
  const cat = categories.find((c) => c.id === b.category_id);
  return cat ? `${cat.icon ?? ''} ${cat.name}`.trim() : `Category #${b.category_id}`;
}

export function BillDraftEditor({
  draft,
  onChange,
  onConfirm,
  onRetry,
  onDiscard,
  mode = 'draft',
  busy = null,
  error = null,
  accounts,
  categories = [],
  brands = [],
  budgets = [],
  stores = [],
}: BillDraftEditorProps) {
  const isBusy = busy !== null;
  const isSaved = mode === 'saved';
  const currency = draft.currency || 'USD';
  // Budget amounts are denominated in the base display currency, unlike the
  // bill's own native amounts.
  const baseCurrency = useAsync(() => settingsApi.getBaseCurrency(), []);

  const [search, setSearch] = useState('');
  // '' = no transaction, '__new_card__' = create a card account, else id.
  const [accountChoice, setAccountChoice] = useState('');
  // Item id currently typing a brand-new brand ('__custom__' selected).
  const [customBrandItem, setCustomBrandItem] = useState<number | null>(null);
  // True while typing a market name that is not an existing store.
  const [customMarket, setCustomMarket] = useState(false);

  const query = search.trim().toLowerCase();
  const visibleItems = query
    ? draft.items.filter((it) =>
        `${it.name} ${it.brand ?? ''}`.toLowerCase().includes(query),
      )
    : draft.items;

  // Everything money-wise is computed live from the edited lines. VAT is
  // already included in each item's price, so the total is just the sum.
  const linesSum = draft.items.reduce((sum, it) => sum + it.line_total_cents, 0);
  const computedTotal = linesSum;
  const printed = draft.printed_total_cents;
  const mismatch = printed > 0 && printed !== computedTotal;
  const savings =
    draft.items.reduce((sum, it) => sum + it.discount_cents, 0) + draft.discount_cents;

  // Product storage categories grouped by section for the <select> optgroups
  // (general expense categories are budget-level and stay out of item picks).
  const sections = useMemo(() => {
    const map = new Map<string, Category[]>();
    for (const c of categories) {
      if ((c.kind ?? 'product') !== 'product') continue;
      const key = c.section || 'Other';
      const list = map.get(key) ?? [];
      if (!map.has(key)) map.set(key, list);
      list.push(c);
    }
    return [...map.entries()];
  }, [categories]);

  // Brand dropdown options: known brands from saved bills plus every brand
  // already typed in this draft.
  const brandOptions = useMemo(() => {
    const set = new Set<string>(brands);
    for (const it of draft.items) {
      const b = (it.brand ?? '').trim();
      if (b) set.add(b);
    }
    return [...set].sort((a, b) => a.localeCompare(b));
  }, [brands, draft.items]);

  // Market picker state: the store matching the draft's market name
  // (case-insensitive), or null when the name is empty/new.
  const matchedStore = useMemo(
    () => stores.find((s) => s.name.toLowerCase() === draft.market_name.trim().toLowerCase()),
    [stores, draft.market_name],
  );

  function updateHeader(patch: Partial<BillDraft>) {
    onChange({ ...draft, ...patch });
  }

  // Deposit returns ("Leergut") and lines filed under a negative-allowed
  // category (the "Deposit & Returns" / Pfand family) are money back or
  // refund-like: their amounts may go negative and reduce the total.
  function negativeAllowed(it: BillDraftItem): boolean {
    return (
      it.is_return ||
      Boolean(it.category_id && categories.find((c) => c.id === it.category_id)?.allows_negative)
    );
  }

  function updateItem(id: number, patch: Partial<BillDraftItem>) {
    onChange({
      ...draft,
      items: draft.items.map((it) => {
        if (it.id !== id) return it;
        const next = { ...it, ...patch };
        // "Leergut" lines are bottle/crate deposit returns — money back, so
        // their amount may go negative and reduces the total.
        next.is_return = isDepositReturn(next.name);
        const line = next.quantity * next.unit_price_cents - next.discount_cents;
        next.line_total_cents = negativeAllowed(next)
          ? Math.round(line)
          : Math.max(0, Math.round(line));
        return next;
      }),
    });
  }

  /** Drops a line from the draft (wrong extraction, duplicated, …). */
  function removeItem(id: number) {
    onChange({ ...draft, items: draft.items.filter((it) => it.id !== id) });
  }

  function confirm() {
    if (accountChoice === '__new_card__') {
      onConfirm(undefined, true);
    } else if (accountChoice) {
      onConfirm(Number(accountChoice));
    } else {
      onConfirm();
    }
  }

  // Accounts already registered with this card's digits sort first.
  const sortedAccounts = [...(accounts ?? [])].sort((a, b) => {
    const aMatch = draft.card_last_digits && a.card_last_digits === draft.card_last_digits;
    const bMatch = draft.card_last_digits && b.card_last_digits === draft.card_last_digits;
    return (aMatch ? 0 : 1) - (bMatch ? 0 : 1);
  });

  const paymentLocked = Boolean(draft.card_last_digits);

  return (
    <div className="bill-draft">
      <div className="stat-cards">
        <div className="stat-card stat-market">
          <span className="stat-card-label">🏪 Market</span>
          {stores.length === 0 && !customMarket ? (
            // No stores loaded yet — keep the plain input behavior.
            <input
              className="stat-card-input"
              defaultValue={draft.market_name}
              placeholder="Unknown market"
              aria-label="Market name"
              onBlur={(e) =>
                e.target.value.trim() !== draft.market_name &&
                updateHeader({ market_name: e.target.value.trim() })
              }
            />
          ) : customMarket ? (
            <input
              className="stat-card-input"
              autoFocus
              defaultValue={draft.market_name}
              placeholder="New market…"
              aria-label="New market name"
              onBlur={(e) => {
                const value = e.target.value.trim();
                if (value !== draft.market_name) updateHeader({ market_name: value });
                setCustomMarket(false);
              }}
            />
          ) : (
            <select
              className="stat-card-input"
              value={matchedStore ? String(matchedStore.id) : draft.market_name}
              aria-label="Market name"
              onChange={(e) => {
                const value = e.target.value;
                if (value === '__custom__') {
                  setCustomMarket(true);
                  return;
                }
                if (value === '') {
                  updateHeader({ market_name: '' });
                  return;
                }
                const store = stores.find((s) => String(s.id) === value);
                if (store) updateHeader({ market_name: store.name });
              }}
            >
              <option value="">Unknown market</option>
              {[...stores]
                .sort((a, b) => a.name.localeCompare(b.name))
                .map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              {/* Keep the draft's own name selectable when it matches no store
                  yet — the backend find-or-creates it on confirm. */}
              {draft.market_name && !matchedStore && (
                <option value={draft.market_name}>{draft.market_name}</option>
              )}
              <option value="__custom__">+ Add market…</option>
            </select>
          )}
        </div>
        <div className="stat-card stat-total">
          <span className="stat-card-label">
            🧾 Total{' '}
            {mismatch && (
              <span
                className="warn-icon"
                title={`Printed on receipt: ${formatCents(printed, currency)}`}
              >
                ⚠️
              </span>
            )}
          </span>
          <span className="stat-card-value">{formatCents(computedTotal, currency)}</span>
          <span className="stat-card-sub">
            {mismatch ? `Printed: ${formatCents(printed, currency)}` : 'VAT included'}
          </span>
        </div>
        {!isSaved && (
        <div className="stat-card stat-account">
          <span className="stat-card-label">💳 Account</span>
          <select
            value={accountChoice}
            aria-label="Account to record the expense (optional)"
            onChange={(e) => setAccountChoice(e.target.value)}
            disabled={isBusy}
          >
            <option value="">No transaction</option>
            {draft.card_last_digits && (
              <option value="__new_card__">New card (•{draft.card_last_digits})</option>
            )}
            {sortedAccounts.map((a) => {
              const isMatch =
                draft.card_last_digits && a.card_last_digits === draft.card_last_digits;
              return (
                <option key={a.id} value={a.id}>
                  {isMatch ? '★ ' : ''}
                  {a.name}
                  {a.card_last_digits ? ` (•${a.card_last_digits})` : ''}
                </option>
              );
            })}
          </select>
        </div>
        )}
        <div className="stat-card stat-savings">
          <span className="stat-card-label">🏷️ Total savings</span>
          <span className="stat-card-value">{formatCents(savings, currency)}</span>
          <span className="stat-card-sub">all discounts</span>
        </div>
        <div className="stat-card stat-currency">
          <span className="stat-card-label">💱 Currency</span>
          <select
            className="stat-card-input"
            value={currency}
            aria-label="Bill currency"
            disabled={isBusy}
            onChange={(e) => updateHeader({ currency: e.target.value })}
          >
            {COMMON_CURRENCIES.map((c) => (
              <option key={c.code} value={c.code}>
                {c.code}
              </option>
            ))}
            {/* Unlisted code from an old bill stays selectable. */}
            {!COMMON_CURRENCIES.some((c) => c.code === currency) && (
              <option value={currency}>{currency}</option>
            )}
          </select>
          <span className="stat-card-sub">as printed on the receipt</span>
        </div>
      </div>

      {mismatch && (
        <div className="bill-warning">
          ⚠️ The calculated total ({formatCents(computedTotal, currency)}) does not
          match the amount printed on the receipt ({formatCents(printed, currency)}).
          Check the article lines or the VAT below.
        </div>
      )}

      <input
        className="search-input"
        type="search"
        placeholder="Search articles…"
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        aria-label="Filter articles"
      />

      {busy === 'extract' ? (
        <Spinner label="Re-reading the receipt…" />
      ) : draft.items.length === 0 ? (
        <EmptyState message="No articles were extracted from this receipt — correct the VAT below and confirm." />
      ) : visibleItems.length === 0 ? (
        <EmptyState message={`No articles match “${search}”.`} />
      ) : (
        <div className="item-panels">
          {visibleItems.map((it) => (
            <details className="item-panel" key={it.id}>
              <summary>
                <span
                  className="item-icon"
                  title={
                    it.is_return
                      ? 'Deposit return (money back)'
                      : categories.find((c) => c.id === it.category_id)?.name ||
                        it.category_name ||
                        'Unclassified'
                  }
                >
                  {it.is_return ? '♻️' : itemIcon(it.category_id, categories)}
                </span>
                <span className="item-title">
                  <span className="item-name">{it.name}</span>
                  {it.is_return ? (
                    <span className="item-brand">♻️ deposit return</span>
                  ) : (
                    it.brand && <span className="item-brand">{it.brand}</span>
                  )}
                </span>
                <span className="item-price">{formatCents(it.line_total_cents, currency)}</span>
                <button
                  type="button"
                  className="item-remove"
                  aria-label={`Remove ${it.name}`}
                  title="Remove this article from the bill"
                  onClick={(e) => {
                    e.preventDefault(); // don't toggle the panel
                    e.stopPropagation();
                    removeItem(it.id);
                  }}
                >
                  ✕
                </button>
              </summary>
              <div className="item-detail">
                <div className="item-field">
                  <span>Article</span>
                  <input
                    className="cell-input cell-input-name"
                    defaultValue={it.name}
                    aria-label="Article name"
                    onBlur={(e) =>
                      e.target.value.trim() !== it.name &&
                      e.target.value.trim() !== '' &&
                      updateItem(it.id, { name: e.target.value.trim() })
                    }
                  />
                </div>
                <div className="item-field-grid">
                  <div className="item-field">
                    <span>Brand</span>
                    {customBrandItem === it.id ? (
                      <input
                        autoFocus
                        defaultValue={it.brand ?? ''}
                        placeholder="New brand…"
                        aria-label={`New brand for ${it.name}`}
                        onBlur={(e) => {
                          const value = e.target.value.trim();
                          updateItem(it.id, { brand: value || undefined });
                          setCustomBrandItem(null);
                        }}
                      />
                    ) : (
                      <select
                        value={it.brand ?? ''}
                        aria-label={`Brand for ${it.name}`}
                        onChange={(e) => {
                          if (e.target.value === '__custom__') {
                            setCustomBrandItem(it.id);
                          } else {
                            updateItem(it.id, { brand: e.target.value || undefined });
                          }
                        }}
                      >
                        <option value="">—</option>
                        {brandOptions.map((b) => (
                          <option key={b} value={b}>
                            {b}
                          </option>
                        ))}
                        {it.brand && !brandOptions.includes(it.brand) && (
                          <option value={it.brand}>{it.brand}</option>
                        )}
                        <option value="__custom__">+ Add brand…</option>
                      </select>
                    )}
                  </div>
                  <div className="item-field">
                    <span>Measure</span>
                    {(() => {
                      const current = normalizeUnit(it.unit);
                      const extra =
                        current && !MEASURE_OPTIONS.some((m) => m.value === current)
                          ? [{ value: current, label: current }]
                          : [];
                      return (
                        <select
                          value={current}
                          aria-label={`Measure for ${it.name}`}
                          onChange={(e) => updateItem(it.id, { unit: e.target.value })}
                        >
                          <option value="">—</option>
                          {[...MEASURE_OPTIONS, ...extra].map((m) => (
                            <option key={m.value} value={m.value}>
                              {m.label}
                            </option>
                          ))}
                        </select>
                      );
                    })()}
                  </div>
                  <div className="item-field">
                    <span>Qty</span>
                    <input
                      type="number"
                      min="0"
                      step="any"
                      defaultValue={it.quantity}
                      aria-label={`Quantity for ${it.name}`}
                      onBlur={(e) =>
                        Number(e.target.value) !== it.quantity &&
                        Number(e.target.value) > 0 &&
                        updateItem(it.id, { quantity: Number(e.target.value) })
                      }
                    />
                  </div>
                </div>
                <div className="item-field-grid">
                  <div className="item-field">
                    <span>Unit price</span>
                    <input
                      inputMode="decimal"
                      defaultValue={(it.unit_price_cents / 100).toFixed(2)}
                      aria-label={`Unit price for ${it.name}`}
                      onBlur={(e) => {
                        const cents = dollarsToCents(e.target.value);
                        // Deposit/refund lines may have negative prices.
                        const allowed =
                          Number.isFinite(cents) &&
                          (cents >= 0 || negativeAllowed(it)) &&
                          cents !== it.unit_price_cents;
                        if (allowed) {
                          updateItem(it.id, { unit_price_cents: cents });
                        } else {
                          e.target.value = (it.unit_price_cents / 100).toFixed(2);
                        }
                      }}
                    />
                  </div>
                  <div className="item-field">
                    <span>Discount</span>
                    <input
                      inputMode="decimal"
                      defaultValue={(it.discount_cents / 100).toFixed(2)}
                      aria-label={`Discount for ${it.name}`}
                      onBlur={(e) => {
                        const cents = dollarsToCents(e.target.value) || 0;
                        // Deposit/refund lines may carry negative discounts
                        // (e.g. a printed rebate refund).
                        if ((cents >= 0 || negativeAllowed(it)) && cents !== it.discount_cents) {
                          updateItem(it.id, { discount_cents: cents });
                        }
                      }}
                    />
                  </div>
                  <div className="item-field">
                    <span>Line total</span>
                    <strong className="item-line-total">
                      {formatCents(it.line_total_cents, currency)}
                    </strong>
                  </div>
                </div>
                <div className="item-field">
                  <span>Category</span>
                  <select
                    value={it.category_id ?? ''}
                    aria-label={`Category for ${it.name}`}
                    onChange={(e) =>
                      updateItem(it.id, {
                        category_id: e.target.value ? Number(e.target.value) : null,
                      })
                    }
                  >
                    <option value="">Unclassified</option>
                    {sections.map(([section, cats]) => (
                      <optgroup key={section} label={section}>
                        {cats.map((c) => (
                          <option key={c.id} value={c.id} title={c.description}>
                            {c.icon} {c.name}
                          </option>
                        ))}
                      </optgroup>
                    ))}
                  </select>
                </div>
                <div className="item-field">
                  <span>Budget</span>
                  <select
                    value={it.budget_id ?? ''}
                    aria-label={`Budget for ${it.name}`}
                    onChange={(e) =>
                      updateItem(it.id, {
                        budget_id: e.target.value ? Number(e.target.value) : null,
                      })
                    }
                  >
                    <option value="">
                      {draft.budget_id ? 'Bill budget (default)' : 'No budget'}
                    </option>
                    {budgets.map((b) => (
                      <option key={b.id} value={b.id}>
                        {budgetLabel(b, categories)} • {formatCents(b.amount_cents, baseCurrency.data?.currency ?? currency)}
                      </option>
                    ))}
                  </select>
                </div>
              </div>
            </details>
          ))}
        </div>
      )}

      <div className="bill-totals">
        <div className="bill-total-line">
          <span>Items total ({draft.items.length} articles)</span>
          <span>{formatCents(linesSum, currency)}</span>
        </div>
        <div className="item-field-grid">
          <div className="item-field">
            <span>Market discount</span>
            <input
              inputMode="decimal"
              defaultValue={(draft.discount_cents / 100).toFixed(2)}
              aria-label="Market discount"
              onBlur={(e) => {
                const cents = dollarsToCents(e.target.value) || 0;
                if (cents >= 0 && cents !== draft.discount_cents) {
                  updateHeader({ discount_cents: cents });
                }
              }}
            />
          </div>
          <div className="item-field">
            <span>Total VAT/IVA (already in prices)</span>
            <input
              inputMode="decimal"
              defaultValue={(draft.vat_cents / 100).toFixed(2)}
              aria-label="Total VAT/IVA"
              onBlur={(e) => {
                const cents = dollarsToCents(e.target.value) || 0;
                if (cents >= 0 && cents !== draft.vat_cents) {
                  updateHeader({ vat_cents: cents });
                }
              }}
            />
          </div>
        </div>
        <div className="bill-total-line bill-total-row">
          <span>Total paid (VAT included — recalculates on edit)</span>
          <strong>{formatCents(computedTotal, currency)}</strong>
        </div>
      </div>

      <div className="item-field-grid bill-meta-edit">
        <div className="item-field">
          <span>Date</span>
          <input
            type="date"
            defaultValue={draft.date || new Date().toISOString().slice(0, 10)}
            aria-label="Bill date"
            onBlur={(e) => e.target.value !== draft.date && updateHeader({ date: e.target.value })}
          />
        </div>
        <div className="item-field">
          <span>Payment</span>
          <select
            value={paymentLocked ? 'card' : draft.payment_method}
            disabled={paymentLocked}
            aria-label="Payment method"
            onChange={(e) => updateHeader({ payment_method: e.target.value })}
          >
            {paymentOptions.map((p) => (
              <option key={p || 'none'} value={p}>
                {paymentLocked && p === 'card' ? 'card (by card digits)' : p || '—'}
              </option>
            ))}
          </select>
        </div>
        <div className="item-field">
          <span>Card digits</span>
          <input
            defaultValue={draft.card_last_digits ?? ''}
            placeholder="—"
            inputMode="numeric"
            maxLength={4}
            aria-label="Card last digits"
            onBlur={(e) => {
              const digits = e.target.value.replace(/\D/g, '').slice(-4);
              if (digits !== (draft.card_last_digits ?? '')) {
                updateHeader({ card_last_digits: digits, payment_method: 'card' });
              }
            }}
          />
        </div>
        <div className="item-field">
          <span>Budget</span>
          <select
            value={draft.budget_id ?? ''}
            aria-label="Budget the bill counts toward"
            onChange={(e) =>
              updateHeader({ budget_id: e.target.value ? Number(e.target.value) : null })
            }
          >
            <option value="">No budget</option>
            {budgets.map((b) => (
              <option key={b.id} value={b.id}>
                {budgetLabel(b, categories)} • {formatCents(b.amount_cents, baseCurrency.data?.currency ?? currency)}
              </option>
            ))}
          </select>
        </div>
      </div>

      <p className="hint-text">
        {isSaved
          ? 'Edits apply on leaving a field — press “Save changes” to persist them.'
          : 'Edits apply on leaving a field — nothing is saved until you confirm.'}
      </p>

      {error && <ErrorMessage message={error} />}

      <div className="bill-actions">
        <Button onClick={confirm} disabled={isBusy}>
          {isSaved ? (busy === 'confirm' ? 'Saving…' : 'Save changes') : 'Confirm bill'}
        </Button>
        {!isSaved && (
          <>
            <Button variant="secondary" onClick={() => onRetry?.()} disabled={isBusy}>
              {busy === 'extract' ? 'Reading…' : 'Re-read'}
            </Button>
            <Button variant="danger" onClick={() => onDiscard?.()} disabled={isBusy}>
              Discard
            </Button>
          </>
        )}
      </div>
    </div>
  );
}