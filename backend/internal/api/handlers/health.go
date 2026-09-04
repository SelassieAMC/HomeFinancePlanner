package handlers

import (
	"net/http"

	"home-finance-planner/backend/internal/config"
)

// HealthHandler serves readiness probes.
type HealthHandler struct{ cfg config.Config }

// NewHealthHandler constructs the health handler.
func NewHealthHandler(cfg config.Config) *HealthHandler { return &HealthHandler{cfg: cfg} }

// Check reports service health.
func (h *HealthHandler) Check(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, r, http.StatusOK, map[string]string{
		"status": "ok",
		"env":    h.cfg.Env,
	})
}
