package service

import (
	"context"
	"path/filepath"
	"testing"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/repository"
)

// Integration test: the create flow against REAL SQLite (repository.Open
// runs the embedded migrations). The service unit tests run on fakes, which
// do not enforce the schema's CHECK constraints — a derived day-of-month of
// 29..31 once sailed through every fake and died on migration 0037's real
// `CHECK (day_of_month BETWEEN 1 AND 28)` as a bare "validation failed".
// Every kind the dialog can create must insert cleanly.
func TestScheduledActionCreateRealSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open real db: %v", err)
	}
	defer db.Close()

	accounts := repository.NewAccountRepository(db)
	bags := repository.NewBagRepository(db)
	schedules := repository.NewScheduledActionRepository(db)

	bagSvc := &BagService{bags: bags, accounts: accounts, log: discardLogger()}
	svc := &ScheduledActionService{
		actions:      schedules,
		bags:         bagSvc,
		transactions: nil, // not exercised: create only
		accounts:     accounts,
		categories:   nil,
		base:         fakeBaseSource{currency: "EUR"},
		log:          discardLogger(),
	}

	acc, err := accounts.Create(ctx, domain.Account{Name: "Main", Type: domain.AccountChecking, Currency: "EUR", OpeningBalanceCents: 100000})
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	cash, err := accounts.Create(ctx, domain.Account{Name: "Cash", Type: domain.AccountCash, Currency: "EUR", OpeningBalanceCents: 50000})
	if err != nil {
		t.Fatalf("seed second account: %v", err)
	}
	bag, err := bagSvc.Create(ctx, BagInput{Name: "Vacation", AccountID: acc.ID})
	if err != nil {
		t.Fatalf("seed bag: %v", err)
	}

	accID, cashID, bagID := acc.ID, cash.ID, bag.ID

	cases := []struct {
		name string
		in   ScheduledActionInput
	}{
		{"funding", ScheduledActionInput{
			Name: "Mobile", Kind: domain.ScheduleBagFunding,
			Payload:    domain.SchedulePayload{BagID: &bagID, AmountCents: 20000},
			Recurrence: domain.ScheduleMonthly, StartDate: "2026-10-28",
		}},
		{"transfer", ScheduledActionInput{
			Name: "Rent", Kind: domain.ScheduleAccountTransfer,
			Payload:    domain.SchedulePayload{AccountID: &accID, CounterID: &cashID, AmountCents: 100000, Description: "monthly rent"},
			Recurrence: domain.ScheduleMonthly, StartDate: "2026-01-31",
		}},
		{"expense-with-bag", ScheduledActionInput{
			Name: "Insurance", Kind: domain.ScheduleTransaction,
			Payload:    domain.SchedulePayload{AccountID: &accID, TransactionKind: domain.TransactionExpense, AmountCents: 5000, BagID: &bagID},
			Recurrence: domain.ScheduleMonthly, StartDate: "2026-10-31",
		}},
		{"purchase", ScheduledActionInput{
			Name: "Couch", Kind: domain.SchedulePurchase,
			Payload:    domain.SchedulePayload{EstimatedCents: int64Ptr(15000), Note: "optional"},
			Recurrence: domain.ScheduleOnce, StartDate: "2026-10-28",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := svc.Create(ctx, "2026-10-28", tc.in)
			if err != nil {
				t.Fatalf("create %s: %v", tc.name, err)
			}
			if tc.in.Recurrence == domain.ScheduleMonthly && tc.in.StartDate[8:10] > "28" {
				// Days beyond 28 must store NULL (the CHECK bound), never a
				// value the schema would refuse.
				if a.DayOfMonth != nil {
					t.Fatalf("day %s must store NULL, got %d", tc.in.StartDate[8:10], *a.DayOfMonth)
				}
			}
		})
	}
}
