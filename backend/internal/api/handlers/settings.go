package handlers

import (
	"net/http"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/service"
)

// SettingsHandler handles /api/v1/settings.
type SettingsHandler struct{ Svc *service.SettingsService }

type aiProviderRequest struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	BaseURL string `json:"base_url,omitempty"`
	APIKey  string `json:"api_key,omitempty"`
	Model   string `json:"model"`
}

// ListAIProviders returns configured AI connectors with masked keys.
func (h *SettingsHandler) ListAIProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := h.Svc.ListAIProviders(r.Context())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, providers)
}

// SaveAIProviders replaces the provider list; empty api_key keeps the stored
// key for that provider id.
func (h *SettingsHandler) SaveAIProviders(w http.ResponseWriter, r *http.Request) {
	var req []aiProviderRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	inputs := make([]service.ProviderInput, 0, len(req))
	for _, in := range req {
		inputs = append(inputs, service.ProviderInput{
			ID:      in.ID,
			Type:    domain.AIProviderType(in.Type),
			BaseURL: in.BaseURL,
			APIKey:  in.APIKey,
			Model:   in.Model,
		})
	}

	providers, err := h.Svc.SaveAIProviders(r.Context(), inputs)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, providers)
}

// TestAIProvider checks connectivity for one provider.
func (h *SettingsHandler) TestAIProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Svc.TestProvider(r.Context(), id); err != nil {
		respondJSON(w, r, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	respondJSON(w, r, http.StatusOK, map[string]any{"ok": true})
}

type currencyRequest struct {
	Currency string `json:"currency"`
}

// GetBaseCurrency returns the user's display/base currency.
func (h *SettingsHandler) GetBaseCurrency(w http.ResponseWriter, r *http.Request) {
	currency, err := h.Svc.BaseCurrency(r.Context())
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, currencyRequest{Currency: currency})
}

// SaveBaseCurrency sets the display/base currency all aggregates convert into.
func (h *SettingsHandler) SaveBaseCurrency(w http.ResponseWriter, r *http.Request) {
	var req currencyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	currency, err := h.Svc.SaveBaseCurrency(r.Context(), req.Currency)
	if err != nil {
		respondServiceError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, currencyRequest{Currency: currency})
}
