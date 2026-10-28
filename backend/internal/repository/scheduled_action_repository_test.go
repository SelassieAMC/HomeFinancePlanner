package repository

import (
	"context"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

// Regression test against the REAL SQLite schema (a service fake does not
// enforce the DB CHECKs — the derived day-of-month once sailed through the
// fake and died on migration 0037's `day_of_month BETWEEN 1 AND 28` with a
// bare "validation failed"). The month-end clamp itself is pinned by the
// service tests; here only the storage round-trip matters.
func TestScheduledActionRepository_AcceptsMonthEndDays(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	repo := NewScheduledActionRepository(db)

	// A day-31 plan stores day_of_month NULL (the CHECK's bound — the start
	// date is the pattern; the materializer derives from it) and round-trips.
	a, err := repo.CreateAction(ctx, domain.ScheduledAction{
		Name:       "Rent",
		Kind:       domain.ScheduleTransaction,
		Payload:    domain.SchedulePayload{AccountID: func() *int64 { v := int64(1); return &v }(), TransactionKind: domain.TransactionExpense, AmountCents: 98000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-10-31",
	})
	if err != nil {
		t.Fatalf("create with derived day 31: %v", err)
	}
	if a.DayOfMonth != nil {
		t.Fatalf("day 31 must store NULL (CHECK 1..28; derivation reads the start date), got %d", *a.DayOfMonth)
	}

	// A day-10 plan stores the day (the service derives/stamps it; the repo
	// stores what it is given).
	ten := 10
	b, err := repo.CreateAction(ctx, domain.ScheduledAction{
		Name:       "Gym",
		Kind:       domain.ScheduleTransaction,
		Payload:    domain.SchedulePayload{AccountID: func() *int64 { v := int64(1); return &v }(), TransactionKind: domain.TransactionExpense, AmountCents: 2000},
		Recurrence: domain.ScheduleMonthly,
		StartDate:  "2026-10-10",
		DayOfMonth: &ten,
	})
	if err != nil {
		t.Fatalf("create with derived day 10: %v", err)
	}
	if b.DayOfMonth == nil || *b.DayOfMonth != 10 {
		t.Fatalf("day 10 must store the derived day, got %+v", b.DayOfMonth)
	}
}
