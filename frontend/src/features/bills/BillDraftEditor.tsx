import { useEffect, useMemo, useRef, useState } from 'react';
import type { AccountType, BillConfirmInput, BillDraft, BillDraftItem, Budget, Category, Product, Store } from '../../types/domain';
import { formatCents, dollarsToCents, parseDecimalInput } from '../../lib/money';
import { COMMON_CURRENCIES } from '../../lib/currencies';
import { useAsync } from '../../hooks/useAsync';
import { settingsApi } from '../../api/settings';
import { productsApi } from '../../api/products';
import {
  Button,
  CategorySelect,
  ProductAutocomplete,
  Spinner,
  ErrorMessage,
  EmptyState,
  UnitSelect,
} from '../../components/ui';

// BillDraftEditor edits the scan draft client-side — nothing is persisted
// until Confirm. The layout is mobile-first: colored summary cards on top,
// one collapsible panel per article, and a computed total that recalculates
// on every price edit (with a warning when it no longer matches the receipt).

export type BillBusyAction = 'extract' | 'confirm' | 'discard' | 'cancel' | null;

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
  accounts?: { id: number; name: string; type: AccountType; card_last_digits?: string }[];
  /**
   * Account the bill is currently recorded on (saved mode). Preselects the
   * picker; null/undefined = wallet default. Used to edit a saved bill's
   * account.
   */
  initialAccountId?: number | null;
  /** Fixed storage taxonomy used for classification. */
  categories?: Category[];
  /** Brands already recorded on bill items — dropdown options. */
  brands?: string[];
  /** Open budget envelopes — correlation options (no month scoping). */
  budgets?: Budget[];
  /** Known stores for the market picker (omit to fall back to a plain input). */
  stores?: Store[];
}

/** Deposit/bottle return (e.g. "Leergut") — money back, negative amounts allowed. */
function isDepositReturn(name: string): boolean {
  return name.toLowerCase().includes('leergut');
}

/**
 * Payment metadata the account implies: the wallet is cash money, an account
 * with registered card digits pays by that card, and a credit account is a
 * card even without digits recorded. Anything else keeps the method read off
 * the receipt (transfer, voucher, …).
 */
function paymentForAccount(
  account: { type: AccountType; card_last_digits?: string } | undefined,
  draft: BillDraft,
): { payment_method: string; card_last_digits: string } {
  if (!account) {
    return { payment_method: draft.payment_method, card_last_digits: draft.card_last_digits ?? '' };
  }
  if (account.type === 'cash') return { payment_method: 'cash', card_last_digits: '' };
  if (account.card_last_digits) {
    return { payment_method: 'card', card_last_digits: account.card_last_digits };
  }
  if (account.type === 'credit') return { payment_method: 'card', card_last_digits: '' };
  return { payment_method: draft.payment_method, card_last_digits: '' };
}

export function buildConfirmInput(
  draft: BillDraft,
  accountId?: number,
  accounts?: { id: number; type: AccountType; card_last_digits?: string }[],
): BillConfirmInput {
  const account = accountId ? accounts?.find((a) => a.id === accountId) : undefined;
  return {
    market_name: draft.market_name,
    date: draft.date,
    ...paymentForAccount(account, draft),
    currency: draft.currency || 'USD',
    discount_cents: draft.discount_cents,
    vat_cents: draft.vat_cents,
    printed_total_cents: draft.printed_total_cents,
    budget_id: draft.budget_id ?? undefined,
    items: draft.items.map((it) => ({
      name: it.name,
      // Empty standardized name (old drafts, untouched lines) is the raw
      // text itself — the identity mapping, never learned as a change.
      standard_name: it.standard_name || it.name,
      // No identity fallback for the family: empty means "no broader
      // family known" and stays empty.
      generic_name: it.generic_name,
      brand: it.brand,
      unit: it.unit,
      unit_value: it.unit_value ?? undefined,
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

/**
 * Money input with a ± sign-flip button for fields where negatives are
 * legitimate (deposit/refund unit prices — money back). Phone decimal
 * keypads (inputMode="decimal") have no minus key, so those fields flip the
 * sign by tapping the toggle: it parses the field's current value, flips
 * the sign, commits and rewrites the uncontrolled input's DOM value to
 * match. Fields that are positive by nature (a discount is inherently a
 * subtraction of the main price) pass negative={false} and get only the
 * shared reject-revert semantics: an empty field commits 0 (clearing zeroes
 * the amount); other rejected input (unparseable text, negatives) reverts
 * the DOM value instead of leaving stale text behind.
 */
function SignedMoneyInput({
  valueCents,
  negative,
  ariaLabel,
  onCommit,
}: {
  valueCents: number;
  /** Negatives are legitimate on this field (deposit/refund lines). */
  negative: boolean;
  ariaLabel: string;
  onCommit: (cents: number) => void;
}) {
  const inputRef = useRef<HTMLInputElement>(null);

  function revert() {
    if (inputRef.current) inputRef.current.value = (valueCents / 100).toFixed(2);
  }

  function flip() {
    const input = inputRef.current;
    if (!input) return;
    const cents = dollarsToCents(input.value);
    if (!Number.isFinite(cents)) return;
    const flipped = -cents;
    input.value = (flipped / 100).toFixed(2);
    if (flipped !== valueCents) onCommit(flipped);
  }

  return (
    <div className="money-input-row">
      <input
        ref={inputRef}
        inputMode="decimal"
        defaultValue={(valueCents / 100).toFixed(2)}
        aria-label={ariaLabel}
        onBlur={(e) => {
          const raw = e.target.value.trim();
          const cents = raw === '' ? 0 : dollarsToCents(raw);
          if (!Number.isFinite(cents) || (cents < 0 && !negative)) {
            revert();
            return;
          }
          if (cents !== valueCents) onCommit(cents);
        }}
      />
      {negative && (
        <button
          type="button"
          className="sign-toggle"
          onClick={flip}
          aria-label={`Flip the sign of ${ariaLabel}`}
          title="Flip sign (phone keypads have no minus key)"
        >
          ±
        </button>
      )}
    </div>
  );
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
  initialAccountId = null,
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
  // '' = wallet default, '__new_card__' = create a card account, else id.
  // null until the accounts load: the picker preselects the card account
  // matching the receipt digits (fresh scan) or the bill's current account
  // (saved bill) once they arrive.
  const [accountChoice, setAccountChoice] = useState<string | null>(null);
  // Item id currently typing a brand-new brand ('__custom__' selected).
  const [customBrandItem, setCustomBrandItem] = useState<number | null>(null);
  // Item id of a just-added line — its panel renders open with the name
  // field focused, so the user can correct a missed article immediately.
  const [addedItemId, setAddedItemId] = useState<number | null>(null);
  // True while typing a market name that is not an existing store.
  const [customMarket, setCustomMarket] = useState(false);

  const query = search.trim().toLowerCase();
  const visibleItems = query
    ? draft.items.filter((it) =>
        `${it.name} ${it.brand ?? ''}`.toLowerCase().includes(query),
      )
    : draft.items;

  // Everything money-wise is computed live from the edited lines. VAT is
  // already included in each item's price, so it is never added again; the
  // bill-level discount is the receipt-wide rebate printed after the lines
  // (e.g. "10% Rabatt") and IS subtracted from the item sum.
  const linesSum = draft.items.reduce((sum, it) => sum + it.line_total_cents, 0);
  const computedTotal = linesSum - draft.discount_cents;
  const printed = draft.printed_total_cents;
  const mismatch = printed > 0 && printed !== computedTotal;
  const savings =
    draft.items.reduce((sum, it) => sum + it.discount_cents, 0) + draft.discount_cents;

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

  // Latest draft for async callbacks and synchronous multi-part edits: the
  // draft lives in the page's state, so the props closure goes stale between
  // two onChange calls in one event — picking a product fires onValueChange
  // (the name) and then onPick (the product info), and the second update must
  // build on the first, not on the pre-pick draft.
  const draftRef = useRef(draft);
  draftRef.current = draft;

  /** Commits the next draft and keeps draftRef ahead of the re-render. */
  function updateDraft(next: BillDraft) {
    draftRef.current = next;
    onChange(next);
  }

  function updateHeader(patch: Partial<BillDraft>) {
    updateDraft({ ...draftRef.current, ...patch });
  }

  // The default wallet account, matched like the backend's wallet convention:
  // a cash account named "Wallet" (case-insensitive). Seeded by migration, so
  // normally present; undefined just skips the client-side coupling.
  const walletAccount = accounts?.find(
    (a) => a.type === 'cash' && a.name.toLowerCase() === 'wallet',
  );

  // Preselect once the account options arrive: the saved bill's current
  // account, or (fresh scan) the card account matching the receipt's digits.
  useEffect(() => {
    if (accountChoice !== null || !accounts) return;
    let initial = '';
    if (isSaved) {
      initial = initialAccountId != null ? String(initialAccountId) : '';
    } else if (draft.card_last_digits) {
      initial = String(
        accounts.find((a) => a.card_last_digits === draft.card_last_digits)?.id ?? '',
      );
    }
    setAccountChoice(initial);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accounts, accountChoice]);

  /** Picks an account. */
  function handleAccountChange(value: string) {
    setAccountChoice(value);
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
    updateDraft({
      ...draftRef.current,
      items: draftRef.current.items.map((it) => {
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

  // Per item id, the article name a normalization lookup already ran for —
  // bill items live in the page-held draft, so the guard cannot ride on the
  // line object like TransactionForm's normalizedFor.
  const normalizedForRef = useRef(new Map<number, string>());

  /**
   * Picking a catalogue product fills the line's product info from it.
   * Prices, quantity and the discount stay as printed on the receipt. A
   * null pick (Enter with no suggestions) keeps the typed name — the
   * product is find-or-created on confirm.
   */
  function fillFromProduct(it: BillDraftItem, p: Product | null) {
    if (!p) return;
    updateItem(it.id, {
      // The article input shows the picked product's name, not the
      // partially typed text.
      name: p.name,
      // Identity fallback like everywhere else; an empty generic stays
      // empty ("no broader family known" is a real state).
      standard_name: p.standard_name || p.name,
      generic_name: p.generic_name || '',
      // Keep the line's existing value when the product has none.
      brand: p.brand || it.brand,
      unit: p.unit || it.unit,
      unit_value: p.unit_value ?? it.unit_value,
      category_id: p.category_id ?? it.category_id,
    });
  }

  /**
   * Name-mapping memory lookup when the article field is left: fills the
   * standardized/generic name (only when empty — the AI suggestion or a
   * user edit wins) and the category (only when unset) of a freely-typed
   * name, mirroring the manual-transaction form. Deposit/return lines are
   * never normalized and the typed name is never rewritten; failures are
   * best-effort and leave the fields as typed.
   */
  async function lookupNormalization(it: BillDraftItem) {
    const name = it.name.trim();
    if (!name || it.is_return || name.toLowerCase().includes('leergut')) return;
    if (normalizedForRef.current.get(it.id) === name) return;
    normalizedForRef.current.set(it.id, name);
    try {
      const res = await productsApi.normalizeName(name);
      if (!res.matched) return;
      // Apply only while the line still carries the looked-up name — it may
      // have been edited (or picked from the autocomplete) in the meantime.
      const current = draftRef.current.items.find((i) => i.id === it.id);
      if (!current || current.is_return || current.name.trim() !== name) return;
      updateItem(it.id, {
        standard_name: current.standard_name || res.standard_name,
        generic_name: current.generic_name || res.generic_name,
        category_id: current.category_id ?? (res.category_id ?? null),
      });
    } catch {
      /* Best-effort fill; failures just leave the fields as typed. */
    }
  }

  /** Drops a line from the draft (wrong extraction, duplicated, …). */
  function removeItem(id: number) {
    updateDraft({ ...draftRef.current, items: draftRef.current.items.filter((it) => it.id !== id) });
    if (addedItemId === id) setAddedItemId(null);
  }

  /**
   * Appends an empty line so the user can restore an article the extraction
   * missed or misread. Draft line ids are session-local, so the next free
   * number never collides with extracted lines (or saved-bill row ids).
   */
  function addItem() {
    const id = draft.items.reduce((max, it) => Math.max(max, it.id), 0) + 1;
    updateDraft({
      ...draftRef.current,
      items: [
        ...draftRef.current.items,
        {
          id,
          name: '',
          category_id: null,
          quantity: 1,
          unit_price_cents: 0,
          discount_cents: 0,
          line_total_cents: 0,
          budget_id: null,
        },
      ],
    });
    // Drop any active search so the new line is immediately visible.
    setSearch('');
    setAddedItemId(id);
  }

  function confirm() {
    if (accountChoice === '__new_card__') {
      onConfirm(undefined, true);
    } else if (accountChoice) {
      onConfirm(Number(accountChoice));
    } else if (isSaved) {
      // Saved edits always send an explicit account: an omitted account_id
      // would tell the backend to keep the current one, and the wallet is
      // the default choice here.
      onConfirm(walletAccount?.id);
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

  // The save/confirm actions render both above the articles (no scrolling
  // needed on a first look) and below the totals.
  const actions = (
    <>
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
    </>
  );

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
        <div className="stat-card stat-date">
          <span className="stat-card-label">📅 Date</span>
          <input
            type="date"
            className="stat-card-input"
            defaultValue={draft.date || new Date().toISOString().slice(0, 10)}
            aria-label="Bill date"
            disabled={isBusy}
            onBlur={(e) => e.target.value !== draft.date && updateHeader({ date: e.target.value })}
          />
        </div>
        {accounts && accountChoice !== null && (
        <div className="stat-card stat-account">
          <span className="stat-card-label">💳 Account</span>
          <select
            value={accountChoice}
            aria-label="Account to record the expense (wallet by default)"
            onChange={(e) => handleAccountChange(e.target.value)}
            disabled={isBusy}
          >
            <option value="">👛 Wallet (default)</option>
            {!isSaved && draft.card_last_digits && (
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
        <div className="stat-card stat-savings">
          <span className="stat-card-label">🏷️ Total savings</span>
          <span className="stat-card-value">{formatCents(savings, currency)}</span>
          <span className="stat-card-sub">all discounts</span>
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
        <div className="stat-card stat-budget">
          <span className="stat-card-label">🎯 Budget</span>
          <select
            className="stat-card-input"
            value={draft.budget_id ?? ''}
            aria-label="Budget the bill counts toward"
            disabled={isBusy}
            onChange={(e) =>
              updateHeader({ budget_id: e.target.value ? Number(e.target.value) : null })
            }
          >
            <option value="">No budget</option>
            {budgets.map((b) => (
              <option key={b.id} value={b.id}>
                {budgetLabel(b, categories)} •{' '}
                {formatCents(b.amount_cents, baseCurrency.data?.currency ?? currency)}
              </option>
            ))}
          </select>
        </div>
      </div>

      <div className="bill-actions">{actions}</div>

      {mismatch && (
        <div className="bill-warning">
          ⚠️ The calculated total ({formatCents(computedTotal, currency)}) does not
          match the amount printed on the receipt ({formatCents(printed, currency)}).
          Check the article lines, the market discount or the VAT below.
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

      <div className="bill-actions">
        <Button variant="secondary" onClick={addItem} disabled={isBusy}>
          ＋ Add article
        </Button>
      </div>

      {busy === 'extract' ? (
        <Spinner label="Re-reading the receipt…" />
      ) : draft.items.length === 0 ? (
        <EmptyState message="No articles were extracted from this receipt — add missing ones with “Add article”, correct the VAT below, or re-read." />
      ) : visibleItems.length === 0 ? (
        <EmptyState message={`No articles match “${search}”.`} />
      ) : (
        <div className="item-panels">
          {visibleItems.map((it) => (
            <details
              className="item-panel"
              key={it.id}
              open={addedItemId === it.id ? true : undefined}
            >
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
                  <span className="item-name">{it.name || 'New article'}</span>
                  {it.is_return ? (
                    <span className="item-brand">♻️ deposit return</span>
                  ) : (
                    <>
                      {it.brand && <span className="item-brand">{it.brand}</span>}
                      {it.standard_name &&
                        it.standard_name.toLowerCase() !== it.name.toLowerCase() && (
                          <span className="item-brand">↳ {it.standard_name}</span>
                        )}
                      {it.generic_name && <span className="item-brand">· {it.generic_name}</span>}
                    </>
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
                  <ProductAutocomplete
                    value={it.name}
                    onValueChange={(v) => updateItem(it.id, { name: v })}
                    onPick={(p) => fillFromProduct(it, p)}
                    onBlur={() => void lookupNormalization(it)}
                    currency={currency}
                    autoFocus={addedItemId === it.id}
                    ariaLabel="Article name"
                  />
                </div>
                {!it.is_return && (
                  <div className="item-field">
                    <span>
                      Standardized name{' '}
                      <span className="item-field-note">on receipt: {it.name || '—'}</span>
                    </span>
                    <input
                      key={`${it.id}:${it.standard_name || it.name}`}
                      defaultValue={it.standard_name || it.name}
                      placeholder="Human-readable name"
                      aria-label={`Standardized name for ${it.name}`}
                      onBlur={(e) => {
                        const value = e.target.value.trim();
                        if (value !== (it.standard_name || it.name)) {
                          updateItem(it.id, { standard_name: value });
                        }
                      }}
                    />
                  </div>
                )}
                {!it.is_return && (
                  <div className="item-field">
                    <span>Generic product</span>
                    <input
                      key={`${it.id}:${it.generic_name ?? ''}`}
                      defaultValue={it.generic_name || ''}
                      placeholder="e.g. Frozen Shaped Potatoes"
                      aria-label={`Generic product family for ${it.name}`}
                      onBlur={(e) => {
                        const value = e.target.value.trim();
                        if (value !== (it.generic_name || '')) {
                          updateItem(it.id, { generic_name: value });
                        }
                      }}
                    />
                  </div>
                )}
                <div className="item-field">
                  <span>Category</span>
                  <CategorySelect
                    categories={categories}
                    kind="product"
                    value={it.category_id ?? null}
                    ariaLabel={`Category for ${it.name}`}
                    emptyLabel="Unclassified"
                    onChange={(category_id) => updateItem(it.id, { category_id })}
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
                    <div className="unit-value-row">
                      <UnitSelect
                        value={it.unit}
                        ariaLabel={`Measure for ${it.name}`}
                        onChange={(unit) => updateItem(it.id, { unit })}
                      />
                      <input
                        type="text"
                        inputMode="decimal"
                        defaultValue={it.unit_value ?? ''}
                        placeholder="500"
                        aria-label={`Size value for ${it.name}`}
                        onBlur={(e) => {
                          // Decimal comma tolerated (mobile keypads type it);
                          // blank or non-positive clears the magnitude.
                          const v = parseDecimalInput(e.target.value);
                          const next = Number.isFinite(v) && v > 0 ? v : undefined;
                          if (next !== it.unit_value) {
                            updateItem(it.id, { unit_value: next });
                          }
                          if (next === undefined) {
                            e.target.value = ''; // keep the DOM in sync with the cleared state
                          }
                        }}
                      />
                    </div>
                  </div>
                  <div className="item-field">
                    <span>Qty</span>
                    <input
                      type="text"
                      inputMode="decimal"
                      defaultValue={it.quantity}
                      aria-label={`Quantity for ${it.name}`}
                      onBlur={(e) => {
                        // Decimal comma tolerated (mobile keypads type it);
                        // unparseable text reverts instead of sticking stale.
                        const v = parseDecimalInput(e.target.value);
                        if (!Number.isFinite(v) || v <= 0) {
                          e.target.value = String(it.quantity);
                          return;
                        }
                        if (v !== it.quantity) updateItem(it.id, { quantity: v });
                      }}
                    />
                  </div>
                </div>
                <div className="item-field-grid">
                  <div className="item-field">
                    <span>Unit price</span>
                    <SignedMoneyInput
                      valueCents={it.unit_price_cents}
                      negative={negativeAllowed(it)}
                      ariaLabel={`Unit price for ${it.name}`}
                      onCommit={(cents) => updateItem(it.id, { unit_price_cents: cents })}
                    />
                  </div>
                  <div className="item-field">
                    <span>Discount</span>
                    {/* Positive by nature — a discount is a subtraction of the
                        main price, so no ± (even on deposit lines). */}
                    <SignedMoneyInput
                      valueCents={it.discount_cents}
                      negative={false}
                      ariaLabel={`Discount for ${it.name}`}
                      onCommit={(cents) => updateItem(it.id, { discount_cents: cents })}
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
              aria-label="Market discount (receipt-wide, reduces the total)"
              onBlur={(e) => {
                const raw = e.target.value.trim();
                const cents = raw === '' ? 0 : dollarsToCents(raw);
                // The receipt-wide discount is always a positive reduction —
                // rejected input reverts the DOM value instead of leaving
                // stale text behind.
                if (!Number.isFinite(cents) || cents < 0) {
                  e.target.value = (draft.discount_cents / 100).toFixed(2);
                } else if (cents !== draft.discount_cents) {
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
                const raw = e.target.value.trim();
                const cents = raw === '' ? 0 : dollarsToCents(raw);
                if (!Number.isFinite(cents) || cents < 0) {
                  e.target.value = (draft.vat_cents / 100).toFixed(2);
                } else if (cents !== draft.vat_cents) {
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

      <p className="hint-text">
        {isSaved
          ? 'Edits apply on leaving a field — press “Save changes” to persist them.'
          : 'Edits apply on leaving a field — nothing is saved until you confirm.'}
      </p>

      {error && <ErrorMessage message={error} />}

      <div className="bill-actions">{actions}</div>
    </div>
  );
}
