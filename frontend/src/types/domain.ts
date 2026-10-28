// Domain types mirrored from the backend DTOs (snake_case on the wire).
// Keep field names exactly in sync with backend/internal/domain.

export type AccountType =
  | 'checking'
  | 'savings'
  | 'credit'
  | 'cash'
  | 'other';

export type TransactionKind = 'income' | 'expense' | 'transfer';

export interface Account {
  id: number;
  name: string;
  type: AccountType;
  currency: string;
  opening_balance_cents: number; // the pre-ledger seed money; the only editable money field
  balance_cents: number; // derived per read: opening + Σ transactions (income +, expense −, transfers both legs)
  card_last_digits?: string; // for bill matching (card •1234)
  created_at: string;
  updated_at: string;
}

export interface AccountInput {
  name: string;
  type: AccountType;
  currency: string;
  opening_balance_cents: number;
  card_last_digits?: string;
}

// The account register (MoneyWiz pillar): one transaction plus the balance as
// it stood right after it. change_cents is this row's own effect in the
// ACCOUNT's currency (foreign-currency rows fold their account_amount_cents,
// transfers their counter/fee legs); running_balance_cents folds opening +
// every row up to and including this one — the last row of a full ledger
// equals the account's derived balance.
export interface LedgerEntry extends Transaction {
  change_cents: number;
  running_balance_cents: number;
}

export interface AccountLedger {
  account: Account;
  entries: LedgerEntry[];
}

export interface Transaction {
  id: number;
  account_id: number;
  category_id: number | null;
  kind: TransactionKind;
  amount_cents: number;
  currency: string; // ISO 4217; account's currency, bill's for confirmations
  description: string;
  date: string; // YYYY-MM-DD
  // Set when this row was recorded for a scanned bill: the transaction is
  // then readonly — edits happen in the bills view.
  bill_id?: number | null;
  // Manual purchases: the market bought at (null for bill transactions).
  store_id?: number | null;
  store_name?: string; // display-only, joined from stores
  // Transfers only: destination account (account_id is the source).
  counter_account_id?: number | null;
  counter_account_name?: string; // display-only, joined from accounts
  // Transfers between different currencies: what the destination gains,
  // recorded from the FX snapshot at save time (null = same currency/1:1).
  counter_amount_cents?: number | null;
  counter_currency?: string;
  // Transfers only: the bank/intermediary fee in the SOURCE account's
  // currency (null = no fee).
  fee_cents?: number | null;
  // The rate actually applied when the row was converted (units of `to` per
  // 1 of `from`) — the public reference rate unless the user overrode it.
  fx_rate?: number | null;
  // Income/expense rows in a currency other than the owning account's (e.g. a
  // bill paid by a foreign-currency card): the amount in the ACCOUNT's
  // currency, converted at save time. Balance reads fold this, not the raw
  // amount. null = same currency (or no rate known → 1:1).
  account_amount_cents?: number | null;
  // Bag (allocation envelope) tagging. On bill-linked rows this mirrors the
  // bill-level tag for display only — the bag's spend truth is the bill lines.
  bag_id?: number | null;
  bag_name?: string; // display-only, joined from bags
  item_count?: number; // display-only correlated count
  items_total_cents?: number; // display-only Σ line totals
  items?: TransactionItem[]; // loaded by get only, never by list
  created_at: string;
  updated_at: string;
}

/** One article line of a manually entered transaction. */
export interface TransactionItem {
  id: number;
  transaction_id: number;
  product_id?: number | null;
  product_name?: string; // display-only join
  name: string;
  standard_name?: string; // display-only join: raw text → standardized name
  generic_name?: string; // display-only join: raw text → generic product family
  brand?: string;
  unit?: string;
  unit_value?: number | null; // printed size magnitude ("500" for "500ml"); null = unknown
  category_id?: number | null;
  quantity: number;
  unit_price_cents: number;
  discount_cents: number;
  line_total_cents: number;
}

export interface TransactionItemInput {
  name: string;
  brand?: string;
  unit?: string;
  unit_value?: number | null; // printed size magnitude (null = unknown)
  category_id?: number | null;
  quantity: number;
  unit_price_cents: number;
  discount_cents: number;
}

/** Public exchange-rate snapshot (Frankfurter/ECB, cached 24h server-side). */
export interface FxSnapshot {
  pivot: string;
  date: string; // reference date of the rate data
  fetched_at: string;
  rates: Record<string, number>; // units of the key currency per 1 pivot
}

export interface TransactionInput {
  account_id: number;
  category_id: number | null;
  kind: TransactionKind;
  amount_cents: number;
  description: string;
  date: string;
  store_name?: string; // manual purchases: market, find-or-created server-side
  counter_account_id?: number; // transfers only: destination account
  fee_cents?: number; // transfers only: bank fee in the source currency
  fx_rate?: number; // transfers only: user-overridden conversion rate
  bag_id?: number | null; // expenses only: tagged into one reserve
  items?: TransactionItemInput[];
}

/** A named savings target tracked against one account. */
export interface Goal {
  id: number;
  name: string;
  target_cents: number;
  currency: string; // the linked account's currency, decided at save time
  account_id: number;
  target_date?: string | null; // optional YYYY-MM-DD milestone
  created_at: string;
  updated_at: string;
}

/** A goal with its live progress attached (server-computed per read). */
export interface GoalStatus extends Goal {
  account_name: string;
  progress_cents: number; // the linked account's current balance
  achieved: boolean;
}

export interface GoalInput {
  name: string;
  target_cents: number;
  account_id: number;
  target_date?: string | null;
}

// --- Bags: allocation envelopes inside one account ---------------------------

export type BagLifecycle = 'open' | 'closed';

/** A named reserve of one account's balance — virtual partition, no money of
 *  its own. Balance is derived per read: Σ allocations − tagged spend. */
export interface Bag {
  id: number;
  name: string;
  account_id: number;
  target_cents?: number | null; // optional "should hold at least this much"
  status: BagLifecycle;
  closed_at?: string | null;
  created_at: string;
  updated_at: string;
}

/** One signed ledger row of a bag (never a transaction — partitions an
 *  existing balance, enters no aggregate). kind carries the sign for
 *  fund/withdraw; rebalance/adjust carry it directly. */
export interface BagAllocation {
  id: number;
  bag_id: number;
  kind: 'fund' | 'rebalance' | 'withdraw' | 'adjust';
  amount_cents: number;
  note?: string;
  date: string; // YYYY-MM-DD
  related_allocation_id?: number | null; // counterpart leg of a rebalance
  created_at: string;
  updated_at: string;
}

/** A bag with its computed balance attached (in the account's currency). */
export interface BagStatus extends Bag {
  account_name: string;
  tagged_spend_cents: number;
  balance_cents: number;
}

/** One account's bag section of GET /bags. */
export interface BagAccountView {
  account_id: number;
  account_name: string;
  currency: string;
  account_balance_cents: number;
  unbagged_cents: number;
  oversubscribed: boolean;
  bags: BagStatus[];
}

export interface BagOverview {
  conversion_warnings: string[];
  accounts: BagAccountView[];
}

export interface BagInput {
  name: string;
  account_id: number;
  target_cents?: number | null;
}

/** Manual ledger entry. fund/withdraw take the positive magnitude only (the
 *  kind carries the sign); adjust takes any non-zero amount. */
export interface BagAllocationInput {
  kind: 'fund' | 'withdraw' | 'adjust';
  amount_cents: number;
  note?: string;
  date: string;
}

export interface BagRebalanceInput {
  from_bag_id: number;
  to_bag_id: number;
  amount_cents: number;
  note?: string;
  date: string;
}

/** One open bag's year-end close preview row. */
export interface YearCloseBagRow {
  bag_id: number;
  bag_name: string;
  account_id: number;
  account_name: string;
  currency: string;
  balance_cents: number;
  target_cents: number;
  shortfall_cents: number;
}

export interface YearClosePreview {
  year: string;
  conversion_warnings: string[];
  bags: YearCloseBagRow[];
}

export interface BagRebalancePair {
  from: BagAllocation;
  to: BagAllocation;
}

// 'product' = bill item storage taxonomy; 'expense' = general budget grouping.
export type CategoryKind = 'product' | 'expense';

export interface Category {
  id: number;
  name: string;
  section?: string; // storage group (Fridge, Freezer, …) — seeded taxonomy
  icon?: string; // emoji representing the category
  description?: string;
  kind: CategoryKind;
  is_system: boolean; // seeded taxonomy row
  allows_negative?: boolean; // deposit/refund lines (Pfand, Leergut) may be negative
  is_fixed?: boolean; // recurring commitment (rent, utilities, insurance) vs discretionary
  created_at: string;
}

export type BudgetLifecycle = 'open' | 'closed';

/** 'open' = open-ended envelope; 'monthly' = fixed cap reset per calendar month. */
export type BudgetCadence = 'open' | 'monthly';

/**
 * A spending pool for one category. Envelopes (cadence 'open') have no
 * period: they stay open — accumulating attributed spend — until marked
 * finished and closed. Monthly budgets (cadence 'monthly') cover exactly the
 * calendar `month` they carry; a lazy rollover closes the stale open monthly
 * row and opens the current month's successor with the same amount. Monthly
 * rows with `carry_over` roll their leftover forward as a running carried
 * balance (deficits negative) — the successor's carried_cents inherits it.
 */
export interface Budget {
  id: number;
  category_id: number;
  amount_cents: number;
  status: BudgetLifecycle;
  cadence: BudgetCadence;
  month?: string; // YYYY-MM, monthly rows only
  carry_over?: boolean; // monthly series roll the leftover forward
  carried_cents?: number; // running carried balance (monthly rows)
  closed_at?: string; // set when closed, cleared on reopen
  created_at: string;
  updated_at: string;
}

export interface BudgetInput {
  category_id: number;
  amount_cents: number;
  cadence?: BudgetCadence; // empty = envelope
  carry_over?: boolean; // monthly series roll the leftover forward
}

export interface BudgetStatus extends Budget {
  spent_cents: number; // spend attributed inside the reported range
  // All spend attributed since creation; for monthly rows, bounded to the
  // row's own month.
  lifetime_spent_cents: number;
  // amount − lifetime spend; carrying monthly rows add the inherited
  // carried_cents (negative carry shrinks the available money).
  remaining_cents: number;
}

// One auditable envelope transfer (budget→budget): the source cap shrank,
// the target cap grew, the reallocation is recorded. from_budget_id null is
// a carried-balance SEED — the (signed) amount rode onto the target row's
// carried_cents ("a transfer from nothing"). Display-only category joins.
export interface BudgetTransfer {
  id: number;
  from_budget_id?: number | null;
  to_budget_id: number;
  month: string; // YYYY-MM, shared by both rows
  amount_cents: number;
  note?: string;
  created_at: string;
  from_category_id?: number;
  from_category_name?: string;
  to_category_id: number;
  to_category_name: string;
}

// --- Stores (recurring markets bills link to) --------------------------------

export interface Store {
  id: number;
  name: string;
  description?: string;
  location?: string;
  has_logo: boolean; // GET /stores/{id}/logo returns an image
  bill_count: number; // display-only, counted from bills
  created_at: string;
  updated_at: string;
}

export interface StoreInput {
  name: string;
  description?: string;
  location?: string;
}

// --- Products (catalogue of things ever bought, from bill items) -------------

export interface Product {
  id: number;
  name: string;
  standard_name?: string; // display-only join: raw text → standardized name
  generic_name?: string; // display-only join: raw text → generic product family
  brand?: string;
  unit?: string; // measure: kg, g, l, ml, pcs, …
  unit_value?: number | null; // printed size magnitude ("500" for "500ml"); null = unknown
  category_id?: number | null;
  category_name?: string;
  description?: string;
  has_image: boolean; // GET /products/{id}/photo returns an image
  times_bought: number; // display-only, counted from accepted bill lines
  last_purchase_date?: string; // YYYY-MM-DD of the most recent purchase
  latest_price_cents?: number; // native-currency amount of the latest purchase
  avg_price_cents?: number; // average over lines in the latest currency
  best_price_cents?: number; // lowest price ever paid, in the latest currency
  price_currency?: string; // currency latest/avg/best price are quoted in
  last_store_name?: string; // store of the most recent purchase (grouped listing only)
  created_at: string;
  updated_at: string;
}

/** One generic-product family of the grouped catalogue (GET /products/grouped):
 *  every product whose mapping shares a generic name, members embedded. A
 *  product with no family groups under its raw name. The price columns are
 *  family aggregates — the average/best of the members' own averages, quoted
 *  in the currency of the family's most recent purchase. */
export interface ProductGroup {
  generic_name: string;
  category_name?: string; // category of the most-bought member
  product_count: number;
  times_bought: number;
  last_purchase_date?: string; // YYYY-MM-DD of the family's most recent purchase
  avg_price_cents?: number;
  best_price_cents?: number;
  price_currency?: string;
  items: Product[];
}

/** Latest price of a product at one store (details modal breakdown). */
export interface ProductStorePrice {
  store_id?: number | null;
  store_name: string; // "—" when the bill had no store
  latest_price_cents?: number | null;
  /** Currency of latest_price_cents. The /prices endpoint is currency-scoped
   *  (constant); the merge check compares unscoped rows where the two products
   *  may have been priced in different currencies. */
  currency?: string;
  last_purchase_date?: string;
}

export interface ProductInput {
  name: string;
  brand?: string;
  unit?: string;
  /** Printed size magnitude: omitted = untouched, ≤ 0 = cleared. */
  unit_value?: number;
  category_id?: number | null;
  description?: string;
  /** Normalization-mapping decision for the raw name; omitted = untouched. */
  standard_name?: string;
  /** Generic product-family decision; omitted = untouched, '' = cleared. */
  generic_name?: string;
}

/** Pre-save rename check: the matched product and the merge plan to confirm.
 *  `match` is absent when the new name matches nothing (or the product itself). */
export interface ProductMergeCheck {
  match?: Product;
  mergeable: boolean;
  /** "different_stores" — same product bought in different stores (simple
   *  confirm); "same_store" — both bought at a shared store, any price (the
   *  user picks which record to keep). */
  reason?: 'different_stores' | 'same_store';
  /** Shared store the comparison is taken at (same_store only). */
  store_name?: string;
  source_store_price?: ProductStorePrice;
  target_store_price?: ProductStorePrice;
  /** Store names each product was bought at (different_stores dialog). */
  source_stores?: string[];
  target_stores?: string[];
}

/** Merge confirmation: fold the product into `merge_with`, keeping "source"
 *  (the edited product; `product` carries the pending edit) or "target" (the
 *  matched product; the typed edits are discarded). */
export interface ProductMergeInput {
  merge_with: number;
  keep: 'source' | 'target';
  product?: ProductInput;
}

export type ProductSort =
  | 'name'
  | 'updated_at'
  | 'created_at'
  | 'times_bought'
  | 'last_purchase'
  | 'best_price'
  | 'avg_price'
  | 'latest_price';

export interface ProductFilters {
  name?: string;
  category_id?: number;
  sort?: ProductSort;
  order?: 'asc' | 'desc';
  limit?: number;
  offset?: number;
}

export interface Paged<T> {
  items: T[];
  total: number;
  limit: number;
  offset: number;
}

export interface Summary {
  from: string; // YYYY-MM-DD, inclusive
  to: string; // YYYY-MM-DD, inclusive
  currency: string; // base currency of all amounts
  conversion_warnings: string[]; // currencies shown 1:1 (no rate available)
  income_cents: number;
  expense_cents: number;
  net_cents: number;
  total_balance_cents: number;
  budgets: BudgetStatus[]; // open envelopes plus closed ones with in-range spend
  top_categories: CategoryTotal[];
  daily_expenses: DayTotal[];
}

export interface DayTotal {
  date: string; // YYYY-MM-DD
  expense_cents: number;
}

/**
 * Period-independent "at a glance" row: current standing regardless of the
 * dashboard period navigation. Cash flow covers the current calendar month;
 * burn_rate_cents is the average daily expense over the trailing 30 days.
 * pending_receipts is the scan review backlog — money already spent (not
 * unpaid obligations).
 */
export interface Glance {
  currency: string; // base currency of all amounts
  conversion_warnings: string[];
  month: string; // YYYY-MM the cash-flow window covers
  income_cents: number;
  expense_cents: number;
  net_cents: number; // income minus expense for month
  net_worth_cents: number;
  burn_rate_cents: number;
  pending_receipts: number;
  pending_receipt_cents: number;
}

export interface CategoryTotal {
  category_id: number;
  category_name: string;
  total_cents: number;
}

// --- Bills (scan feature) ---------------------------------------------------

export type BillStatus = 'pending' | 'draft' | 'accepted' | 'discarded';

export interface BillItem {
  id: number;
  bill_id: number;
  name: string;
  standard_name?: string; // display-only join: raw text → standardized name
  generic_name?: string; // display-only join: raw text → generic product family
  brand?: string; // optional, editable in the draft
  unit?: string; // measure: kg, g, l, ml, pcs, …
  unit_value?: number | null; // printed size magnitude; null = unknown
  category_id?: number | null;
  category_name?: string;
  quantity: number;
  unit_price_cents: number; // may be negative for deposit returns
  discount_cents: number;
  line_total_cents: number; // may be negative for deposit returns
  is_return: boolean; // deposit/bottle return (e.g. "Leergut")
  budget_id?: number | null; // per-line budget override (null = bill's budget)
  bag_id?: number | null; // per-line bag override (null = bill's bag)
  product_id?: number | null; // catalogue product resolved from the name (null for returns)
}

export interface Bill {
  id: number;
  market_name: string;
  date: string; // YYYY-MM-DD
  payment_method: string;
  card_last_digits?: string;
  currency: string;
  items_subtotal_cents: number;
  discount_cents: number; // receipt-wide discount, subtracted from the item sum in the total
  vat_cents: number;
  total_cents: number; // computed: sum of lines − global discount (VAT included in prices)
  printed_total_cents: number; // as printed on the receipt (warning when ≠ total_cents)
  status: BillStatus;
  extracted_by: string;
  budget_id?: number | null; // budget the bill counts toward
  budget_name?: string; // display-only, joined from budgets
  bag_id?: number | null; // bag the bill is tagged into (per-line overrides live on items)
  transaction_id?: number | null; // expense transaction recorded at confirm
  account_id?: number | null; // account of that transaction (display-only, joined)
  account_name?: string; // display-only, joined from accounts
  store_id?: number | null; // store resolved from market_name (nullable)
  file_count?: number; // receipt parts stored for this bill (omitted for single-file bills)
  items?: BillItem[];
  created_at: string;
  updated_at: string;
}

export type AIProviderType =
  | 'ollama'
  | 'ollama_web_search'
  | 'openai'
  | 'gemini'
  | 'anthropic'
  | 'openai_compatible';

export interface AIProvider {
  id: string;
  type: AIProviderType;
  base_url?: string;
  api_key?: string; // masked (••••abcd) in responses
  model: string;
  /** The connector used by default for bill reads (any configured one when unset). */
  default_for_bills?: boolean;
  /** The connector used by default for web (offer) searches. */
  default_for_search?: boolean;
}

export interface AIPrompt {
  id: number;
  /** Stable lookup key the processes resolve by (immutable after create). */
  key: string;
  name: string;
  description: string;
  /** Empty content means "use the built-in default" at resolve time. */
  content: string;
  created_at: string;
  updated_at: string;
}

/** A prompt plus the extras the management UI needs. */
export interface AIPromptView extends AIPrompt {
  /** Raw built-in template ({{categories}} NOT expanded) — the reset target. */
  default_content: string;
  /** True when the row resolves to the built-in default (content empty). */
  uses_default: boolean;
}

export interface AIPromptInput {
  name: string;
  description?: string;
  content?: string;
}

export interface AIPromptCreateInput extends AIPromptInput {
  key: string;
}

// --- Scan draft (in memory until confirmed) ---------------------------------

export interface BillDraftItem {
  id: number; // session-local line number (1..n)
  name: string;
  standard_name?: string; // AI/memory suggestion, editable (learned at confirm)
  generic_name?: string; // AI/memory family suggestion, editable (learned at confirm)
  brand?: string;
  unit?: string; // measure: kg, g, l, ml, pcs, …
  unit_value?: number | null; // AI-suggested (or user-corrected) size magnitude; null = unknown
  category_name?: string;
  category_id?: number | null;
  quantity: number;
  unit_price_cents: number; // may be negative for deposit returns
  discount_cents: number;
  line_total_cents: number; // may be negative for deposit returns
  is_return?: boolean; // deposit/bottle return (e.g. "Leergut")
  budget_id?: number | null; // per-line budget override (null = bill's budget)
  bag_id?: number | null; // per-line bag override (null = bill's bag)
}

export interface BillDraft {
  market_name: string;
  date: string; // YYYY-MM-DD ("" when unreadable)
  payment_method: string;
  card_last_digits?: string;
  currency?: string;
  items: BillDraftItem[];
  items_subtotal_cents: number; // sum of the line totals
  discount_cents: number; // receipt-wide discount, subtracted from the item sum in the total
  vat_cents: number;
  total_cents: number; // computed: sum of lines − global discount (VAT included in prices)
  printed_total_cents: number; // as printed on the receipt
  budget_id?: number | null; // budget the whole bill counts toward
  bag_id?: number | null; // bag the whole bill is tagged into
}

/** Pipeline state of one uploaded receipt (analysis runs in the background).
 *  "cancelled" = the user aborted an in-progress analysis (its photos are kept
 *  until the request is deleted or swept). */
export type BillScanStatus = 'analyzing' | 'done' | 'failed' | 'cancelled';

export interface BillScan {
  scan_token: string;
  status: BillScanStatus;
  provider_id?: string;
  draft?: BillDraft; // absent while analyzing, failed or cancelled
  error?: string; // set when failed
  file_count?: number; // receipt parts in this upload (omitted for single-file scans)
  created_at?: string;
}

// --- Offer search (purchase cart) -------------------------------------------

/** Pipeline state of one persisted offer search (runs in the background). */
export type OfferSearchStatus = 'searching' | 'done' | 'failed';

/** One cart line as sent to the model — a snapshot of the product at search
 *  time, so the result stays renderable after product edits/merges. */
export interface OfferSearchProduct {
  product_id: number;
  name: string;
  brand?: string;
  unit?: string;
  quantity: number;
  last_price_cents?: number; // calibration hint for the model
  currency?: string;
}

export type OfferAvailability = 'available' | 'not_available' | 'not_published';

/** One price found for one product in one market — or an explicit "no price
 *  here" row (availability) when the search was scoped to pinned stores.
 *  best/worst are computed backend-side (cheapest/most expensive of the
 *  product within one currency). */
export interface OfferRow {
  market: string;
  brand?: string;
  variety?: string; // name/variety actually sold, e.g. "Hass"
  price_cents: number;
  currency: string;
  is_offer: boolean; // true = explicit promotion
  availability?: OfferAvailability; // omitted = available
  note?: string;
  best_price?: boolean;
  worst_price?: boolean;
}

export interface OfferProductResult {
  product_id: number;
  name: string;
  brand?: string; // requested brand, echoed
  offers: OfferRow[];
  note?: string; // e.g. "nothing found for this product"
}

export interface OfferResult {
  products: OfferProductResult[];
  searched_at: string; // RFC3339
}

export interface OfferSearch {
  search_token: string;
  status: OfferSearchStatus;
  provider_id?: string;
  products?: OfferSearchProduct[]; // request snapshot echoed back
  stores?: string[]; // pinned markets; absent = any local market
  name_match?: 'strict' | 'loose'; // absent = strict
  result?: OfferResult; // absent while searching or failed
  error?: string; // set when failed
  created_at?: string;
}

export interface BillConfirmInput {
  market_name: string;
  date: string;
  payment_method: string;
  card_last_digits: string;
  currency: string;
  discount_cents: number; // receipt-wide discount, subtracted from the item sum in the total
  vat_cents: number;
  printed_total_cents: number; // carried through for the mismatch warning
  budget_id?: number; // optional budget this bill counts toward
  bag_id?: number | null; // optional bag the whole bill is tagged into
  items: {
    name: string;
    standard_name?: string; // per-line override (empty = raw name)
    generic_name?: string; // per-line family override (empty = no family)
    brand?: string;
    unit?: string;
    unit_value?: number | null; // printed size magnitude (null = unknown)
    category_id?: number | null;
    quantity: number;
    unit_price_cents: number;
    discount_cents: number;
    // the reviewed line total — confirmed as-is (backend recomputes only for
    // payloads that omit it, so old clients keep working)
    line_total_cents?: number;
    budget_id?: number | null; // per-line override (omitted = bill's budget)
    bag_id?: number | null; // per-line override (omitted = bill's bag)
  }[];
  account_id?: number;
  // The user's rate (bill currency → paying account currency) when the bank
  // doesn't follow the public reference. Omitted = convert from the snapshot;
  // refused when both currencies are the same.
  fx_rate?: number;
}

// --- Product name normalization (canonical name mapping memory) ------------

/** GET /products/normalize — memory lookup for one raw text (no AI call). */
export interface ProductNormalizeResult {
  raw_name: string;
  standard_name?: string;
  generic_name?: string;
  category_id?: number | null;
  category_name?: string;
  source?: 'ai' | 'user' | 'manual';
  /** False when the raw text has no mapping yet. */
  matched: boolean;
}

/** Single-row status of the "analyze existing products" backfill job. */
export interface NormalizationJob {
  status: 'idle' | 'running' | 'done' | 'failed';
  total_names: number;
  processed_names: number;
  mapped_names: number;
  error?: string;
  created_at: string;
  updated_at: string;
}
// --- Product insights (deferred price-per-unit intelligence) ----------------

/** What the analysis detected. shrinkflation = smaller pack at a flat price,
 *  bulk_buy = a bigger pack of the same family is cheaper per unit,
 *  price_creep = the price per unit keeps rising purchase after purchase. */
export type ProductInsightKind = 'shrinkflation' | 'bulk_buy' | 'price_creep' | 'category_spike';

/** One purchase snapshot inside the insight's facts. */
export interface ProductInsightPurchase {
  date: string; // YYYY-MM-DD
  store_name?: string;
  unit_price_cents: number;
  unit_value: number; // printed magnitude ("500" for 500ml)
  unit: string; // printed unit text
  ppu_cents_per_unit: number; // cents per canonical unit (kg/l/pcs)
}

/** The deterministic finding the nudge was phrased from (facts snapshot). */
export interface ProductInsightData {
  unit: string; // canonical comparison unit (kg / l / pcs)
  threshold_pct: number; // the change that triggered the finding
  purchases?: ProductInsightPurchase[];
  old_price_cents?: number;
  new_price_cents?: number;
  old_unit_value?: number;
  new_unit_value?: number;
  old_ppu?: number; // cents per canonical unit
  new_ppu?: number;
  change_pct: number;
  // Category-spike facts: the evaluated month vs the mean of the prior
  // months (money in the base currency, per prior month).
  month?: string; // YYYY-MM
  current_cents?: number;
  baseline_cents?: number;
  prior_months?: { month: string; cents: number }[];
}

/** One saved insight (GET /insights). Written by the background analysis job
 *  after an accepted purchase; source ai = LLM-phrased nudge, auto = the
 *  deterministic fallback wording (no connector configured or the call failed). */
export interface ProductInsight {
  id: number;
  kind: ProductInsightKind;
  product_id?: number | null;
  category_id?: number | null;
  product_name: string;
  category_name?: string;
  generic_name: string;
  currency: string;
  message: string;
  source: 'ai' | 'auto';
  data: ProductInsightData;
  acknowledged: boolean;
  created_at: string; // unix seconds
  updated_at: string;
}

// --- Scheduled actions (the unified scheduler) ------------------------------

/** What the scheduled action is. The split is the auto-apply boundary:
 *  virtual kinds (bag_funding, bag_rebalance) write only bag allocation rows —
 *  the system applies them itself on the due date; real kinds
 *  (account_transfer, transaction) and purchase are confirm-first — a
 *  reminder until one user click applies. */
export type ScheduleKind = 'bag_funding' | 'bag_rebalance' | 'account_transfer' | 'transaction' | 'purchase';

export type ScheduleRecurrence = 'once' | 'monthly' | 'yearly';

export type ScheduleRunStatus = 'pending' | 'applied' | 'dismissed' | 'skipped';

/** Kind-switched action details; which fields are meaningful is decided by
 *  the action's kind (validated backend-side). */
export interface SchedulePayload {
  bag_id?: number; // bag_funding: the bag to fund
  from_bag_id?: number; // bag_rebalance: source bag
  to_bag_id?: number; // bag_rebalance: destination bag
  account_id?: number; // account_transfer: source account; transaction: its account
  counter_account_id?: number; // account_transfer: destination account
  amount_cents?: number; // bag kinds + transfer/transaction kinds
  transaction_kind?: 'expense' | 'income'; // transaction kind only (never 'transfer')
  category_id?: number; // transaction kind, optional
  description?: string; // transaction kind; blank defaults to the schedule name
  estimated_cents?: number; // purchase kind, optional
  note?: string; // purchase kind
}

/** One plan row: what should move, how often, from when. */
export interface ScheduledAction {
  id: number;
  name: string;
  kind: ScheduleKind;
  payload: SchedulePayload;
  recurrence: ScheduleRecurrence;
  start_date: string; // YYYY-MM-DD
  day_of_month?: number; // 1..28, monthly/yearly only
  month_of_year?: number; // 1..12, yearly only
  archived: boolean;
  created_at: string;
  updated_at: string;
}

/** The full create/edit body (the payload mirrors SchedulePayload). */
export interface ScheduledActionInput {
  name: string;
  kind: ScheduleKind;
  payload: SchedulePayload;
  recurrence: ScheduleRecurrence;
  start_date: string;
  day_of_month?: number;
  month_of_year?: number;
}

/** One materialized occurrence with its action attached (a run alone is
 *  uninterpretable). result_* records what apply wrote: allocation (virtual
 *  auto-apply) or transaction (confirm-first apply). */
export interface ScheduleRunView {
  id: number;
  action_id: number;
  due_date: string; // YYYY-MM-DD
  status: ScheduleRunStatus;
  result_type?: 'allocation' | 'transaction';
  result_id?: number;
  created_at: string;
  updated_at: string;
  action_name: string;
  kind: ScheduleKind;
  schedule: SchedulePayload;
}

// --- Users & sessions -------------------------------------------------------
// One shared household dataset: a user is login identity + header avatar,
// not a data owner (backend/internal/domain/user.go).

/** The authenticated user (GET /api/v1/auth/me and login response). */
export interface User {
  id: number;
  username: string;
  full_name: string;
  has_photo: boolean;
  locale: string;
  is_admin: boolean;
  created_at: string;
  updated_at: string;
}

/** A public identity in the login dropdown (no roles, no password fields). */
export interface LoginUser {
  id: number;
  username: string;
  full_name: string;
  has_photo: boolean;
}

/** Admin user list row (full User shape). */
export interface UserInput {
  username: string;
  full_name: string;
  password: string;
  is_admin?: boolean;
}

/** Admin edit payload; empty password keeps the current one. */
export interface UserUpdateInput {
  username: string;
  full_name: string;
  is_admin: boolean;
  password?: string;
}
