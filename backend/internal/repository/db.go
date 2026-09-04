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

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, name, applied_at)
			 VALUES (?, ?, strftime('%s','now'))`, version, name); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
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
