package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/imageproc"
)

// UserStore is the persistence contract of the auth/user service.
type UserStore interface {
	List(ctx context.Context) ([]domain.User, error)
	Count(ctx context.Context) (int64, error)
	GetByID(ctx context.Context, id int64) (domain.User, error)
	FindByUsername(ctx context.Context, username string) (domain.User, error)
	Create(ctx context.Context, u domain.User) (domain.User, error)
	UpdateFullName(ctx context.Context, id int64, fullName string) (domain.User, error)
	UpdatePassword(ctx context.Context, id int64, passwordHash string) (domain.User, error)
	SetPhoto(ctx context.Context, id int64, photoPath string) (domain.User, error)
}

// SessionStore is the persistence contract for login cookie sessions.
type SessionStore interface {
	Create(ctx context.Context, token string, userID int64, expiresAt time.Time) error
	GetUser(ctx context.Context, token string) (domain.User, error)
	Delete(ctx context.Context, token string) error
	DeleteAllForUserExcept(ctx context.Context, userID int64, token string) error
	PruneExpired(ctx context.Context) (int64, error)
}

// UserService: login/logout, the login-page user list, admin user management
// and the own-account (profile) endpoints. Passwords are bcrypt-hashed; the
// dataset stays shared — a user is login identity + avatar, not a data owner.
type UserService struct {
	users      UserStore
	sessions   SessionStore
	avatarsDir string
	log        *slog.Logger
}

// NewUserService wires the auth/user workflow. avatarsDir is where avatar
// files are stored.
func NewUserService(users UserStore, sessions SessionStore, avatarsDir string) *UserService {
	return &UserService{users: users, sessions: sessions, avatarsDir: avatarsDir, log: slog.Default()}
}

// SeedAdminUsername is the default administrator both the login endpoint and
// the admin user management start from.
const SeedAdminUsername = "admin"

// EnsureSeedAdmin creates the initial admin (username "admin", password
// "admin") exactly once — on an empty users table. A hash is computed at boot
// (never stored as a SQL literal); the user is expected to change the
// password on first use. Failure only logs — an unusable seed must not block
// the whole server from starting.
func (s *UserService) EnsureSeedAdmin(ctx context.Context) {
	n, err := s.users.Count(ctx)
	if err != nil {
		s.log.Error("admin seed skipped: cannot count users", "error", err)
		return
	}
	if n > 0 {
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
	if err != nil {
		s.log.Error("admin seed failed", "error", err)
		return
	}
	_, err = s.users.Create(ctx, domain.User{
		Username:     SeedAdminUsername,
		FullName:     "Administrator",
		PasswordHash: string(hash),
		IsAdmin:      true,
	})
	if err != nil {
		s.log.Error("admin seed failed", "error", err)
		return
	}
	s.log.Warn("seeded initial admin user — sign in as admin/admin and change the password in Settings")
}

// --- Login ------------------------------------------------------------------

// LoginUser is the public identity for the login dropdown.
func (s *UserService) LoginUsers(ctx context.Context) ([]domain.LoginUser, error) {
	users, err := s.users.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.LoginUser, 0, len(users))
	for _, u := range users {
		out = append(out, domain.LoginUser{
			ID:       u.ID,
			Username: u.Username,
			FullName: u.FullName,
			HasPhoto: u.HasPhoto,
		})
	}
	return out, nil
}

// Session lifetimes. Unchecked "keep me logged in" is a browser-session
// cookie expiring after 12h server-side; checked is a persistent cookie
// (30d) whose server-side session expires at the same moment.
const (
	sessionTTL     = 12 * time.Hour
	persistentTTL  = 30 * 24 * time.Hour
	maxPasswordLen = 200
)

// Login verifies the password and creates the session token (the cookie
// value). Unknown user and wrong password return the same
// domain.ErrCredentials. Returns the fresh token and its duration so the
// handler can shape the cookie.
func (s *UserService) Login(ctx context.Context, userID int64, password string, keepLoggedIn bool) (sessionToken string, user domain.User, err error) {
	user, err = s.users.GetByID(ctx, userID)
	if err == domain.ErrNotFound {
		return "", domain.User{}, domain.ErrCredentials
	}
	if err != nil {
		return "", domain.User{}, err
	}
	if user.PasswordHash == "" ||
		bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return "", domain.User{}, domain.ErrCredentials
	}

	ttl := sessionTTL
	if keepLoggedIn {
		ttl = persistentTTL
	}
	expires := time.Now().Add(ttl)
	token, err := s.createSession(ctx, user.ID, expires)
	if err != nil {
		return "", domain.User{}, err
	}
	return token, user, nil
}

// createSession writes a fresh 256-bit token row and returns the token,
// pruning expired rows while it is already touching the table (best-effort).
func (s *UserService) createSession(ctx context.Context, userID int64, expires time.Time) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	token := hex.EncodeToString(buf)
	if err := s.sessions.Create(ctx, token, userID, expires); err != nil {
		return "", fmt.Errorf("store session: %w", err)
	}
	// Best-effort housekeeping: expired rows die whenever a fresh one lands.
	if _, err := s.sessions.PruneExpired(ctx); err != nil {
		s.log.Debug("session prune skipped", "error", err)
	}
	return token, nil
}

// Logout deletes the caller's own session token.
func (s *UserService) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.sessions.Delete(ctx, token)
}

// Current resolves a session token into its owner (the auth middleware's
// backing call).
func (s *UserService) Current(ctx context.Context, token string) (domain.User, error) {
	if token == "" {
		return domain.User{}, domain.ErrUnauthorized
	}
	return s.sessions.GetUser(ctx, token)
}

// TokenTTL returns the lifetime for a session given the keep-logged-in
// choice, so the handler can size the cookie (persistent) or leave it a
// browser-session cookie expiring after the server-side TTL.
func TokenTTL(keepLoggedIn bool) time.Duration {
	if keepLoggedIn {
		return persistentTTL
	}
	return sessionTTL
}

// --- Profile (own account) --------------------------------------------------

// UpdateProfile renames the own display name.
func (s *UserService) UpdateProfile(ctx context.Context, id int64, fullName string) (domain.User, error) {
	fullName = strings.TrimSpace(fullName)
	if len(fullName) > 80 {
		return domain.User{}, validationError("full name must be at most 80 characters")
	}
	return s.users.UpdateFullName(ctx, id, fullName)
}

// ChangePassword verifies the current password, sets the new one and revokes
// every other session of this user (the device that made the change stays
// logged in via exceptToken).
func (s *UserService) ChangePassword(ctx context.Context, id int64, currentToken, current, next string) error {
	user, err := s.users.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if user.PasswordHash == "" ||
		bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(current)) != nil {
		return validationError("current password is wrong")
	}
	if len(next) < 4 {
		return validationError("new password must be at least 4 characters")
	}
	if len(next) > maxPasswordLen {
		return validationError("new password must be at most %d characters", maxPasswordLen)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if _, err := s.users.UpdatePassword(ctx, id, string(hash)); err != nil {
		return err
	}
	if err := s.sessions.DeleteAllForUserExcept(ctx, id, currentToken); err != nil {
		return err
	}
	return nil
}

// MaxAvatarBytes caps uploaded avatar files at 10 MB — the original upload
// may be a heavy camera format (HEIC, TIFF); conversion shrinks it.
const MaxAvatarBytes = 10 << 20

// SetPhoto stores an uploaded avatar, replacing any previous one. Avatars
// normalize to a small UI-sized JPEG (256 px) — the header shows them tiny.
func (s *UserService) SetPhoto(ctx context.Context, id int64, mimeType string, file []byte) (domain.User, error) {
	if sniffed := sniffMime(file); strings.HasPrefix(sniffed, "image/") {
		mimeType = sniffed
	}
	mimeType = strings.ToLower(mimeType)
	if _, ok := logoMime[mimeType]; !ok {
		return domain.User{}, validationError("unsupported file type %q (want jpeg, png, webp, gif, tiff, bmp, heic or heif)", mimeType)
	}
	if len(file) == 0 {
		return domain.User{}, validationError("photo file is empty")
	}
	if len(file) > MaxAvatarBytes {
		return domain.User{}, validationError("photo file exceeds %d MB limit", MaxAvatarBytes>>20)
	}
	outs, err := imageproc.Process(ctx, file, mimeType, imageproc.Options{MaxEdge: 256})
	if err != nil {
		return domain.User{}, validationError("%v", err)
	}
	ext, ok := logoMime[outs[0].MimeType]
	if !ok || len(outs) != 1 {
		return domain.User{}, validationError("a photo must be a single image, not a PDF")
	}
	path, err := writeFileRandom(s.avatarsDir, ext, outs[0].Data)
	if err != nil {
		return domain.User{}, fmt.Errorf("save avatar: %w", err)
	}
	existing, err := s.users.GetByID(ctx, id)
	if err != nil {
		_ = os.Remove(path)
		return domain.User{}, err
	}
	updated, err := s.users.SetPhoto(ctx, id, path)
	if err != nil {
		_ = os.Remove(path)
		return domain.User{}, err
	}
	// The replaced file dies; the fresh file stays.
	if existing.PhotoPath != "" && existing.PhotoPath != path {
		s.removeAvatarFile(existing.PhotoPath, "avatar replace")
	}
	return updated, nil
}

// RemovePhoto clears the avatar and deletes its file.
func (s *UserService) RemovePhoto(ctx context.Context, id int64) (domain.User, error) {
	existing, err := s.users.GetByID(ctx, id)
	if err != nil {
		return domain.User{}, err
	}
	updated, err := s.users.SetPhoto(ctx, id, "")
	if err != nil {
		return domain.User{}, err
	}
	if existing.PhotoPath != "" {
		s.removeAvatarFile(existing.PhotoPath, "avatar clear")
	}
	return updated, nil
}

// PhotoPath resolves the avatar file for serving; mirrors LogoPath.
func (s *UserService) PhotoPath(ctx context.Context, id int64) (string, string, error) {
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		return "", "", err
	}
	if u.PhotoPath == "" {
		return "", "", validationError("user %d has no photo", id)
	}
	return u.PhotoPath, detectMime(u.PhotoPath), nil
}

// removeAvatarFile deletes an avatar file best-effort: it is the old file
// after a replace or a cleared photo — deleting always leaves a DB row
// without a photo; failing to delete the file only leaks bytes on disk.
func (s *UserService) removeAvatarFile(path, op string) {
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.log.Warn("could not delete avatar file", "op", op, "error", err)
	}
}

// --- Admin ------------------------------------------------------------------

var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]{2,32}$`)

// CreateUserInput is the admin user-management payload.
type CreateUserInput struct {
	Username string
	FullName string
	Password string
	IsAdmin  bool
}

// AdminCreate adds a user (admin only — the handler enforces the role).
func (s *UserService) AdminCreate(ctx context.Context, in CreateUserInput) (domain.User, error) {
	username := strings.TrimSpace(in.Username)
	if !usernameRegex.MatchString(username) {
		return domain.User{}, validationError(
			"username %q must be 2-32 characters: letters, digits, dots, dashes or underscores", username)
	}
	fullName := strings.TrimSpace(in.FullName)
	if len(fullName) > 80 {
		return domain.User{}, validationError("full name must be at most 80 characters")
	}
	if len(in.Password) < 4 {
		return domain.User{}, validationError("password must be at least 4 characters")
	}
	if len(in.Password) > maxPasswordLen {
		return domain.User{}, validationError("password must be at most %d characters", maxPasswordLen)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return domain.User{}, fmt.Errorf("hash password: %w", err)
	}
	return s.users.Create(ctx, domain.User{
		Username:     username,
		FullName:     fullName,
		PasswordHash: string(hash),
		IsAdmin:      in.IsAdmin,
	})
}

// AdminList returns every user (admin only — the handler enforces the role).
func (s *UserService) AdminList(ctx context.Context) ([]domain.User, error) {
	return s.users.List(ctx)
}
