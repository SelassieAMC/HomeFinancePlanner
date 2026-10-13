// Package domain: app users and login sessions.
package domain

import (
	"errors"
	"time"
)

var (
	// ErrUnauthorized is returned when the caller is not logged in (no or
	// expired session). Handlers map it to HTTP 401.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrForbidden is returned when a logged-in user lacks the role an
	// action requires (admin-only endpoints). Handlers map it to 403.
	ErrForbidden = errors.New("forbidden")
	// ErrCredentials is a wrong password or an unknown user at login —
	// deliberately indistinguishable from the outside.
	ErrCredentials = errors.New("invalid user or password")
)

// User is an application user (a household member). The dataset stays shared:
// a user is login identity + header avatar, never a data owner. PhotoPath is
// the server-side file path and is never exposed; HasPhoto tells the UI
// whether GET /users/{id}/photo returns an image. PasswordHash never leaves
// the service layer.
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	FullName     string    `json:"full_name"`
	PasswordHash string    `json:"-"`
	PhotoPath    string    `json:"-"`
	HasPhoto     bool      `json:"has_photo"`
	IsAdmin      bool      `json:"is_admin"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// LoginUser is the public identity shown in the login dropdown: the name and
// whether an avatar exists — never password material, and only the id for a
// private self-read (this struct carries no role so the pre-login list is
// inert).
type LoginUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	FullName string `json:"full_name"`
	HasPhoto bool   `json:"has_photo"`
}
