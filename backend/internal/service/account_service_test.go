package service

import (
	"context"
	"errors"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

// fakeAccountStore is an in-memory AccountStore for unit tests.
type fakeAccountStore struct {
	items map[int64]domain.Account
	next  int64
}

func newFakeAccountStore() *fakeAccountStore {
	return &fakeAccountStore{items: map[int64]domain.Account{}, next: 1}
}

func (f *fakeAccountStore) List(context.Context) ([]domain.Account, error) {
	out := []domain.Account{}
	for _, a := range f.items {
		out = append(out, a)
	}
	return out, nil
}

func (f *fakeAccountStore) GetByID(_ context.Context, id int64) (domain.Account, error) {
	a, ok := f.items[id]
	if !ok {
		return domain.Account{}, domain.ErrNotFound
	}
	return a, nil
}

func (f *fakeAccountStore) Create(_ context.Context, a domain.Account) (domain.Account, error) {
	a.ID = f.next
	f.next++
	f.items[a.ID] = a
	return a, nil
}

func (f *fakeAccountStore) Update(_ context.Context, a domain.Account) (domain.Account, error) {
	if _, ok := f.items[a.ID]; !ok {
		return domain.Account{}, domain.ErrNotFound
	}
	f.items[a.ID] = a
	return a, nil
}

func (f *fakeAccountStore) Delete(_ context.Context, id int64) error {
	if _, ok := f.items[id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.items, id)
	return nil
}

func TestAccountCreateValidatesInput(t *testing.T) {
	svc := &AccountService{accounts: newFakeAccountStore()}
	ctx := context.Background()

	cases := []struct {
		name    string
		input   AccountInput
		wantErr bool
	}{
		{name: "valid", input: AccountInput{Name: "Checking", Type: domain.AccountChecking, BalanceCents: 1000}, wantErr: false},
		{name: "empty name", input: AccountInput{Name: "  ", Type: domain.AccountChecking}, wantErr: true},
		{name: "bad type", input: AccountInput{Name: "X", Type: "crypto"}, wantErr: true},
		{name: "negative balance", input: AccountInput{Name: "X", Type: domain.AccountCash, BalanceCents: -1}, wantErr: true},
		{name: "bad currency", input: AccountInput{Name: "X", Type: domain.AccountCash, Currency: "DOLLAR"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Create(ctx, tc.input)
			if tc.wantErr != (err != nil) {
				t.Fatalf("wantErr=%v, got error: %v", tc.wantErr, err)
			}
			if err != nil && !isValidationError(err) {
				t.Fatalf("expected validation error, got: %v", err)
			}
		})
	}
}

func TestAccountCreateDefaultsCurrency(t *testing.T) {
	svc := &AccountService{accounts: newFakeAccountStore()}
	a, err := svc.Create(context.Background(), AccountInput{Name: "Cash", Type: domain.AccountCash})
	if err != nil {
		t.Fatal(err)
	}
	if a.Currency != "USD" {
		t.Fatalf("expected default USD, got %q", a.Currency)
	}
}

func isValidationError(err error) bool {
	return errors.Is(err, domain.ErrValidation)
}
