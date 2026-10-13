package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// UserRepository is the SQLite-backed store for app users.
type UserRepository struct{ db *sql.DB }

func NewUserRepository(db *sql.DB) *UserRepository { return &UserRepository{db: db} }

const userColumns = `
	u.id, u.username, u.full_name, u.password_hash, u.photo_path, u.is_admin,
	u.created_at, u.updated_at`

const userFrom = `FROM users u`

// List returns every user ordered by username (case-insensitively).
func (r *UserRepository) List(ctx context.Context) ([]domain.User, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+userColumns+` `+userFrom+` ORDER BY u.username COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	users := []domain.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user row: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// Count reports how many users exist (the boot seed's guard: the admin is
// created exactly once, only on the empty table).
func (r *UserRepository) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// GetByID returns one user, or domain.ErrNotFound for unknown ids.
func (r *UserRepository) GetByID(ctx context.Context, id int64) (domain.User, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` `+userFrom+` WHERE u.id = ?`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("get user %d: %w", id, err)
	}
	return u, nil
}

// FindByUsername returns the user whose username matches case-insensitively
// (the UNIQUE index is NOCASE, so this is also the login-time exact match).
func (r *UserRepository) FindByUsername(ctx context.Context, username string) (domain.User, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` `+userFrom+` WHERE u.username = ? COLLATE NOCASE`, username)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("find user %q: %w", username, err)
	}
	return u, nil
}

// Create inserts a user and returns it with its id and timestamps filled in.
// The username's UNIQUE NOCASE index turns a collision into domain.ErrConflict
// via mapWriteError.
func (r *UserRepository) Create(ctx context.Context, u domain.User) (domain.User, error) {
	now := time.Now().Unix()
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO users (username, full_name, password_hash, photo_path, is_admin, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.Username, u.FullName, u.PasswordHash, u.PhotoPath, boolInt(u.IsAdmin), now, now)
	if err != nil {
		return domain.User{}, mapWriteError("create user", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return domain.User{}, fmt.Errorf("user insert id: %w", err)
	}
	return r.GetByID(ctx, id)
}

// UpdateFullName rewrites the display name only.
func (r *UserRepository) UpdateFullName(ctx context.Context, id int64, fullName string) (domain.User, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE users SET full_name = ?, updated_at = ? WHERE id = ?`,
		fullName, time.Now().Unix(), id)
	if err != nil {
		return domain.User{}, mapWriteError("update user name", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.User{}, domain.ErrNotFound
	}
	return r.GetByID(ctx, id)
}

// UpdatePassword replaces the stored bcrypt hash.
func (r *UserRepository) UpdatePassword(ctx context.Context, id int64, passwordHash string) (domain.User, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		passwordHash, time.Now().Unix(), id)
	if err != nil {
		return domain.User{}, mapWriteError("update user password", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.User{}, domain.ErrNotFound
	}
	return r.GetByID(ctx, id)
}

// SetPhoto stores (or clears, with an empty path) the avatar file path.
func (r *UserRepository) SetPhoto(ctx context.Context, id int64, photoPath string) (domain.User, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE users SET photo_path = ?, updated_at = ? WHERE id = ?`,
		photoPath, time.Now().Unix(), id)
	if err != nil {
		return domain.User{}, mapWriteError("set user photo", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.User{}, domain.ErrNotFound
	}
	return r.GetByID(ctx, id)
}

// Delete removes a user and (via the FK) every session of theirs.
func (r *UserRepository) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return mapWriteError("delete user", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func scanUser(row interface{ Scan(...any) error }) (domain.User, error) {
	var (
		u       domain.User
		hash    string
		photo   string
		isAdmin int64
		created int64
		updated int64
	)
	if err := row.Scan(&u.ID, &u.Username, &u.FullName, &hash, &photo, &isAdmin,
		&created, &updated); err != nil {
		return domain.User{}, err
	}
	u.PasswordHash = hash
	u.PhotoPath = photo
	u.HasPhoto = photo != ""
	u.IsAdmin = isAdmin != 0
	u.CreatedAt = time.Unix(created, 0).UTC()
	u.UpdatedAt = time.Unix(updated, 0).UTC()
	return u, nil
}
