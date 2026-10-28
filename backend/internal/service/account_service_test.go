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
	// The fake derives like the real store with an empty ledger: the balance
	// equals the opening balance until transactions exist.
	a.BalanceCents = a.OpeningBalanceCents
	f.items[a.ID] = a
	return a, nil
}

func (f *fakeAccountStore) Update(_ context.Context, a domain.Account) (domain.Account, error) {
	if _, ok := f.items[a.ID]; !ok {
		return domain.Account{}, domain.ErrNotFound
	}
	a.BalanceCents = a.OpeningBalanceCents
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

// Ledger is thread plumbing the unit tests don't exercise (the SQLite
// invariant tests own the running-balance math): an empty thread.
func (f *fakeAccountStore) Ledger(context.Context, int64, int, int) ([]domain.LedgerEntry, error) {
	return nil, nil
}

func TestAccountCreateValidatesInput(t *testing.T) {
	svc := &AccountService{accounts: newFakeAccountStore()}
	ctx := context.Background()

	cases := []struct {
		name    string
		input   AccountInput
		wantErr bool
	}{
		{name: "valid", input: AccountInput{Name: "Checking", Type: domain.AccountChecking, OpeningBalanceCents: 1000}, wantErr: false},
		{name: "empty name", input: AccountInput{Name: "  ", Type: domain.AccountChecking}, wantErr: true},
		{name: "bad type", input: AccountInput{Name: "X", Type: "crypto"}, wantErr: true},
		{name: "negative opening is allowed (credit accounts)", input: AccountInput{Name: "Y", Type: domain.AccountCredit, OpeningBalanceCents: -1}, wantErr: false},
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

// fakeAccountGroupStore is an in-memory AccountGroupStore for unit tests.
type fakeAccountGroupStore struct {
	items map[int64]domain.AccountGroup
	next  int64
}

func newFakeAccountGroupStore() *fakeAccountGroupStore {
	return &fakeAccountGroupStore{items: map[int64]domain.AccountGroup{}, next: 1}
}

func (f *fakeAccountGroupStore) List(context.Context) ([]domain.AccountGroup, error) {
	out := []domain.AccountGroup{}
	for _, g := range f.items {
		out = append(out, g)
	}
	return out, nil
}

func (f *fakeAccountGroupStore) GetByID(_ context.Context, id int64) (domain.AccountGroup, error) {
	g, ok := f.items[id]
	if !ok {
		return domain.AccountGroup{}, domain.ErrNotFound
	}
	return g, nil
}

func (f *fakeAccountGroupStore) Create(_ context.Context, g domain.AccountGroup) (domain.AccountGroup, error) {
	g.ID = f.next
	f.next++
	f.items[g.ID] = g
	return g, nil
}

func (f *fakeAccountGroupStore) Update(_ context.Context, g domain.AccountGroup) (domain.AccountGroup, error) {
	if _, ok := f.items[g.ID]; !ok {
		return domain.AccountGroup{}, domain.ErrNotFound
	}
	f.items[g.ID] = g
	return g, nil
}

func (f *fakeAccountGroupStore) Delete(_ context.Context, id int64) error {
	if _, ok := f.items[id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.items, id)
	return nil
}

// TestAccountCreateGroupMembership: a known group_id rides onto the account,
// 0 stays ungrouped (nil), and an unknown id is refused as validation.
func TestAccountCreateGroupMembership(t *testing.T) {
	ctx := context.Background()
	groups := newFakeAccountGroupStore()
	if _, err := groups.Create(ctx, domain.AccountGroup{Name: "Household"}); err != nil {
		t.Fatal(err)
	}
	svc := &AccountService{accounts: newFakeAccountStore(), groups: groups}

	a, err := svc.Create(ctx, AccountInput{Name: "Giro", Type: domain.AccountChecking, GroupID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if a.GroupID == nil || *a.GroupID != 1 {
		t.Fatalf("group_id = %v, want 1", a.GroupID)
	}

	b, err := svc.Create(ctx, AccountInput{Name: "Loose", Type: domain.AccountCash})
	if err != nil {
		t.Fatal(err)
	}
	if b.GroupID != nil {
		t.Fatalf("ungrouped account carries group_id %v, want nil", b.GroupID)
	}

	if _, err := svc.Create(ctx, AccountInput{Name: "X", Type: domain.AccountCash, GroupID: 99}); err == nil || !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown group_id: expected not-found, got %v", err)
	}
}

// TestAccountGroupServiceCRUD covers the presentation groups: create trims,
// update renames, delete ungroups nothing on the store level (the SET NULL
// cascade is a SQLite concern, tested against the repository).
func TestAccountGroupServiceCRUD(t *testing.T) {
	ctx := context.Background()
	svc := &AccountGroupService{groups: newFakeAccountGroupStore()}

	g, err := svc.Create(ctx, AccountGroupInput{Name: "  German banks  "})
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != "German banks" {
		t.Fatalf("name = %q, want trimmed", g.Name)
	}

	if _, err := svc.Update(ctx, g.ID, AccountGroupInput{Name: "Banks"}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Banks" {
		t.Fatalf("updated name = %q, want Banks", got.Name)
	}

	for _, bad := range []AccountGroupInput{{Name: ""}, {Name: "   "}} {
		if _, err := svc.Create(ctx, bad); err == nil || !isValidationError(err) {
			t.Errorf("input %+v: expected validation error, got %v", bad, err)
		}
	}

	if err := svc.Delete(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, g.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete: expected ErrNotFound, got %v", err)
	}
}

func isValidationError(err error) bool {
	return errors.Is(err, domain.ErrValidation)
}
