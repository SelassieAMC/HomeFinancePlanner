package service

import (
	"context"
	"testing"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// fakeAnalyticsStore serves canned rows for each analytics query and records
// the last arguments it saw.
type fakeAnalyticsStore struct {
	prices        []domain.ItemPriceRow
	categorySpend []domain.CategorySpendRow
	daily         []domain.DayExpenseRow
	heatmap       []domain.SectionDayRow
	monthFixed    []domain.MonthFixedRow
	openBudgets   int64

	gotMonths []string
}

func (f *fakeAnalyticsStore) ItemPrices(_ context.Context, _, _ string) ([]domain.ItemPriceRow, error) {
	return f.prices, nil
}
func (f *fakeAnalyticsStore) CategorySpend(_ context.Context, _, _ string) ([]domain.CategorySpendRow, error) {
	return f.categorySpend, nil
}
func (f *fakeAnalyticsStore) DailyExpenseByMonth(_ context.Context, months []string) ([]domain.DayExpenseRow, error) {
	f.gotMonths = months
	return f.daily, nil
}
func (f *fakeAnalyticsStore) Heatmap(_ context.Context, _, _ string) ([]domain.SectionDayRow, error) {
	return f.heatmap, nil
}
func (f *fakeAnalyticsStore) SpendByMonthAndFixed(_ context.Context, months []string) ([]domain.MonthFixedRow, error) {
	f.gotMonths = months
	return f.monthFixed, nil
}
func (f *fakeAnalyticsStore) OpenBudgetTotal(_ context.Context) (int64, error) {
	return f.openBudgets, nil
}

// fakeStoreNames lists stores for index name resolution.
type fakeStoreNames struct{ stores []domain.Store }

func (f fakeStoreNames) List(context.Context) ([]domain.Store, error) { return f.stores, nil }

// newTestAnalyticsService wires the service against fakes with EUR base and
// an empty rate snapshot (all conversions 1:1 unless a snapshot is passed).
func newTestAnalyticsService(store *fakeAnalyticsStore, snap domain.RateSnapshot, stores []domain.Store) (*AnalyticsService, *fakeAnalyticsStore) {
	settings := NewSettingsService(&fakeSettingsStore{data: map[string]string{
		settingsKeyBaseCurrency: "EUR",
	}}, passthroughBox{}, nil)
	return &AnalyticsService{
		store:    store,
		settings: settings,
		rates:    stubRateSource{snap: snap},
		stores:   fakeStoreNames{stores: stores},
	}, store
}

// --- helpers under test ------------------------------------------------------

func TestMedian(t *testing.T) {
	cases := []struct {
		name string
		in   []float64
		want float64
	}{
		{"odd", []float64{3, 1, 2}, 2},
		{"even", []float64{4, 1, 3, 2}, 2.5},
		{"single", []float64{7}, 7},
		{"unsorted skews resist", []float64{100, 1, 2, 3, 2}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := median(tc.in); got != tc.want {
				t.Fatalf("median(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// --- Chart 1: store price index ----------------------------------------------

func TestStorePriceIndexOverlapAndIndex(t *testing.T) {
	// Milk (pcs) sold at both stores; eggs only at store 1 (must be excluded).
	store := &fakeAnalyticsStore{prices: []domain.ItemPriceRow{
		{StoreID: 1, Key: "milk", BaseUnit: "l", Currency: "EUR", PricePerUnit: 100},
		{StoreID: 2, Key: "milk", BaseUnit: "l", Currency: "EUR", PricePerUnit: 120},
		{StoreID: 1, Key: "eggs", BaseUnit: "pcs", Currency: "EUR", PricePerUnit: 300},
		// Flour at both stores; the repository normalizes g→kg, so the fake
		// rows arrive pre-normalized in the same base unit.
		{StoreID: 2, Key: "flour", BaseUnit: "kg", Currency: "EUR", PricePerUnit: 200},
		{StoreID: 1, Key: "flour", BaseUnit: "kg", Currency: "EUR", PricePerUnit: 200},
	}}
	svc, _ := newTestAnalyticsService(store, domain.RateSnapshot{}, []domain.Store{
		{ID: 1, Name: "Rewe"}, {ID: 2, Name: "Lidl"},
	})

	out, err := svc.StorePriceIndex(context.Background(), "2026-08-01", "2026-08-31")
	if err != nil {
		t.Fatalf("StorePriceIndex: %v", err)
	}
	if len(out.Rows) != 2 {
		t.Fatalf("want 2 store rows, got %d: %+v", len(out.Rows), out.Rows)
	}
	// Milk: Rewe 100 vs Lidl 120 → 90.9 / 109.1. Flour identical → 100 each.
	// Store index = mean of its product relatives: 95.5 / 104.5.
	if out.Rows[0].Index != 95.5 || out.Rows[1].Index != 104.5 {
		t.Fatalf("indexes = %v, %v; want 95.5, 104.5", out.Rows[0].Index, out.Rows[1].Index)
	}
	if out.Rows[0].StoreName != "Rewe" {
		t.Fatalf("cheapest store = %q, want Rewe", out.Rows[0].StoreName)
	}
	// Both stores indexed over milk+flour (flour g-price normalized to kg).
	for _, r := range out.Rows {
		if r.ProductCount != 2 {
			t.Fatalf("store %d product count = %d, want 2", r.StoreID, r.ProductCount)
		}
	}
}

func TestStorePriceIndexSkipsStorelessBills(t *testing.T) {
	store := &fakeAnalyticsStore{prices: []domain.ItemPriceRow{
		{StoreID: 0, Key: "milk", BaseUnit: "l", Currency: "EUR", PricePerUnit: 100},
		{StoreID: 1, Key: "milk", BaseUnit: "l", Currency: "EUR", PricePerUnit: 110},
	}}
	svc, _ := newTestAnalyticsService(store, domain.RateSnapshot{}, nil)

	out, err := svc.StorePriceIndex(context.Background(), "2026-08-01", "2026-08-31")
	if err != nil {
		t.Fatalf("StorePriceIndex: %v", err)
	}
	// Milk exists at only one attributed store → nothing comparable.
	if len(out.Rows) != 0 {
		t.Fatalf("want no rows, got %+v", out.Rows)
	}
}

func TestStorePriceIndexCurrencyConversion(t *testing.T) {
	// Store 2 prices in USD (rate 1.1 USD/EUR ⇒ ×1/1.1 to EUR).
	snap := domain.RateSnapshot{Pivot: "EUR", Rates: map[string]float64{"USD": 1.1}}
	store := &fakeAnalyticsStore{prices: []domain.ItemPriceRow{
		{StoreID: 1, Key: "milk", BaseUnit: "l", Currency: "EUR", PricePerUnit: 100},
		{StoreID: 2, Key: "milk", BaseUnit: "l", Currency: "USD", PricePerUnit: 110},
	}}
	svc, _ := newTestAnalyticsService(store, snap, nil)

	out, err := svc.StorePriceIndex(context.Background(), "2026-08-01", "2026-08-31")
	if err != nil {
		t.Fatalf("StorePriceIndex: %v", err)
	}
	if len(out.Rows) != 2 || out.Rows[0].Index != 100 || out.Rows[1].Index != 100 {
		t.Fatalf("converted prices should be identical, got %+v", out.Rows)
	}
}

// --- Chart 3: run rate ---------------------------------------------------------

func TestRunRateCumulativeAndBudgetPace(t *testing.T) {
	store := &fakeAnalyticsStore{
		daily: []domain.DayExpenseRow{
			{Month: "2026-08", Day: 1, Currency: "EUR", AmountCents: 1000},
			{Month: "2026-08", Day: 2, Currency: "EUR", AmountCents: 500},
			{Month: "2026-07", Day: 1, Currency: "EUR", AmountCents: 2000},
		},
		openBudgets: 3000,
	}
	svc, fake := newTestAnalyticsService(store, domain.RateSnapshot{}, nil)

	out, err := svc.RunRate(context.Background(), "2026-08")
	if err != nil {
		t.Fatalf("RunRate: %v", err)
	}
	if len(fake.gotMonths) != 4 {
		t.Fatalf("want 4 months queried, got %v", fake.gotMonths)
	}
	if out.DaysInMonth != 31 || out.BudgetTotalCents != 3000 {
		t.Fatalf("days=%d budget=%d; want 31, 3000", out.DaysInMonth, out.BudgetTotalCents)
	}
	// Cumulative: day 1 = 1000, day 2 = 1500, day 3 = 1500.
	if p := out.Points[0]; p.CurrentCents == nil || *p.CurrentCents != 1000 {
		t.Fatalf("day 1 current = %+v", p)
	}
	if p := out.Points[1]; p.CurrentCents == nil || *p.CurrentCents != 1500 {
		t.Fatalf("day 2 current = %+v", p)
	}
	if p := out.Points[2]; p.CurrentCents == nil || *p.CurrentCents != 1500 {
		t.Fatalf("day 3 must carry the running total, got %+v", p)
	}
	// Budget pace day 1 = round(3000/31) = 97.
	if p := out.Points[0]; p.BudgetPaceCents == nil || *p.BudgetPaceCents != 97 {
		t.Fatalf("day 1 pace = %+v, want 97", p)
	}
	// July had spend on day 1 → previous curve non-nil; August-only days stay
	// in the average as one-month average.
	if p := out.Points[0]; p.PreviousCents == nil || *p.PreviousCents != 2000 {
		t.Fatalf("day 1 previous = %+v", p)
	}
}

func TestRunRateNoBudgetsYieldsNilPace(t *testing.T) {
	store := &fakeAnalyticsStore{openBudgets: 0}
	svc, _ := newTestAnalyticsService(store, domain.RateSnapshot{}, nil)

	out, err := svc.RunRate(context.Background(), "2026-08")
	if err != nil {
		t.Fatalf("RunRate: %v", err)
	}
	for _, p := range out.Points {
		if p.BudgetPaceCents != nil {
			t.Fatalf("day %d pace = %v, want nil", p.Day, *p.BudgetPaceCents)
		}
	}
}

func TestRunRateValidatesMonth(t *testing.T) {
	svc, _ := newTestAnalyticsService(&fakeAnalyticsStore{}, domain.RateSnapshot{}, nil)
	if _, err := svc.RunRate(context.Background(), "2026-13"); err == nil {
		t.Fatal("expected validation error for month 2026-13")
	}
}

// --- Chart 4: personal price index ---------------------------------------------

func TestPriceIndexMediansAndBase100(t *testing.T) {
	store := &fakeAnalyticsStore{prices: []domain.ItemPriceRow{
		// Milk: two purchases in Jan (median 100), one in Feb (120), two in Mar.
		{Date: "2026-01-05", Key: "milk", BaseUnit: "l", Currency: "EUR", PricePerUnit: 100},
		{Date: "2026-01-20", Key: "milk", BaseUnit: "l", Currency: "EUR", PricePerUnit: 110},
		{Date: "2026-02-10", Key: "milk", BaseUnit: "l", Currency: "EUR", PricePerUnit: 120},
		{Date: "2026-03-02", Key: "milk", BaseUnit: "l", Currency: "EUR", PricePerUnit: 150},
		{Date: "2026-03-15", Key: "milk", BaseUnit: "l", Currency: "EUR", PricePerUnit: 130},
		// Bread: three months, constant price.
		{Date: "2026-01-06", Key: "bread", BaseUnit: "pcs", Currency: "EUR", PricePerUnit: 200},
		{Date: "2026-02-06", Key: "bread", BaseUnit: "pcs", Currency: "EUR", PricePerUnit: 200},
		{Date: "2026-03-06", Key: "bread", BaseUnit: "pcs", Currency: "EUR", PricePerUnit: 200},
		// One-off item: only one month → must be dropped.
		{Date: "2026-02-07", Key: "caviar", BaseUnit: "g", Currency: "EUR", PricePerUnit: 900},
	}}
	svc, _ := newTestAnalyticsService(store, domain.RateSnapshot{}, nil)

	out, err := svc.PriceIndex(context.Background(), "2026-01-01", "2026-03-31")
	if err != nil {
		t.Fatalf("PriceIndex: %v", err)
	}
	if len(out.Series) != 2 {
		t.Fatalf("want 2 series, got %d", len(out.Series))
	}
	if out.BaseMonth != "2026-01" {
		t.Fatalf("base month = %q, want 2026-01", out.BaseMonth)
	}
	// Series order: milk (4 purchases) before bread (3).
	if out.Series[0].Label != "milk" || out.Series[1].Label != "bread" {
		t.Fatalf("series order = %q, %q", out.Series[0].Label, out.Series[1].Label)
	}
	milk := out.Series[0]
	// Jan median = 105 → index 100; Feb median 120 → 114.3; Mar median 140 → 133.3.
	want := map[string]float64{"2026-01": 100, "2026-02": 114.3, "2026-03": 133.3}
	for _, p := range milk.Points {
		if p.Index == nil || *p.Index != want[p.Month] {
			t.Fatalf("milk %s index = %v, want %v", p.Month, p.Index, want[p.Month])
		}
	}
	// Bread stays flat at 100.
	for _, p := range out.Series[1].Points {
		if p.Index == nil || *p.Index != 100 {
			t.Fatalf("bread index = %v, want 100", p.Index)
		}
	}
	// Average: Jan = 100, Feb = (114.3+100)/2, Mar = (133.3+100)/2.
	if p := out.Average[1]; p.Index == nil || *p.Index != 107.2 {
		t.Fatalf("average Feb = %v, want 107.2", p.Index)
	}
}

func TestPriceIndexMissingFXWarns(t *testing.T) {
	store := &fakeAnalyticsStore{prices: []domain.ItemPriceRow{
		{Date: "2026-01-05", Key: "milk", BaseUnit: "l", Currency: "USD", PricePerUnit: 110},
		{Date: "2026-02-05", Key: "milk", BaseUnit: "l", Currency: "USD", PricePerUnit: 120},
		{Date: "2026-03-05", Key: "milk", BaseUnit: "l", Currency: "USD", PricePerUnit: 130},
	}}
	svc, _ := newTestAnalyticsService(store, domain.RateSnapshot{}, nil)

	out, err := svc.PriceIndex(context.Background(), "2026-01-01", "2026-03-31")
	if err != nil {
		t.Fatalf("PriceIndex: %v", err)
	}
	if len(out.ConversionWarnings) != 1 || out.ConversionWarnings[0] != "USD" {
		t.Fatalf("warnings = %v, want [USD]", out.ConversionWarnings)
	}
}

// --- Chart 5: heatmap -----------------------------------------------------------

func TestSpendHeatmapZeroFilledAndGrouped(t *testing.T) {
	store := &fakeAnalyticsStore{heatmap: []domain.SectionDayRow{
		// Monday (0), grocery section.
		{Dow: 0, Section: "Fridge", Currency: "EUR", TotalCents: 1000},
		// Sunday (6), non-food essentials.
		{Dow: 6, Section: "Paper Goods", Currency: "EUR", TotalCents: 500},
		// Tuesday, unknown section → Other.
		{Dow: 1, Section: "", Currency: "EUR", TotalCents: 250},
	}}
	svc, _ := newTestAnalyticsService(store, domain.RateSnapshot{}, nil)

	out, err := svc.SpendHeatmap(context.Background(), "2026-08-01", "2026-08-31")
	if err != nil {
		t.Fatalf("SpendHeatmap: %v", err)
	}
	if len(out.Rows) != 3 || len(out.Groups) != 3 {
		t.Fatalf("want 3 rows/groups, got %d/%d", len(out.Rows), len(out.Groups))
	}
	if out.Groups[0] != "Groceries" || out.Groups[1] != "Household & Essentials" || out.Groups[2] != "Other" {
		t.Fatalf("group order = %v", out.Groups)
	}
	groceries := out.Rows[0]
	if got := groceryCell(t, out, "Groceries", 0); got != 1000 {
		t.Fatalf("Mon groceries = %d, want 1000", got)
	}
	_ = groceries
	if got := groceryCell(t, out, "Household & Essentials", 6); got != 500 {
		t.Fatalf("Sun essentials = %d, want 500", got)
	}
	if got := groceryCell(t, out, "Other", 1); got != 250 {
		t.Fatalf("Tue other = %d, want 250", got)
	}
	if out.MaxCents != 1000 {
		t.Fatalf("max = %d, want 1000", out.MaxCents)
	}
	// Zero-filled cells stay zero.
	if got := groceryCell(t, out, "Groceries", 3); got != 0 {
		t.Fatalf("Thu groceries = %d, want 0", got)
	}
}

// groceryCell fetches one cell by group name and dow index.
func groceryCell(t *testing.T, out domain.SpendHeatmap, group string, dow int) int64 {
	t.Helper()
	for _, row := range out.Rows {
		if row.Group == group {
			return row.Cells[dow].TotalCents
		}
	}
	t.Fatalf("group %q not found", group)
	return 0
}

// --- Chart 6: fixed vs discretionary --------------------------------------------

func TestFixedSplitBucketsAndZeroFill(t *testing.T) {
	store := &fakeAnalyticsStore{monthFixed: []domain.MonthFixedRow{
		{Month: "2026-08", IsFixed: true, Currency: "EUR", TotalCents: 900},
		{Month: "2026-08", Currency: "EUR", TotalCents: 400},
		{Month: "2026-08", Unclassified: true, Currency: "EUR", TotalCents: 100},
		{Month: "2026-07", IsFixed: true, Currency: "EUR", TotalCents: 800},
	}}
	svc, fake := newTestAnalyticsService(store, domain.RateSnapshot{}, nil)

	out, err := svc.FixedSplit(context.Background(), "2026-06-15", "2026-08-31")
	if err != nil {
		t.Fatalf("FixedSplit: %v", err)
	}
	if len(fake.gotMonths) != 3 {
		t.Fatalf("want 3 months queried (June, July, August), got %v", fake.gotMonths)
	}
	if len(out.Months) != 3 {
		t.Fatalf("want 3 zero-filled months, got %d", len(out.Months))
	}
	june := out.Months[0]
	if june.Month != "2026-06" || june.FixedCents != 0 || june.DiscretionaryCents != 0 {
		t.Fatalf("June must be zero-filled, got %+v", june)
	}
	aug := out.Months[2]
	if aug.FixedCents != 900 || aug.DiscretionaryCents != 500 || aug.TotalCents != 1400 {
		t.Fatalf("August split = %+v", aug)
	}
	if out.UnclassifiedCents != 100 {
		t.Fatalf("unclassified = %d, want 100", out.UnclassifiedCents)
	}
}

// --- Validation -----------------------------------------------------------------

func TestAnalyticsValidatesRanges(t *testing.T) {
	svc, _ := newTestAnalyticsService(&fakeAnalyticsStore{}, domain.RateSnapshot{}, nil)
	ctx := context.Background()

	if _, err := svc.StorePriceIndex(ctx, "2026-08-02", "2026-08-01"); err == nil {
		t.Fatal("expected inverted range error")
	}
	if _, err := svc.CategorySunburst(ctx, "not-a-date", "2026-08-01"); err == nil {
		t.Fatal("expected invalid from error")
	}
	if _, err := svc.PriceIndex(ctx, "2026-08-01", "2026-02-30"); err == nil {
		t.Fatal("expected impossible date error")
	}
	if _, err := svc.SpendHeatmap(ctx, "2026-08-01", ""); err == nil {
		t.Fatal("expected missing to error")
	}
	if _, err := svc.FixedSplit(ctx, "2026-08-01", "2026-07-01"); err == nil {
		t.Fatal("expected inverted range error")
	}
	if _, err := svc.RunRate(ctx, "abc"); err == nil {
		t.Fatal("expected invalid month error")
	}
}

// Ensure the time helpers behave (used by RunRate's month windows).
func TestMonthHelpers(t *testing.T) {
	if got := monthWindowList("2026-08", 4); got[0] != "2026-05" || got[3] != "2026-08" {
		t.Fatalf("monthWindowList = %v", got)
	}
	if got := daysInMonthOf("2026-02"); got != 28 {
		t.Fatalf("Feb 2026 days = %d", got)
	}
	if got := monthRangeList("2025-11-20", "2026-02-05"); len(got) != 4 || got[0] != "2025-11" || got[3] != "2026-02" {
		t.Fatalf("monthRangeList = %v", got)
	}
	_ = time.Now // keep the time import anchored if helpers change
}
