package domain

// Analytics DTOs for the dashboard charts. Every amount is integer cents in
// the base currency (converted by the service from native-currency rows).
// Pointer fields distinguish "no data yet" from zero so chart lines break
// instead of dropping to the axis.
//
// All six responses carry conversion_warnings using the same convention as
// Summary: currencies without an FX rate degrade to 1:1 and are reported.

// ItemPriceRow is a repository-facing intermediate row: one accepted,
// non-return bill line normalized to a price per base unit (kg/l/pcs) in its
// native currency. It feeds the store price index and the personal price
// index; conversion to base currency happens in the service.
type ItemPriceRow struct {
	StoreID      int64  // 0 when the bill has no store
	Date         string // YYYY-MM-DD
	Key          string // lower(trim(name))
	BaseUnit     string // kg | l | pcs
	Currency     string
	PricePerUnit float64 // cents per base unit, native currency
	CategoryID   int64   // 0 when uncategorized
	CategoryName string
	Section      string
}

// CategorySpendRow is one accepted-bill-item category aggregate in its native
// currency (sunburst input).
type CategorySpendRow struct {
	CategoryID int64
	Name       string
	Section    string
	Currency   string
	TotalCents int64
}

// DayExpenseRow is one month/day expense aggregate from transactions in its
// native currency (run-rate input).
type DayExpenseRow struct {
	Month       string // YYYY-MM
	Day         int    // 1..31
	Currency    string
	AmountCents int64
}

// SectionDayRow is one dow×section aggregate from accepted bill items in its
// native currency (heatmap input). Dow is Monday-first 0..6.
type SectionDayRow struct {
	Dow        int
	Section    string
	Currency   string
	TotalCents int64
}

// MonthFixedRow is one month×fixed aggregate from expense transactions in its
// native currency (fixed-split input). Unclassified marks transactions with
// no category at all (how bill transactions are created).
type MonthFixedRow struct {
	Month        string // YYYY-MM
	IsFixed      bool
	Unclassified bool
	Currency     string
	TotalCents   int64
}

// --- Chart 1: Store Price Efficiency Comparison -----------------------------

// StorePriceIndexRow is one store's average relative price index over the
// overlapping products it shares with other stores. 100 = overall average;
// lower is cheaper.
type StorePriceIndexRow struct {
	StoreID      int64   `json:"store_id"`
	StoreName    string  `json:"store_name"`
	Index        float64 `json:"index"`
	ProductCount int     `json:"product_count"` // overlapping products backing the index
}

type StorePriceIndex struct {
	From               string               `json:"from"`
	To                 string               `json:"to"`
	Currency           string               `json:"currency"`
	MinStores          int                  `json:"min_stores"` // overlap threshold a product must meet
	Rows               []StorePriceIndexRow `json:"rows"`       // cheapest first
	ConversionWarnings []string             `json:"conversion_warnings"`
}

// --- Chart 2: Micro-Category Spending Distribution (sunburst) ---------------

// SunburstLeaf is an outer-ring slice: one product category inside a section.
type SunburstLeaf struct {
	CategoryID int64   `json:"category_id"`
	Name       string  `json:"name"`
	TotalCents int64   `json:"total_cents"`
	Share      float64 `json:"share"` // 0..1 of the grand total
}

// SunburstSegment is an inner-ring slice: one category section with its
// children in draw order (largest first, small slices folded into "Other").
type SunburstSegment struct {
	Section    string         `json:"section"`
	TotalCents int64          `json:"total_cents"`
	Share      float64        `json:"share"`
	Children   []SunburstLeaf `json:"children"`
}

type CategorySunburst struct {
	From               string            `json:"from"`
	To                 string            `json:"to"`
	Currency           string            `json:"currency"`
	TotalCents         int64             `json:"total_cents"`
	Segments           []SunburstSegment `json:"segments"`
	ConversionWarnings []string          `json:"conversion_warnings"`
}

// --- Chart 3: Monthly Run Rate vs. Rolling Budget ---------------------------

// RunRatePoint is one day of the month. A nil field means "no data for this
// day" (future days, or a previous month that was shorter): the frontend
// renders these as line breaks, never as zero.
type RunRatePoint struct {
	Day             int    `json:"day"` // 1..31
	CurrentCents    *int64 `json:"current_cents"`
	PreviousCents   *int64 `json:"previous_cents"`
	AverageCents    *int64 `json:"average_cents"`     // 3-month average
	BudgetPaceCents *int64 `json:"budget_pace_cents"` // nil when there are no open budgets
}

type RunRate struct {
	Month              string         `json:"month"` // YYYY-MM
	Currency           string         `json:"currency"`
	DaysInMonth        int            `json:"days_in_month"`
	BudgetTotalCents   int64          `json:"budget_total_cents"` // SUM(open budgets), base currency
	Points             []RunRatePoint `json:"points"`
	ConversionWarnings []string       `json:"conversion_warnings"`
}

// --- Chart 4: Personal Consumer Price Index ---------------------------------

// PriceIndexPoint is one month of one staple series. Index is 100 at the
// series' own first month with data; nil index = no purchases that month.
type PriceIndexPoint struct {
	Month          string   `json:"month"` // YYYY-MM
	Index          *float64 `json:"index"`
	UnitPriceCents *float64 `json:"unit_price_cents"` // per base unit, base currency (tooltip)
}

type PriceIndexSeries struct {
	Label         string            `json:"label"` // item name
	BaseUnit      string            `json:"base_unit"`
	PurchaseCount int               `json:"purchase_count"`
	Points        []PriceIndexPoint `json:"points"`
}

type PriceIndex struct {
	From               string             `json:"from"`
	To                 string             `json:"to"`
	BaseMonth          string             `json:"base_month"` // earliest month with data
	Currency           string             `json:"currency"`
	Series             []PriceIndexSeries `json:"series"` // top staples by purchase frequency
	Average            []PriceIndexPoint  `json:"average"`
	ConversionWarnings []string           `json:"conversion_warnings"`
}

// --- Chart 5: Day-of-Week × Purchase-Type Heatmap ---------------------------

// HeatmapCell is one day-of-week cell; Cells is Monday-first, always 7
// entries (zero-filled).
type HeatmapCell struct {
	TotalCents int64 `json:"total_cents"`
}

type HeatmapRow struct {
	Group string        `json:"group"` // display name of the purchase-type group
	Cells []HeatmapCell `json:"cells"`
}

type SpendHeatmap struct {
	From               string       `json:"from"`
	To                 string       `json:"to"`
	Currency           string       `json:"currency"`
	Groups             []string     `json:"groups"` // row order for the frontend
	Rows               []HeatmapRow `json:"rows"`
	MaxCents           int64        `json:"max_cents"` // color-scale anchor
	ConversionWarnings []string     `json:"conversion_warnings"`
}

// --- Chart 6: Fixed vs. Discretionary Cash Flow Split ------------------------

type FixedSplitMonth struct {
	Month              string `json:"month"` // YYYY-MM
	FixedCents         int64  `json:"fixed_cents"`
	DiscretionaryCents int64  `json:"discretionary_cents"`
	TotalCents         int64  `json:"total_cents"`
}

type FixedSplit struct {
	From               string            `json:"from"`
	To                 string            `json:"to"`
	Currency           string            `json:"currency"`
	Months             []FixedSplitMonth `json:"months"`             // zero-filled, chronological
	UnclassifiedCents  int64             `json:"unclassified_cents"` // spend with no category (bill transactions)
	ConversionWarnings []string          `json:"conversion_warnings"`
}
