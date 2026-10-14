package repository

import (
	"context"
	"testing"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// TestSessionGetUserJoinsAllUserColumns is the regression test for the 0039
// locale rollout break: GetUser JOINs users with its own column list, and a
// list stale against scanUser's expectations fails the Scan on every
// authenticated request — the app boots fine but login bounces back to the
// login page. Creating a user + session and resolving it through the full
// migrated schema pins the two in sync.
func TestSessionGetUserJoinsAllUserColumns(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	users := NewUserRepository(db)
	sessions := NewSessionRepository(db)

	created, err := users.Create(ctx, domain.User{
		Username: "anna", FullName: "Anna", PasswordHash: "hash", Locale: "de",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Locale != "de" {
		t.Fatalf("create round-trip lost the locale: %q", created.Locale)
	}

	expires := time.Now().Add(time.Hour)
	if err := sessions.Create(ctx, "token1", created.ID, expires); err != nil {
		t.Fatal(err)
	}

	u, err := sessions.GetUser(ctx, "token1")
	if err != nil {
		// The failure this test exists for surfaces here.
		t.Fatalf("GetUser must resolve a valid session with the full user shape: %v", err)
	}
	if u.ID != created.ID || u.Username != "anna" || u.Locale != "de" || u.IsAdmin {
		t.Fatalf("resolved user incomplete: %+v", u)
	}

	if _, err := sessions.GetUser(ctx, "unknown"); err != domain.ErrUnauthorized {
		t.Fatalf("unknown token: want ErrUnauthorized, got %v", err)
	}
}