// Analytics chart payloads — mirrors backend/internal/domain/analytics.go.
// Money is integer cents in the user's base currency; pointer-ish fields are
// `null` on the wire and mean "no data" (render as a line break, not zero).

// --- Chart 1: Store Price Efficiency Comparison -----------------------------

export interface StorePriceIndexRow {
  store_id: number;
  store_name: string;
  /** 100 = overall average; lower = cheaper. */
  index: number;
  /** Overlapping products backing the index. */
  product_count: number;
}

export interface StorePriceIndex {
  from: string;
  to: string;
  currency: string;
  min_stores: number;
  rows: StorePriceIndexRow[];
  conversion_warnings: string[];
}

// --- Chart 2: Micro-Category Spending Distribution (sunburst) ---------------

export interface SunburstLeaf {
  category_id: number;
  name: string;
  total_cents: number;
  /** 0..1 of the grand total. */
  share: number;
}

export interface SunburstSegment {
  section: string;
  total_cents: number;
  share: number;
  children: SunburstLeaf[];
}

export interface CategorySunburst {
  from: string;
  to: string;
  currency: string;
  total_cents: number;
  segments: SunburstSegment[];
  conversion_warnings: string[];
}

// --- Chart 3: Monthly Run Rate vs. Rolling Budget ---------------------------

export interface RunRatePoint {
  day: number;
  current_cents: number | null;
  previous_cents: number | null;
  average_cents: number | null;
  budget_pace_cents: number | null;
}

export interface RunRate {
  month: string;
  currency: string;
  days_in_month: number;
  budget_total_cents: number;
  points: RunRatePoint[];
  conversion_warnings: string[];
}

// --- Chart 4: Personal Consumer Price Index ---------------------------------

export interface PriceIndexPoint {
  month: string;
  /** 100 at the series' own first month with data; null = no purchases. */
  index: number | null;
  unit_price_cents: number | null;
}

export interface PriceIndexSeries {
  label: string;
  base_unit: string;
  purchase_count: number;
  points: PriceIndexPoint[];
}

export interface PriceIndex {
  from: string;
  to: string;
  base_month: string;
  currency: string;
  series: PriceIndexSeries[];
  average: PriceIndexPoint[];
  conversion_warnings: string[];
}

// --- Chart 5: Day-of-Week × Purchase-Type Heatmap ---------------------------

export interface HeatmapCell {
  total_cents: number;
}

export interface HeatmapRow {
  group: string;
  /** Monday-first, always 7 entries. */
  cells: HeatmapCell[];
}

export interface SpendHeatmap {
  from: string;
  to: string;
  currency: string;
  groups: string[];
  rows: HeatmapRow[];
  max_cents: number;
  conversion_warnings: string[];
}

// --- Chart 6: Fixed vs. Discretionary Cash Flow Split -----------------------

export interface FixedSplitMonth {
  month: string;
  fixed_cents: number;
  discretionary_cents: number;
  total_cents: number;
}

export interface FixedSplit {
  from: string;
  to: string;
  currency: string;
  months: FixedSplitMonth[];
  /** Spend with no category at all (bill transactions) — footnote material. */
  unclassified_cents: number;
  conversion_warnings: string[];
}