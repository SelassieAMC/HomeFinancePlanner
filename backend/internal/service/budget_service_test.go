package service

import (
	"context"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

// fakeBudgetStore is an in-memory BudgetStore; RolloverMonthly only records
// the months it was asked to roll to.
type fakeBudgetStore struct {
	items      []domain.Budget
	rolloverTo []string
}

func (f *fakeBudgetStore) List(context.Context) ([]domain.Budget, error) {
	return f.items, nil
}

func (f *fakeBudgetStore) GetByID(_ context.Context, id int64) (domain.Budget, error) {
	for _, b := range f.items {
		if b.ID == id {
			return b, nil
		}
	}
	return domain.Budget{}, domain.ErrNotFound
}

func (f *fakeBudgetStore) Create(_ context.Context, b domain.Budget) (domain.Budget, error) {
	b.ID = int64(len(f.items) + 1)
	f.items = append(f.items, b)
	return b, nil
}

func (f *fakeBudgetStore) Update(_ context.Context, b domain.Budget) (domain.Budget, error) {
	return b, nil
}

func (f *fakeBudgetStore) Delete(context.Context, int64) error { return nil }

func (f *fakeBudgetStore) RolloverMonthly(_ context.Context, month string) error {
	f.rolloverTo = append(f.rolloverTo, month)
	return nil
}

func newTestBudgetService() (*BudgetService, *fakeBudgetStore) {
	store := &fakeBudgetStore{}
	return &BudgetService{
		budgets:    store,
		categories: fakeCategoryNames{cats: []domain.Category{{ID: 1, Name: "Groceries"}}},
	}, store
}

func TestBudgetCreateDefaultsToEnvelope(t *testing.T) {
	svc, _ := newTestBudgetService()
	b, err := svc.Create(context.Background(), BudgetInput{CategoryID: 1, AmountCents: 50_000})
	if err != nil {
		t.Fatal(err)
	}
	if b.Cadence != domain.BudgetEnvelope {
		t.Errorf("Cadence = %q, want open", b.Cadence)
	}
	if b.Month != "" {
		t.Errorf("Month = %q, want empty for envelope", b.Month)
	}
}

func TestBudgetCreateMonthlyStampsCurrentMonth(t *testing.T) {
	svc, _ := newTestBudgetService()
	b, err := svc.Create(context.Background(), BudgetInput{
		CategoryID: 1, AmountCents: 30_000, Cadence: domain.BudgetMonthly,
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.Month != currentMonth() {
		t.Errorf("Month = %q, want %q", b.Month, currentMonth())
	}
}

func TestBudgetCreateRejectsBadCadence(t *testing.T) {
	svc, _ := newTestBudgetService()
	if _, err := svc.Create(context.Background(), BudgetInput{
		CategoryID: 1, AmountCents: 10_000, Cadence: "weekly",
	}); err == nil {
		t.Fatal("expected validation error for cadence weekly")
	}
}

func TestEnsureMonthlyCurrentValidatesAndForwards(t *testing.T) {
	svc, store := newTestBudgetService()
	if err := svc.EnsureMonthlyCurrent(context.Background(), "bogus"); err == nil {
		t.Fatal("expected validation error for month bogus")
	}
	if err := svc.EnsureMonthlyCurrent(context.Background(), "2026-11"); err != nil {
		t.Fatal(err)
	}
	if len(store.rolloverTo) != 1 || store.rolloverTo[0] != "2026-11" {
		t.Errorf("rolloverTo = %v, want [2026-11]", store.rolloverTo)
	}
}

func TestBudgetListTriggersRollover(t *testing.T) {
	svc, store := newTestBudgetService()
	if _, err := svc.List(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(store.rolloverTo) != 1 || store.rolloverTo[0] != currentMonth() {
		t.Errorf("rolloverTo = %v, want [%s]", store.rolloverTo, currentMonth())
	}
}
