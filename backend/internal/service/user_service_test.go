package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// fakeUserStore is an in-memory user store recording writes.
type fakeUserStore struct {
	mu    sync.Mutex
	items map[int64]domain.User
	next  int64
}

func newFakeUserStore() *fakeUserStore {
	return &fakeUserStore{items: map[int64]domain.User{}, next: 1}
}

func (f *fakeUserStore) List(context.Context) ([]domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []domain.User{}
	for _, u := range f.items {
		out = append(out, u)
	}
	return out, nil
}

func (f *fakeUserStore) Count(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.items)), nil
}

func (f *fakeUserStore) GetByID(_ context.Context, id int64) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.items[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

func (f *fakeUserStore) FindByUsername(_ context.Context, username string) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.items {
		if strings.EqualFold(u.Username, username) {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}

func (f *fakeUserStore) Create(_ context.Context, u domain.User) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.items {
		if strings.EqualFold(existing.Username, u.Username) {
			return domain.User{}, domain.ErrConflict
		}
	}
	u.ID = f.next
	f.next++
	f.items[u.ID] = u
	return u, nil
}

func (f *fakeUserStore) UpdateFullName(_ context.Context, id int64, fullName string) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.items[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	u.FullName = fullName
	f.items[id] = u
	return u, nil
}

func (f *fakeUserStore) UpdateLocale(_ context.Context, id int64, locale string) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.items[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	u.Locale = locale
	f.items[id] = u
	return u, nil
}

func (f *fakeUserStore) UpdatePassword(_ context.Context, id int64, hash string) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.items[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	u.PasswordHash = hash
	f.items[id] = u
	return u, nil
}

func (f *fakeUserStore) SetPhoto(_ context.Context, id int64, path string) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.items[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	u.PhotoPath = path
	u.HasPhoto = path != ""
	f.items[id] = u
	return u, nil
}

func (f *fakeUserStore) AdminUpdate(_ context.Context, id int64, username, fullName string, isAdmin bool) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.items[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	for _, existing := range f.items {
		if existing.ID != id && strings.EqualFold(existing.Username, username) {
			return domain.User{}, domain.ErrConflict
		}
	}
	u.Username, u.FullName, u.IsAdmin = username, fullName, isAdmin
	f.items[id] = u
	return u, nil
}

func (f *fakeUserStore) Delete(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items[id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.items, id)
	return nil
}

// fakeSessionStore is an in-memory session store.
type fakeSessionStore struct {
	mu     sync.Mutex
	tokens map[string]sessionRow
}

type sessionRow struct {
	userID    int64
	expiresAt time.Time
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{tokens: map[string]sessionRow{}}
}

func (f *fakeSessionStore) Create(_ context.Context, token string, userID int64, expiresAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[token] = sessionRow{userID: userID, expiresAt: expiresAt}
	return nil
}

func (f *fakeSessionStore) GetUser(_ context.Context, token string) (domain.User, error) {
	f.mu.Lock()
	row, ok := f.tokens[token]
	f.mu.Unlock()
	if !ok || !time.Now().Before(row.expiresAt) {
		return domain.User{}, domain.ErrUnauthorized
	}
	return domain.User{ID: row.userID}, nil
}

func (f *fakeSessionStore) Delete(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.tokens, token)
	return nil
}

func (f *fakeSessionStore) DeleteAllForUserExcept(_ context.Context, userID int64, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for t, row := range f.tokens {
		if row.userID == userID && t != token {
			delete(f.tokens, t)
		}
	}
	return nil
}

func (f *fakeSessionStore) PruneExpired(context.Context) (int64, error) { return 0, nil }

func TestEnsureSeedAdminOnlyOnce(t *testing.T) {
	users := newFakeUserStore()
	svc := NewUserService(users, newFakeSessionStore(), t.TempDir())
	ctx := context.Background()

	svc.EnsureSeedAdmin(ctx)
	if len(users.items) != 1 {
		t.Fatalf("seed on empty table: want 1 user, got %d", len(users.items))
	}
	admin := users.items[1]
	if admin.Username != "admin" || !admin.IsAdmin {
		t.Fatalf("seeded admin wrong: %+v", admin)
	}

	// A second boot (users exist) never duplicates or overwrites.
	svc.EnsureSeedAdmin(ctx)
	if n, _ := users.Count(ctx); n != 1 {
		t.Fatalf("second seed created a user: %d", n)
	}
}

func TestLoginVerifiesPasswordAndCreatesSession(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	svc := NewUserService(users, sessions, t.TempDir())
	ctx := context.Background()
	svc.EnsureSeedAdmin(ctx)
	admin, _ := users.FindByUsername(ctx, "admin")

	if _, _, err := svc.Login(ctx, admin.ID, "wrong", false); !errors.Is(err, domain.ErrCredentials) {
		t.Fatalf("wrong password: want ErrCredentials, got %v", err)
	}
	if _, _, err := svc.Login(ctx, 999, "admin", false); !errors.Is(err, domain.ErrCredentials) {
		t.Fatalf("unknown user: want ErrCredentials, got %v", err)
	}

	token, user, err := svc.Login(ctx, admin.ID, "admin", false)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if len(token) != 64 {
		t.Fatalf("token should be 32 random bytes hex (64 chars), got %d", len(token))
	}
	if user.ID != admin.ID {
		t.Fatalf("login returned user %d", user.ID)
	}
	if _, err := svc.Current(ctx, token); err != nil {
		t.Fatalf("session should resolve: %v", err)
	}
}

func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	svc := NewUserService(users, sessions, t.TempDir())
	ctx := context.Background()
	svc.EnsureSeedAdmin(ctx)
	admin, _ := users.FindByUsername(ctx, "admin")

	here, _, _ := svc.Login(ctx, admin.ID, "admin", false)
	other, _, _ := svc.Login(ctx, admin.ID, "admin", false)

	// Wrong current password refused.
	if err := svc.ChangePassword(ctx, admin.ID, here, "wrong", "newpw"); err == nil {
		t.Fatal("wrong current password must fail")
	}
	// Too-short new password refused.
	if err := svc.ChangePassword(ctx, admin.ID, here, "admin", "abc"); err == nil {
		t.Fatal("short new password must fail")
	}

	if err := svc.ChangePassword(ctx, admin.ID, here, "admin", "newpw"); err != nil {
		t.Fatalf("change: %v", err)
	}
	if _, err := svc.Current(ctx, here); err != nil {
		t.Fatalf("current device must stay logged in: %v", err)
	}
	if _, err := svc.Current(ctx, other); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatal("other device must be revoked")
	}
	// Old password dead, new one works.
	if _, _, err := svc.Login(ctx, admin.ID, "admin", false); !errors.Is(err, domain.ErrCredentials) {
		t.Fatal("old password must stop working")
	}
	if _, _, err := svc.Login(ctx, admin.ID, "newpw", false); err != nil {
		t.Fatalf("new password login: %v", err)
	}
}

func TestAdminCreateValidatesAndConflicts(t *testing.T) {
	users := newFakeUserStore()
	svc := NewUserService(users, newFakeSessionStore(), t.TempDir())
	ctx := context.Background()
	svc.EnsureSeedAdmin(ctx)

	u, err := svc.AdminCreate(ctx, CreateUserInput{Username: "Anna", FullName: "Anna Weber", Password: "secret1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.IsAdmin {
		t.Fatal("new users are not admin by default")
	}
	if _, err := svc.AdminCreate(ctx, CreateUserInput{Username: "anna", Password: "secret2"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("case-insensitive duplicate: want ErrConflict, got %v", err)
	}
	if _, err := svc.AdminCreate(ctx, CreateUserInput{Username: "a", Password: "secret2"}); err == nil {
		t.Fatal("too-short username must fail")
	}
	if _, err := svc.AdminCreate(ctx, CreateUserInput{Username: "bob", Password: "x"}); err == nil {
		t.Fatal("too-short password must fail")
	}
}

func TestAdminUpdateEditsAndGuards(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	svc := NewUserService(users, sessions, t.TempDir())
	ctx := context.Background()
	svc.EnsureSeedAdmin(ctx) // admin (id 1, admin)
	anna, _ := svc.AdminCreate(ctx, CreateUserInput{Username: "Anna", FullName: "A", Password: "anna1"})
	bob, _ := svc.AdminCreate(ctx, CreateUserInput{Username: "bob", FullName: "B", Password: "bob123", IsAdmin: true})
	annaSession, _, _ := svc.Login(ctx, anna.ID, "anna1", false)

	// Plain edit works.
	if _, err := svc.AdminUpdate(ctx, anna.ID, AdminUpdateInput{Username: "anna", FullName: "Anna W", IsAdmin: false}); err != nil {
		t.Fatalf("plain edit: %v", err)
	}
	// Taken username refused (case-insensitive).
	if _, err := svc.AdminUpdate(ctx, anna.ID, AdminUpdateInput{Username: "BOB", FullName: "Anna W", IsAdmin: false}); err == nil {
		t.Fatal("taken username must fail")
	}
	// Demoting the last of two admins is fine; the last one is not.
	if _, err := svc.AdminUpdate(ctx, bob.ID, AdminUpdateInput{Username: "bob", FullName: "B", IsAdmin: false}); err != nil {
		t.Fatalf("demote with co-admin: %v", err)
	}
	if _, err := svc.AdminUpdate(ctx, 1, AdminUpdateInput{Username: "admin", FullName: "Administrator", IsAdmin: false}); err == nil {
		t.Fatal("demoting the last admin must fail")
	}
	// Password reset works and revokes every session of the target.
	if _, err := svc.AdminUpdate(ctx, anna.ID, AdminUpdateInput{Username: "anna", FullName: "Anna W", IsAdmin: false, Password: "fresh9"}); err != nil {
		t.Fatalf("reset pw: %v", err)
	}
	if _, err := svc.Current(ctx, annaSession); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatal("password reset must revoke the target's sessions")
	}
	if _, _, err := svc.Login(ctx, anna.ID, "anna1", false); !errors.Is(err, domain.ErrCredentials) {
		t.Fatal("old password must stop working after a reset")
	}
	if _, _, err := svc.Login(ctx, anna.ID, "fresh9", false); err != nil {
		t.Fatalf("reset password login: %v", err)
	}
}

func TestAdminDeleteGuards(t *testing.T) {
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	dir := t.TempDir()
	svc := NewUserService(users, sessions, dir)
	ctx := context.Background()
	svc.EnsureSeedAdmin(ctx)
	admin, _ := users.FindByUsername(ctx, "admin")
	anna, _ := svc.AdminCreate(ctx, CreateUserInput{Username: "Anna", FullName: "A", Password: "anna1"})

	// Self-deletion is refused outright.
	if err := svc.AdminDelete(ctx, admin.ID, admin.ID); err == nil {
		t.Fatal("self-delete must fail")
	}
	// Deleting the last admin is refused.
	if err := svc.AdminDelete(ctx, anna.ID, admin.ID); err == nil {
		t.Fatal("deleting the last admin must fail")
	}
	// A normal delete removes user + session and the avatar file.
	photo := dir + "/anna.jpg"
	if err := os.WriteFile(photo, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	users.mu.Lock()
	updated := users.items[anna.ID]
	updated.PhotoPath = photo
	users.items[anna.ID] = updated
	users.mu.Unlock()
	annaSession, _, _ := svc.Login(ctx, anna.ID, "anna1", false)
	if err := svc.AdminDelete(ctx, admin.ID, anna.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Current(ctx, annaSession); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatal("deleted user's session must die")
	}
	if _, err := os.Stat(photo); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("avatar file must be deleted with the user")
	}
	if err := svc.AdminDelete(ctx, admin.ID, anna.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("second delete: want ErrNotFound")
	}
}

func TestUpdateLocaleStoresValidAndRefusesUnknown(t *testing.T) {
	users := newFakeUserStore()
	svc := NewUserService(users, newFakeSessionStore(), t.TempDir())
	ctx := context.Background()
	created, err := users.Create(ctx, domain.User{Username: "anna", FullName: "Anna", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}

	u, err := svc.UpdateLocale(ctx, created.ID, " de ")
	if err != nil {
		t.Fatalf("valid locale: %v", err)
	}
	if u.Locale != "de" {
		t.Fatalf("want de, got %q", u.Locale)
	}

	for _, bad := range []string{"", "fr", "ENGLISH", "de-DE-x-much-too-long"} {
		if _, err := svc.UpdateLocale(ctx, created.ID, bad); err == nil {
			t.Fatalf("locale %q must be refused", bad)
		}
	}
}
