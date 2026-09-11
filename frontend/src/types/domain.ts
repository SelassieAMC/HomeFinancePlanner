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
  created_at: string;
  updated_at: string;
}

export interface TransactionInput {
  account_id: number;
  category_id: number | null;
  kind: TransactionKind;
  amount_cents: number;
  description: string;
  date: string;
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
  created_at: string;
}

export interface Budget {
  id: number;
  category_id: number;
  month: string; // YYYY-MM
  amount_cents: number;
  created_at: string;
  updated_at: string;
}

export interface BudgetInput {
  category_id: number;
  month: string;
  amount_cents: number;
}

export interface BudgetStatus extends Budget {
  spent_cents: number;
  remaining_cents: number;
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

export interface MonthSummary {
  month: string;
  currency: string; // base currency of all amounts
  conversion_warnings: string[]; // currencies shown 1:1 (no rate available)
  income_cents: number;
  expense_cents: number;
  net_cents: number;
  total_balance_cents: number;
  budgets: BudgetStatus[];
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
  store_id?: number | null; // store resolved from market_name (nullable)
  items?: BillItem[];
  created_at: string;
  updated_at: string;
}

export type AIProviderType =
  | 'ollama'
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