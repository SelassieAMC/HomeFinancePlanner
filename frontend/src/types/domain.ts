// Domain types mirrored from the backend DTOs (snake_case on the wire).
// Keep field names exactly in sync with backend/internal/domain.

export type AccountType =
  | 'checking'
  | 'savings'
  | 'credit'
  | 'cash'
  | 'other';

export type TransactionKind = 'income' | 'expense';

export interface Account {
  id: number;
  name: string;
  type: AccountType;
  currency: string;
  balance_cents: number;
  card_last_digits?: string; // for bill matching (card •1234)
  created_at: string;
  updated_at: string;
}

export interface AccountInput {
  name: string;
  type: AccountType;
  currency: string;
  balance_cents: number;
  card_last_digits?: string;
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
  brand?: string;
  unit?: string;
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
  category_id?: number | null;
  quantity: number;
  unit_price_cents: number;
  discount_cents: number;
}

export interface TransactionInput {
  account_id: number;
  category_id: number | null;
  kind: TransactionKind;
  amount_cents: number;
  description: string;
  date: string;
  store_name?: string; // manual purchases: market, find-or-created server-side
  items?: TransactionItemInput[];
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

/**
 * An open-ended spending envelope for one category: no period. It stays open
 * — accumulating attributed spend — until it is marked finished and closed,
 * so total spend, savings and overspend can be evaluated over its lifetime.
 */
export interface Budget {
  id: number;
  category_id: number;
  amount_cents: number;
  status: BudgetLifecycle;
  closed_at?: string; // set when closed, cleared on reopen
  created_at: string;
  updated_at: string;
}

export interface BudgetInput {
  category_id: number;
  amount_cents: number;
}

export interface BudgetStatus extends Budget {
  spent_cents: number; // spend attributed inside the reported range
  lifetime_spent_cents: number; // all spend attributed since creation
  remaining_cents: number; // amount − lifetime spend (negative = overspent)
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
  brand?: string;
  unit?: string; // measure: kg, g, l, ml, pcs, …
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
  created_at: string;
  updated_at: string;
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
  category_id?: number | null;
  description?: string;
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
  brand?: string; // optional, editable in the draft
  unit?: string; // measure: kg, g, l, ml, pcs, …
  category_id?: number | null;
  category_name?: string;
  quantity: number;
  unit_price_cents: number; // may be negative for deposit returns
  discount_cents: number;
  line_total_cents: number; // may be negative for deposit returns
  is_return: boolean; // deposit/bottle return (e.g. "Leergut")
  budget_id?: number | null; // per-line budget override (null = bill's budget)
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
  discount_cents: number;
  vat_cents: number;
  total_cents: number; // computed: sum of lines (VAT included in prices)
  printed_total_cents: number; // as printed on the receipt (warning when ≠ total_cents)
  status: BillStatus;
  extracted_by: string;
  budget_id?: number | null; // budget the bill counts toward
  budget_name?: string; // display-only, joined from budgets
  transaction_id?: number | null; // expense transaction recorded at confirm
  account_id?: number | null; // account of that transaction (display-only, joined)
  account_name?: string; // display-only, joined from accounts
  store_id?: number | null; // store resolved from market_name (nullable)
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
  /** The connector used when a scan does not pin one. */
  is_default: boolean;
}

export interface BillStatsRow {
  label: string;
  bill_count: number;
  quantity?: number;
  total_cents: number;
}

// Envelope for GET /bills/stats: rows merged across currencies and
// converted into the user's base currency.
export interface BillStatsResponse {
  currency: string;
  conversion_warnings: string[];
  rows: BillStatsRow[];
}

// --- Scan draft (in memory until confirmed) ---------------------------------

export interface BillDraftItem {
  id: number; // session-local line number (1..n)
  name: string;
  brand?: string;
  unit?: string; // measure: kg, g, l, ml, pcs, …
  category_name?: string;
  category_id?: number | null;
  quantity: number;
  unit_price_cents: number; // may be negative for deposit returns
  discount_cents: number;
  line_total_cents: number; // may be negative for deposit returns
  is_return?: boolean; // deposit/bottle return (e.g. "Leergut")
  budget_id?: number | null; // per-line budget override (null = bill's budget)
}

export interface BillDraft {
  market_name: string;
  date: string; // YYYY-MM-DD ("" when unreadable)
  payment_method: string;
  card_last_digits?: string;
  currency?: string;
  items: BillDraftItem[];
  items_subtotal_cents: number; // sum of the line totals
  discount_cents: number; // informational
  vat_cents: number;
  total_cents: number; // computed: sum of lines (VAT included in prices)
  printed_total_cents: number; // as printed on the receipt
  budget_id?: number | null; // budget the whole bill counts toward
}

/** Pipeline state of one uploaded receipt (analysis runs in the background). */
export type BillScanStatus = 'analyzing' | 'done' | 'failed';

export interface BillScan {
  scan_token: string;
  status: BillScanStatus;
  provider_id?: string;
  draft?: BillDraft; // absent while analyzing or failed
  error?: string; // set when failed
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

/** One price found for one product in one market. best/worst are computed
 *  backend-side (cheapest/most expensive of the product within one currency). */
export interface OfferRow {
  market: string;
  brand?: string;
  price_cents: number;
  currency: string;
  is_offer: boolean; // true = explicit promotion
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
  discount_cents: number;
  vat_cents: number;
  printed_total_cents: number; // carried through for the mismatch warning
  budget_id?: number; // optional budget this bill counts toward
  items: {
    name: string;
    brand?: string;
    unit?: string;
    category_id?: number | null;
    quantity: number;
    unit_price_cents: number;
    discount_cents: number;
    budget_id?: number | null; // per-line override (omitted = bill's budget)
  }[];
  account_id?: number;
}