// Auth and user handlers: the login flow, the own-account profile endpoints,
// and the admin user management. The authenticated user rides in the request
// context (middleware.Auth); handlers never re-resolve it.
package handlers

import (
	"io"
	"net/http"
	"os"

	"home-finance-planner/backend/internal/api/middleware"
	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/service"
)

// AuthHandler handles the public login endpoints and the session lifecycle.
type AuthHandler struct {
	Svc *service.UserService
}

type loginRequest struct {
	UserID       int64  `json:"user_id"`
	Password     string `json:"password"`
	KeepLoggedIn bool   `json:"keep_logged_in"`
}

// LoginUsers returns the public identities for the login dropdown (pre- and
// post-login consumers; a public route, so the payload carries no roles and
// no password material).
func (h *AuthHandler) LoginUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.Svc.LoginUsers(r.Context())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, users)
}

// Login verifies the password and issues the session cookie.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.UserID <= 0 {
		respondError(w, r, http.StatusBadRequest, "user_id is required")
		return
	}
	if req.Password == "" {
		respondError(w, r, http.StatusBadRequest, "password is required")
		return
	}
	token, user, err := h.Svc.Login(r.Context(), req.UserID, req.Password, req.KeepLoggedIn)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	ttl := service.TokenTTL(req.KeepLoggedIn)
	http.SetCookie(w, middleware.SessionCookie(token, ttl, req.KeepLoggedIn))
	respondJSON(w, r, http.StatusOK, user)
}

// Logout deletes the current session and expires the cookie.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(middleware.SessionCookieName); err == nil {
		_ = h.Svc.Logout(r.Context(), cookie.Value)
	}
	http.SetCookie(w, middleware.ClearSessionCookie())
	w.WriteHeader(http.StatusNoContent)
}

// Me returns the authenticated user (the middleware resolves it per request,
// so this is always fresh).
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		respondError(w, r, http.StatusUnauthorized, "authentication required")
		return
	}
	respondJSON(w, r, http.StatusOK, user)
}

// MeHandler handles the own-account profile (settings page).
type MeHandler struct {
	Svc *service.UserService
}

// principal returns the authenticated user or answers 401 directly.
func principal(w http.ResponseWriter, r *http.Request) (domain.User, bool) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		respondError(w, r, http.StatusUnauthorized, "authentication required")
		return domain.User{}, false
	}
	return user, true
}

type updateProfileRequest struct {
	FullName string `json:"full_name"`
}

// Update rewrote the own display name.
func (h *MeHandler) Update(w http.ResponseWriter, r *http.Request) {
	user, ok := principal(w, r)
	if !ok {
		return
	}
	var req updateProfileRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	updated, err := h.Svc.UpdateProfile(r.Context(), user.ID, req.FullName)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, updated)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword sets a new password; the confirm-match double-check is a
// client-side affordance. Other remembered devices of this user are logged
// out; the current device stays in.
func (h *MeHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	user, ok := principal(w, r)
	if !ok {
		return
	}
	var req changePasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cookie, _ := r.Cookie(middleware.SessionCookieName)
	if err := h.Svc.ChangePassword(r.Context(), user.ID, cookie.Value,
		req.CurrentPassword, req.NewPassword); err != nil {
		respondServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UploadPhoto stores the uploaded profile image (multipart "photo" field).
func (h *MeHandler) UploadPhoto(w http.ResponseWriter, r *http.Request) {
	user, ok := principal(w, r)
	if !ok {
		return
	}
	if err := r.ParseMultipartForm(service.MaxAvatarBytes); err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("photo")
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "missing photo file field")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, service.MaxAvatarBytes+1))
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "could not read photo file")
		return
	}
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	updated, err := h.Svc.SetPhoto(r.Context(), user.ID, mimeType, data)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, updated)
}

// RemovePhoto clears the own avatar.
func (h *MeHandler) RemovePhoto(w http.ResponseWriter, r *http.Request) {
	user, ok := principal(w, r)
	if !ok {
		return
	}
	updated, err := h.Svc.RemovePhoto(r.Context(), user.ID)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, updated)
}

// Photo serves the own avatar.
func (h *MeHandler) Photo(w http.ResponseWriter, r *http.Request) {
	user, ok := principal(w, r)
	if !ok {
		return
	}
	serveAvatar(w, r, h.Svc, user.ID)
}

// AdminUserHandler handles the admin-only user management endpoints.
type AdminUserHandler struct {
	Svc *service.UserService
}

// requireAdmin answers 403 for a non-admin principal.
func requireAdmin(w http.ResponseWriter, r *http.Request) (domain.User, bool) {
	user, ok := principal(w, r)
	if !ok {
		return domain.User{}, false
	}
	if !user.IsAdmin {
		respondError(w, r, http.StatusForbidden, "admin only")
		return domain.User{}, false
	}
	return user, true
}

type createUserRequest struct {
	Username string `json:"username"`
	FullName string `json:"full_name"`
	Password string `json:"password"`
	IsAdmin  bool   `json:"is_admin"`
}

// List returns every user (admin only).
func (h *AdminUserHandler) List(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	users, err := h.Svc.AdminList(r.Context())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, users)
}

// Create adds a user; it appears in the login dropdown immediately.
func (h *AdminUserHandler) Create(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	var req createUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	user, err := h.Svc.AdminCreate(r.Context(), service.CreateUserInput{
		Username: req.Username,
		FullName: req.FullName,
		Password: req.Password,
		IsAdmin:  req.IsAdmin,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusCreated, user)
}

type updateUserRequest struct {
	Username string `json:"username"`
	FullName string `json:"full_name"`
	IsAdmin  bool   `json:"is_admin"`
	Password string `json:"password"` // optional: empty keeps the current one
}

// Update edits an existing user (full name, username, admin flag, optional
// password reset). The last-admin and username-uniqueness guards live in the
// service.
func (h *AdminUserHandler) Update(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var req updateUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	user, err := h.Svc.AdminUpdate(r.Context(), id, service.AdminUpdateInput{
		Username: req.Username,
		FullName: req.FullName,
		IsAdmin:  req.IsAdmin,
		Password: req.Password,
	})
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, user)
}

// Delete removes a user and (via the FK) their sessions; the service guards
// self-deletion and the last administrator, and cleans the avatar file up.
func (h *AdminUserHandler) Delete(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Svc.AdminDelete(r.Context(), actor.ID, id); err != nil {
		respondServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Photo serves any user's avatar image — a logged-in household member may see
// every member's tiny photo (the header renders them).
func (h *AdminUserHandler) Photo(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	serveAvatar(w, r, h.Svc, id)
}

// serveAvatar reads and writes an avatar file with a long private cache.
func serveAvatar(w http.ResponseWriter, r *http.Request, svc *service.UserService, id int64) {
	path, mimeType, err := svc.PhotoPath(r.Context(), id)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		respondError(w, r, http.StatusNotFound, "photo missing on disk")
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
}
