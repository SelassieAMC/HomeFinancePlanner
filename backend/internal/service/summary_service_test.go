package service

import (
	"context"
	"testing"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// fakeSummaryStore returns a canned RawSummary and records the last range
// it was queried with.
type fakeSummaryStore struct {
	raw  domain.RawSummary
	from string
	to   string
}

func (f *fakeSummaryStore) RawRangeSummary(_ context.Context, from, to string) (domain.RawSummary, error) {
	f.from, f.to = from, to
	return f.raw, nil
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
		Budgets: []domain.Budget{
			{ID: 10, CategoryID: 1, Month: "2026-09", AmountCents: 70_000},
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

	// Budget: category spend (40000) + converted bill spend (20000).
	if len(s.Budgets) != 1 {
		t.Fatalf("Budgets = %+v, want one", s.Budgets)
	}
	if s.Budgets[0].SpentCents != 60_000 {
		t.Errorf("SpentCents = %d, want 60000", s.Budgets[0].SpentCents)
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

func TestRangeSummaryBudgetsOnlyInsideSingleMonth(t *testing.T) {
	// A range spanning two months: raw budgets exist but must not surface.
	svc, _ := newTestSummaryService(summaryTestRaw(), "EUR", summaryTestSnapshot(), nil)
	s, err := svc.RangeSummary(context.Background(), "2026-08-31", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Budgets) != 0 {
		t.Errorf("Budgets = %+v, want empty for a multi-month range", s.Budgets)
	}

	// The same data inside one month keeps the budget statuses.
	svc, _ = newTestSummaryService(summaryTestRaw(), "EUR", summaryTestSnapshot(), nil)
	s, err = svc.RangeSummary(context.Background(), "2026-09-01", "2026-09-30")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Budgets) != 1 || s.Budgets[0].SpentCents != 60_000 || s.Budgets[0].RemainingCents != 10_000 {
		t.Errorf("Budgets = %+v, want one budget spent 60000 remaining 10000", s.Budgets)
	}
	if s.From != "2026-09-01" || s.To != "2026-09-30" {
		t.Errorf("From/To = %q/%q, want echoed back", s.From, s.To)
	}
}
