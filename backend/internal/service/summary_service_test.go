package service

import (
	"context"
	"testing"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// fakeSummaryStore returns canned RawSummary/RawGlance and records the
// ranges it was queried with.
type fakeSummaryStore struct {
	raw       domain.RawSummary
	rawGlance domain.RawGlance
	from      string
	to        string

	monthFrom, monthTo string
	burnFrom, burnTo   string
}

func (f *fakeSummaryStore) RawRangeSummary(_ context.Context, from, to string) (domain.RawSummary, error) {
	f.from, f.to = from, to
	return f.raw, nil
}

func (f *fakeSummaryStore) RawGlance(_ context.Context, monthFrom, monthTo, burnFrom, burnTo string) (domain.RawGlance, error) {
	f.monthFrom, f.monthTo, f.burnFrom, f.burnTo = monthFrom, monthTo, burnFrom, burnTo
	return f.rawGlance, nil
}

// fakeCategoryNames lists categories for the top-spend name resolution.
type fakeCategoryNames struct{ cats []domain.Category }

func (f fakeCategoryNames) List(context.Context) ([]domain.Category, error) { return f.cats, nil }
func (f fakeCategoryNames) GetByID(_ context.Context, id int64) (domain.Category, error) {
	for _, c := range f.cats {
		if c.ID == id {
			return c, nil
		}
	}
	return domain.Category{}, domain.ErrNotFound
}
func (f fakeCategoryNames) Create(context.Context, domain.Category) (domain.Category, error) {
	return domain.Category{}, nil
}
func (f fakeCategoryNames) Delete(context.Context, int64) error { return nil }

// stubRateSource serves one snapshot.
type stubRateSource struct{ snap domain.RateSnapshot }

func (s stubRateSource) Snapshot(context.Context) (domain.RateSnapshot, error) {
	return s.snap, nil
}

func newTestSummaryService(raw domain.RawSummary, base string, snap domain.RateSnapshot, cats []domain.Category) (*SummaryService, *fakeSummaryStore) {
	store := &fakeSettingsStore{data: map[string]string{}}
	if base != "" {
		store.data[settingsKeyBaseCurrency] = base
	}
	fake := &fakeSummaryStore{raw: raw}
	return &SummaryService{
		summary:    fake,
		settings:   NewSettingsService(store, passthroughBox{}, nil),
		rates:      stubRateSource{snap: snap},
		categories: fakeCategoryNames{cats: cats},
	}, fake
}

func summaryTestRaw() domain.RawSummary {
	return domain.RawSummary{
		From: "2026-09-01",
		To:   "2026-09-30",
		Income: []domain.CurrencyAmount{
			{Currency: "EUR", Cents: 100_000},
			{Currency: "USD", Cents: 22_000}, // 220 USD ≈ 200 EUR at 1.1
		},
		Expense: []domain.CurrencyAmount{{Currency: "EUR", Cents: 50_000}},
		Balances: []domain.CurrencyAmount{
			{Currency: "EUR", Cents: 10_000},
			{Currency: "USD", Cents: 11_000}, // 100 EUR at 1.1
		},
		DailyExpenses: []domain.DayCurrencyTotal{
			{Date: "2026-09-02", Currency: "USD", ExpenseCents: 11_000}, // 100 EUR
			{Date: "2026-09-01", Currency: "EUR", ExpenseCents: 5_000},
		},
		CategorySpend: []domain.CategoryCurrencyTotal{
			{CategoryID: 1, Currency: "EUR", TotalCents: 30_000},
			{CategoryID: 1, Currency: "USD", TotalCents: 11_000}, // → 10_000 EUR, 40_000 total
			{CategoryID: 2, Currency: "EUR", TotalCents: 20_000},
		},
		BillBudgetSpend: []domain.BudgetCurrencySpend{
			{BudgetID: 10, Currency: "USD", Cents: 22_000}, // → 20_000 EUR
		},
		LifetimeCategorySpend: []domain.CategoryCurrencyTotal{
			{CategoryID: 1, Currency: "EUR", TotalCents: 30_000},
			{CategoryID: 1, Currency: "USD", TotalCents: 11_000}, // → 10_000 EUR
			{CategoryID: 2, Currency: "EUR", TotalCents: 20_000},
		},
		LifetimeBillBudgetSpend: []domain.BudgetCurrencySpend{
			{BudgetID: 10, Currency: "USD", Cents: 22_000}, // → 20_000 EUR
		},
		Budgets: []domain.Budget{
			{ID: 10, CategoryID: 1, AmountCents: 70_000, Status: domain.BudgetOpen},
		},
	}
}

func summaryTestSnapshot() domain.RateSnapshot {
	return domain.RateSnapshot{
		Pivot:     "EUR",
		Date:      "2026-09-10",
		FetchedAt: time.Now().UTC(),
		Rates:     map[string]float64{"USD": 1.1, "GBP": 0.85},
	}
}

func TestMonthSummaryConvertsIntoBaseCurrency(t *testing.T) {
	svc, _ := newTestSummaryService(summaryTestRaw(), "EUR", summaryTestSnapshot(), []domain.Category{
		{ID: 1, Name: "Groceries"},
		{ID: 2, Name: "Rent"},
	})

	s, err := svc.MonthSummary(context.Background(), "2026-09")
	if err != nil {
		t.Fatal(err)
	}
	if s.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", s.Currency)
	}
	if s.IncomeCents != 120_000 {
		t.Errorf("IncomeCents = %d, want 120000 (100000 EUR + 22000 USD→20000)", s.IncomeCents)
	}
	if s.ExpenseCents != 50_000 {
		t.Errorf("ExpenseCents = %d, want 50000", s.ExpenseCents)
	}
	if s.NetCents != 70_000 {
		t.Errorf("NetCents = %d, want 70000", s.NetCents)
	}
	if s.TotalBalanceCents != 20_000 {
		t.Errorf("TotalBalanceCents = %d, want 20000", s.TotalBalanceCents)
	}

	// Daily expenses merged per date and date-ordered.
	if len(s.DailyExpenses) != 2 || s.DailyExpenses[0].Date != "2026-09-01" {
		t.Fatalf("DailyExpenses = %+v, want two days ascending", s.DailyExpenses)
	}
	if s.DailyExpenses[1].ExpenseCents != 10_000 {
		t.Errorf("USD day converted = %d, want 10000", s.DailyExpenses[1].ExpenseCents)
	}

	// Top categories: merged across currencies, ranked, top 5.
	if len(s.TopCategories) != 2 || s.TopCategories[0].CategoryName != "Groceries" {
		t.Fatalf("TopCategories = %+v, want Groceries first", s.TopCategories)
	}
	if s.TopCategories[0].TotalCents != 40_000 {
		t.Errorf("Groceries total = %d, want 40000 (30000 + 11000 USD→10000)", s.TopCategories[0].TotalCents)
	}

	// Budget: category spend (40000) + converted bill spend (20000); lifetime
	// spend matches the range here, so the envelope keeps 10000 of headroom.
	if len(s.Budgets) != 1 {
		t.Fatalf("Budgets = %+v, want one", s.Budgets)
	}
	if s.Budgets[0].SpentCents != 60_000 {
		t.Errorf("SpentCents = %d, want 60000", s.Budgets[0].SpentCents)
	}
	if s.Budgets[0].LifetimeSpentCents != 60_000 {
		t.Errorf("LifetimeSpentCents = %d, want 60000", s.Budgets[0].LifetimeSpentCents)
	}
	if s.Budgets[0].RemainingCents != 10_000 {
		t.Errorf("RemainingCents = %d, want 10000", s.Budgets[0].RemainingCents)
	}

	if len(s.ConversionWarnings) != 0 {
		t.Errorf("ConversionWarnings = %v, want none", s.ConversionWarnings)
	}
}

func TestMonthSummaryWarnsOnMissingRate(t *testing.T) {
	raw := summaryTestRaw()
	raw.Income = append(raw.Income, domain.CurrencyAmount{Currency: "JPY", Cents: 500})
	svc, _ := newTestSummaryService(raw, "EUR", summaryTestSnapshot(), nil)

	s, err := svc.MonthSummary(context.Background(), "2026-09")
	if err != nil {
		t.Fatal(err)
	}
	// No JPY rate → converted 1:1 and reported as a warning.
	if s.IncomeCents != 120_500 {
		t.Errorf("IncomeCents = %d, want 120500 (JPY 1:1)", s.IncomeCents)
	}
	if len(s.ConversionWarnings) != 1 || s.ConversionWarnings[0] != "JPY" {
		t.Errorf("ConversionWarnings = %v, want [JPY]", s.ConversionWarnings)
	}
}

func TestMonthSummaryBaseCurrencyConversion(t *testing.T) {
	// Same data, base USD: EUR amounts convert at 1/1.1.
	svc, _ := newTestSummaryService(summaryTestRaw(), "USD", summaryTestSnapshot(), nil)

	s, err := svc.MonthSummary(context.Background(), "2026-09")
	if err != nil {
		t.Fatal(err)
	}
	if s.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", s.Currency)
	}
	if s.ExpenseCents != 55_000 {
		t.Errorf("ExpenseCents = %d, want 55000 (50000 EUR→USD at 1.1)", s.ExpenseCents)
	}
}

func TestMonthSummaryRejectsBadMonth(t *testing.T) {
	svc, _ := newTestSummaryService(domain.RawSummary{}, "EUR", domain.RateSnapshot{}, nil)
	if _, err := svc.MonthSummary(context.Background(), "2026-13"); err == nil {
		t.Fatal("expected validation error for month 2026-13")
	}
}

func TestMonthSummaryMapsToMonthBounds(t *testing.T) {
	svc, store := newTestSummaryService(summaryTestRaw(), "EUR", summaryTestSnapshot(), nil)
	if _, err := svc.MonthSummary(context.Background(), "2026-02"); err != nil {
		t.Fatal(err)
	}
	if store.from != "2026-02-01" || store.to != "2026-02-28" {
		t.Errorf("store queried %s..%s, want 2026-02-01..2026-02-28", store.from, store.to)
	}
}

func TestRangeSummaryValidation(t *testing.T) {
	svc, _ := newTestSummaryService(domain.RawSummary{}, "EUR", domain.RateSnapshot{}, nil)
	cases := []struct {
		name     string
		from, to string
	}{
		{"to before from", "2026-09-30", "2026-09-01"},
		{"bad from format", "2026-9-1", "2026-09-30"},
		{"impossible date", "2026-02-30", "2026-03-01"},
		{"empty from", "", "2026-09-30"},
	}
	for _, tc := range cases {
		if _, err := svc.RangeSummary(context.Background(), tc.from, tc.to); err == nil {
			t.Errorf("%s: expected validation error, got nil", tc.name)
		}
	}
	// Single-day ranges are valid.
	if _, err := svc.RangeSummary(context.Background(), "2026-09-11", "2026-09-11"); err != nil {
		t.Errorf("single-day range rejected: %v", err)
	}
}

func TestRangeSummaryBudgetsForAnyRange(t *testing.T) {
	// Open envelopes are reported for any range — the old
	// single-calendar-month gate is gone (budgets are open-ended).
	raw := summaryTestRaw()
	// A closed envelope with no in-range activity must stay hidden. Category 3
	// has no spend anywhere, so nothing attributes to it.
	raw.Budgets = append(raw.Budgets, domain.Budget{
		ID: 11, CategoryID: 3, AmountCents: 50_000, Status: domain.BudgetClosed,
	})
	svc, _ := newTestSummaryService(raw, "EUR", summaryTestSnapshot(), nil)
	s, err := svc.RangeSummary(context.Background(), "2026-08-31", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Budgets) != 1 || s.Budgets[0].ID != 10 {
		t.Fatalf("Budgets = %+v, want only open budget 10", s.Budgets)
	}
	if s.Budgets[0].SpentCents != 60_000 || s.Budgets[0].LifetimeSpentCents != 60_000 || s.Budgets[0].RemainingCents != 10_000 {
		t.Errorf("BudgetStatus = %+v, want spent 60000 lifetime 60000 remaining 10000", s.Budgets[0])
	}
	if s.From != "2026-08-31" || s.To != "2026-09-30" {
		t.Errorf("From/To = %q/%q, want echoed back", s.From, s.To)
	}

	// A closed envelope WITH in-range activity stays visible so its final
	// spend can be evaluated.
	raw.BillBudgetSpend = append(raw.BillBudgetSpend,
		domain.BudgetCurrencySpend{BudgetID: 11, Currency: "EUR", Cents: 5_000})
	raw.LifetimeBillBudgetSpend = append(raw.LifetimeBillBudgetSpend,
		domain.BudgetCurrencySpend{BudgetID: 11, Currency: "EUR", Cents: 45_000})
	svc, _ = newTestSummaryService(raw, "EUR", summaryTestSnapshot(), nil)
	s, err = svc.RangeSummary(context.Background(), "2026-09-01", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Budgets) != 2 {
		t.Fatalf("Budgets = %+v, want both budgets", s.Budgets)
	}
	closed := s.Budgets[1]
	if closed.ID != 11 || closed.SpentCents != 5_000 || closed.LifetimeSpentCents != 45_000 || closed.RemainingCents != 5_000 {
		t.Errorf("closed budget status = %+v, want spent 5000 lifetime 45000 remaining 5000", closed)
	}
}

func glanceTestRaw() domain.RawGlance {
	return domain.RawGlance{
		MonthIncome: []domain.CurrencyAmount{
			{Currency: "EUR", Cents: 100_000},
			{Currency: "USD", Cents: 22_000}, // 220 USD ≈ 200 EUR at 1.1
		},
		MonthExpense: []domain.CurrencyAmount{{Currency: "EUR", Cents: 50_000}},
		Balances: []domain.CurrencyAmount{
			{Currency: "EUR", Cents: 10_000},
			{Currency: "USD", Cents: 11_000}, // 100 EUR at 1.1
		},
		Expense30Days: []domain.CurrencyAmount{
			{Currency: "EUR", Cents: 45_000},
			{Currency: "USD", Cents: 16_500}, // → 15_000 EUR, 60_000 total
		},
		PendingReceipts: 3,
		PendingTotals: []domain.CurrencyAmount{
			{Currency: "EUR", Cents: 2_000},
			{Currency: "USD", Cents: 3_300}, // → 3_000 EUR
		},
	}
}

func newTestGlanceService(raw domain.RawGlance, base string, snap domain.RateSnapshot) (*SummaryService, *fakeSummaryStore) {
	svc, fake := newTestSummaryService(domain.RawSummary{}, base, snap, nil)
	fake.rawGlance = raw
	return svc, fake
}

func TestGlanceConvertsIntoBaseCurrency(t *testing.T) {
	svc, _ := newTestGlanceService(glanceTestRaw(), "EUR", summaryTestSnapshot())

	g, err := svc.Glance(context.Background(), "2026-10-12")
	if err != nil {
		t.Fatal(err)
	}
	if g.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", g.Currency)
	}
	if g.Month != "2026-10" {
		t.Errorf("Month = %q, want 2026-10", g.Month)
	}
	if g.IncomeCents != 120_000 {
		t.Errorf("IncomeCents = %d, want 120000 (100000 EUR + 22000 USD→20000)", g.IncomeCents)
	}
	if g.ExpenseCents != 50_000 {
		t.Errorf("ExpenseCents = %d, want 50000", g.ExpenseCents)
	}
	if g.NetCents != 70_000 {
		t.Errorf("NetCents = %d, want 70000", g.NetCents)
	}
	if g.NetWorthCents != 20_000 {
		t.Errorf("NetWorthCents = %d, want 20000", g.NetWorthCents)
	}
	// Trailing-30-day expense 60000 EUR → 2000 per day.
	if g.BurnRateCents != 2_000 {
		t.Errorf("BurnRateCents = %d, want 2000", g.BurnRateCents)
	}
	if g.PendingReceipts != 3 {
		t.Errorf("PendingReceipts = %d, want 3", g.PendingReceipts)
	}
	if g.PendingReceiptCents != 5_000 {
		t.Errorf("PendingReceiptCents = %d, want 5000 (2000 EUR + 3300 USD→3000)", g.PendingReceiptCents)
	}
	if len(g.ConversionWarnings) != 0 {
		t.Errorf("ConversionWarnings = %v, want none", g.ConversionWarnings)
	}
}

func TestGlanceWindowsFollowToday(t *testing.T) {
	svc, store := newTestGlanceService(domain.RawGlance{}, "EUR", summaryTestSnapshot())

	if _, err := svc.Glance(context.Background(), "2026-10-12"); err != nil {
		t.Fatal(err)
	}
	// Cash flow covers the current calendar month; the burn window is the
	// trailing 30 days, today included.
	if store.monthFrom != "2026-10-01" || store.monthTo != "2026-10-31" {
		t.Errorf("month window %s..%s, want 2026-10-01..2026-10-31", store.monthFrom, store.monthTo)
	}
	if store.burnFrom != "2026-09-13" || store.burnTo != "2026-10-12" {
		t.Errorf("burn window %s..%s, want 2026-09-13..2026-10-12", store.burnFrom, store.burnTo)
	}
}

func TestGlanceRejectsBadDate(t *testing.T) {
	svc, _ := newTestGlanceService(domain.RawGlance{}, "EUR", summaryTestSnapshot())
	for _, today := range []string{"", "2026-13-01", "2026-02-30", "2026-1-5"} {
		if _, err := svc.Glance(context.Background(), today); err == nil {
			t.Errorf("today %q: expected validation error, got nil", today)
		}
	}
}

// TestRangeSummaryMonthlyBudgetBoundsToMonth checks the monthly cadence:
// transaction spend counts only inside the row's own month (later months
// belong to successors), while explicit bill links stay all-time.
func TestRangeSummaryMonthlyBudgetBoundsToMonth(t *testing.T) {
	raw := func(rangeCatSpend int64) domain.RawSummary {
		return domain.RawSummary{
			From:            "2026-08-01",
			To:              "2026-08-31",
			CategorySpend:   []domain.CategoryCurrencyTotal{{CategoryID: 3, Currency: "EUR", TotalCents: rangeCatSpend}},
			BillBudgetSpend: []domain.BudgetCurrencySpend{{BudgetID: 20, Currency: "EUR", Cents: 3_000}},
			CategoryMonthSpend: []domain.CategoryMonthCurrencyTotal{
				{CategoryID: 3, Month: "2026-08", Currency: "EUR", TotalCents: 12_000},
				{CategoryID: 3, Month: "2026-09", Currency: "EUR", TotalCents: 9_000},
			},
			LifetimeCategorySpend:   []domain.CategoryCurrencyTotal{{CategoryID: 3, Currency: "EUR", TotalCents: 21_000}},
			LifetimeBillBudgetSpend: []domain.BudgetCurrencySpend{{BudgetID: 20, Currency: "EUR", Cents: 3_000}},
			Budgets: []domain.Budget{
				{ID: 20, CategoryID: 3, AmountCents: 50_000, Status: domain.BudgetClosed, Cadence: domain.BudgetMonthly, Month: "2026-08"},
				{ID: 21, CategoryID: 3, AmountCents: 50_000, Status: domain.BudgetOpen, Cadence: domain.BudgetMonthly, Month: "2026-09"},
			},
		}
	}

	// Viewing August: the closed August row reports its month's bounded spend
	// (12_000 transactions + 3_000 explicitly linked bill lines); the open
	// September row takes its September evaluation even from an August view.
	svc, _ := newTestSummaryService(raw(12_000), "EUR", summaryTestSnapshot(), nil)
	s, err := svc.RangeSummary(context.Background(), "2026-08-01", "2026-08-31")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Budgets) != 2 {
		t.Fatalf("Budgets = %+v, want both monthly rows", s.Budgets)
	}
	aug, sep := s.Budgets[0], s.Budgets[1]
	if aug.ID != 20 || aug.SpentCents != 15_000 || aug.LifetimeSpentCents != 15_000 || aug.RemainingCents != 35_000 {
		t.Errorf("august row = %+v, want spent 15000 lifetime 15000 remaining 35000", aug)
	}
	if sep.ID != 21 || sep.SpentCents != 0 || sep.LifetimeSpentCents != 9_000 {
		t.Errorf("september row = %+v, want spent 0 (disjoint range) lifetime 9000", sep)
	}

	// Viewing September: the closed August row is disjoint and drops out.
	raw2 := raw(9_000)
	raw2.From, raw2.To = "2026-09-01", "2026-09-30"
	svc, _ = newTestSummaryService(raw2, "EUR", summaryTestSnapshot(), nil)
	s, err = svc.RangeSummary(context.Background(), "2026-09-01", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Budgets) != 1 || s.Budgets[0].ID != 21 {
		t.Fatalf("Budgets = %+v, want only the open September row", s.Budgets)
	}
	if s.Budgets[0].SpentCents != 9_000 || s.Budgets[0].LifetimeSpentCents != 9_000 {
		t.Errorf("september row = %+v, want spent 9000 lifetime 9000", s.Budgets[0])
	}
}
