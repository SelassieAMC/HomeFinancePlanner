// Package repository provides SQLite-backed data access for the service layer.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"home-finance-planner/backend/migrations"

	_ "modernc.org/sqlite" // register "sqlite" driver (pure Go, no cgo)
)

// Open opens (creating if necessary) the SQLite database at path, applies
// pending migrations, and returns it. The returned *sql.DB is configured for
// single-writer SQLite access.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	// DSN pragmas: WAL for concurrent readers, busy_timeout to serialize
	// writers, foreign_keys because SQLite defaults them OFF per-connection.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite is a single writer; one connection avoids SQLITE_BUSY churn.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func ensureDir(dir string) error {
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

// fkOffMarker is honored in a migration's comment block: a migration carrying
// it opts out of the pool's foreign_keys enforcement for its own run only.
// Needed by migrations that rebuild a PARENT table (SQLite cannot alter a
// CHECK constraint); with foreign keys on, the rebuild's DROP TABLE of the old
// parent is rejected by the child rows it still holds. The pragma is a no-op
// inside a transaction, so it has to be set on the connection before the
// migration's BEGIN — the runner does that around the migration text.
const fkOffMarker = "pragma: foreign_keys=off"

// migrate applies pending *.sql migrations in filename order, recording each
// applied version in schema_migrations. Each migration runs in one transaction.
func migrate(ctx context.Context, db *sql.DB) error {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	if err := ensureMigrationsTable(ctx, db); err != nil {
		return err
	}

	for i, name := range names {
		version := i + 1

		var applied int64
		err := db.QueryRowContext(ctx,
			`SELECT 1 FROM schema_migrations WHERE version = ?`, version).Scan(&applied)
		switch {
		case err == nil:
			continue // already applied
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("check migration %d: %w", version, err)
		}

		sqlBytes, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		// The pragma must land on the connection BEFORE the migration's BEGIN
		// (it is a no-op inside a transaction) and this pool has exactly one
		// connection, so it is set outside and restored after the run.
		appliedOff, err := setForeignKeys(ctx, db, strings.Contains(strings.ToLower(string(sqlBytes)), fkOffMarker))
		if err != nil {
			return fmt.Errorf("toggle foreign keys for migration %s: %w", name, err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			_ = restoreForeignKeys(ctx, db, appliedOff)
			return fmt.Errorf("begin migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
			tx.Rollback()
			_ = restoreForeignKeys(ctx, db, appliedOff)
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, name, applied_at)
			 VALUES (?, ?, strftime('%s','now'))`, version, name); err != nil {
			tx.Rollback()
			_ = restoreForeignKeys(ctx, db, appliedOff)
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			_ = restoreForeignKeys(ctx, db, appliedOff)
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
		_ = restoreForeignKeys(ctx, db, appliedOff)
	}
	return nil
}

// setForeignKeys turns the connection's foreign-keys enforcement off only when
// the caller asked for it, returning whether it did. Only a migration whose
// text carries fkOffMarker runs with it off (see that constant) — every other
// migration keeps the DSN's enforcement.
func setForeignKeys(ctx context.Context, db *sql.DB, off bool) (bool, error) {
	if !off {
		return false, nil
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return false, err
	}
	return true, nil
}

// restoreForeignKeys re-enables the connection's foreign-keys enforcement the
// DSN asked for. The pool holds a single connection (SetMaxOpenConns(1)), so
// this lands on the same connection the pragma was turned off on. Best-effort:
// the caller's migration result must not be masked, and this exact pragma on
// an open connection cannot fail in practice.
func restoreForeignKeys(ctx context.Context, db *sql.DB, enforce bool) error {
	if !enforce {
		return nil
	}
	_, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	return err
}

func ensureMigrationsTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at INTEGER NOT NULL
		)`)
	return err
}

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint failure.
// Used to translate driver errors into domain.ErrConflict.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}
