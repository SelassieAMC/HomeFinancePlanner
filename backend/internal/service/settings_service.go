package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"home-finance-planner/backend/internal/crypto"
	"home-finance-planner/backend/internal/domain"
)

// SettingsStore is the persistence contract for app settings.
type SettingsStore interface {
	Get(ctx context.Context, key string) (string, error)
	Put(ctx context.Context, key, value string) error
}

const settingsKeyAIProviders = "ai_providers"

// settingsKeyBaseCurrency stores the user's display/base currency code.
const settingsKeyBaseCurrency = "base_currency"

// DefaultBaseCurrency is the base currency when the user has not set one.
const DefaultBaseCurrency = "USD"

// SecretBox is the abstraction services use to encrypt/decrypt secrets.
type SecretBox interface {
	EncryptString(plaintext string) (string, error)
	DecryptString(encoded string) (string, error)
}

// ProviderTester abstracts AI connectivity checks (implemented by extractor).
type ProviderTester interface {
	TestConnection(ctx context.Context, provider domain.AIProvider) error
}

// SettingsService manages AI connector configuration. API keys are encrypted
// at rest and masked in every API-facing response.
type SettingsService struct {
	settings SettingsStore
	box      SecretBox
	tester   ProviderTester
}

func NewSettingsService(settings SettingsStore, box SecretBox, tester ProviderTester) *SettingsService {
	return &SettingsService{settings: settings, box: box, tester: tester}
}

// ListAIProviders returns configured providers with masked API keys. The
// default flag is normalized in memory so legacy stored lists (saved before
// is_default existed) already show a default without waiting for a save.
func (s *SettingsService) ListAIProviders(ctx context.Context) ([]domain.AIProvider, error) {
	stored, err := s.storedProviders(ctx)
	if err != nil {
		return nil, err
	}
	normalizeDefaults(stored)

	out := make([]domain.AIProvider, 0, len(stored))
	for _, p := range stored {
		masked := p
		if key, err := s.box.DecryptString(p.APIKey); err == nil {
			masked.APIKey = crypto.MaskKey(key)
		} else {
			masked.APIKey = "••••" // decrypt failed; hide entirely
		}
		out = append(out, masked)
	}
	return out, nil
}

// ProviderInput is what the UI sends when saving provider configuration.
type ProviderInput struct {
	ID        string
	Type      domain.AIProviderType
	BaseURL   string
	APIKey    string // plaintext from the user; empty = keep stored key
	Model     string
	IsDefault bool
}

// SaveAIProviders persists the provider list, encrypting API keys. An empty
// APIKey in the input means "keep the previously stored key". IDs are fully
// independent per connector: an empty ID always generates a fresh one that
// cannot collide with a stored connector (so a new connector never inherits
// another one's stored key or config), and duplicate IDs within a request are
// rejected. Exactly one connector ends up flagged as the default.
func (s *SettingsService) SaveAIProviders(ctx context.Context, inputs []ProviderInput) ([]domain.AIProvider, error) {
	stored, err := s.storedProviders(ctx)
	if err != nil {
		return nil, err
	}
	storedByKey := make(map[string]domain.AIProvider, len(stored))
	maxN := 0
	for _, p := range stored {
		storedByKey[p.ID] = p
		if n, ok := providerNumber(p.ID); ok && n > maxN {
			maxN = n
		}
	}

	used := make(map[string]bool, len(stored)+len(inputs))
	out := make([]domain.AIProvider, 0, len(inputs))
	for _, in := range inputs {
		id := strings.TrimSpace(in.ID)
		if id == "" {
			// Generate past every stored and already-assigned id so a new
			// connector can never merge into an existing one.
			maxN++
			id = fmt.Sprintf("provider-%d", maxN)
		}
		if used[id] {
			return nil, validationError("duplicate provider id %q", id)
		}
		used[id] = true

		if err := s.validateProvider(in, storedByKey[id]); err != nil {
			return nil, err
		}

		apiKey := storedByKey[id].APIKey // keep existing by default
		if strings.TrimSpace(in.APIKey) != "" {
			enc, err := s.box.EncryptString(strings.TrimSpace(in.APIKey))
			if err != nil {
				return nil, fmt.Errorf("encrypt api key: %w", err)
			}
			apiKey = enc
		}

		out = append(out, domain.AIProvider{
			ID:        id,
			Type:      in.Type,
			BaseURL:   strings.TrimSpace(in.BaseURL),
			APIKey:    apiKey,
			Model:     strings.TrimSpace(in.Model),
			IsDefault: in.IsDefault,
		})
	}
	normalizeDefaults(out)

	if err := s.persistProviders(ctx, out); err != nil {
		return nil, err
	}

	masked := make([]domain.AIProvider, 0, len(out))
	for _, p := range out {
		maskedP := p
		if key, err := s.box.DecryptString(p.APIKey); err == nil {
			maskedP.APIKey = crypto.MaskKey(key)
		}
		masked = append(masked, maskedP)
	}
	return masked, nil
}

// GetProvider returns the provider with its decrypted API key. Internal use
// only (extraction); never exposed through the API.
func (s *SettingsService) GetProvider(ctx context.Context, id string) (domain.AIProvider, error) {
	stored, err := s.storedProviders(ctx)
	if err != nil {
		return domain.AIProvider{}, err
	}
	for _, p := range stored {
		if p.ID == id {
			if key, err := s.box.DecryptString(p.APIKey); err == nil {
				p.APIKey = key
			}
			return p, nil
		}
	}
	return domain.AIProvider{}, fmt.Errorf("%w: provider %q", domain.ErrNotFound, id)
}

// DefaultProvider returns the connector flagged as default, falling back to
// the first configured one (also for legacy stored lists without any flag).
// Used when the client does not pin a provider for extraction.
func (s *SettingsService) DefaultProvider(ctx context.Context) (domain.AIProvider, error) {
	stored, err := s.storedProviders(ctx)
	if err != nil {
		return domain.AIProvider{}, err
	}
	if len(stored) == 0 {
		return domain.AIProvider{}, fmt.Errorf("%w: no AI provider configured", domain.ErrNotFound)
	}
	for _, p := range stored {
		if p.IsDefault {
			return s.GetProvider(ctx, p.ID)
		}
	}
	return s.GetProvider(ctx, stored[0].ID)
}

// TestProvider runs a lightweight connectivity check against the provider.
func (s *SettingsService) TestProvider(ctx context.Context, id string) error {
	p, err := s.GetProvider(ctx, id)
	if err != nil {
		return err
	}
	return s.tester.TestConnection(ctx, p)
}

func (s *SettingsService) validateProvider(in ProviderInput, stored domain.AIProvider) error {
	if !in.Type.Valid() {
		return validationError("type %q must be one of ollama, openai, gemini, anthropic, openai_compatible", in.Type)
	}
	if strings.TrimSpace(in.Model) == "" {
		return validationError("model is required")
	}
	// Ollama is local and needs no key; hosted providers need one (either
	// newly supplied or already stored).
	if in.Type != domain.AIProviderOllama &&
		strings.TrimSpace(in.APIKey) == "" && stored.APIKey == "" {
		return validationError("api_key is required for %s providers", in.Type)
	}
	return nil
}

// BaseCurrency returns the user's display/base currency — the currency all
// aggregated totals are converted into. Falls back to DefaultBaseCurrency
// when unset; a corrupt stored value is treated as unset.
func (s *SettingsService) BaseCurrency(ctx context.Context) (string, error) {
	raw, err := s.settings.Get(ctx, settingsKeyBaseCurrency)
	if errors.Is(err, domain.ErrNotFound) {
		return DefaultBaseCurrency, nil
	}
	if err != nil {
		return "", err
	}
	cur, err := normalizeCurrency(raw)
	if err != nil {
		return DefaultBaseCurrency, nil
	}
	return cur, nil
}

// SaveBaseCurrency validates and persists the display/base currency.
func (s *SettingsService) SaveBaseCurrency(ctx context.Context, currency string) (string, error) {
	cur, err := normalizeCurrency(currency)
	if err != nil {
		return "", err
	}
	if err := s.settings.Put(ctx, settingsKeyBaseCurrency, cur); err != nil {
		return "", fmt.Errorf("save base currency: %w", err)
	}
	return cur, nil
}

func (s *SettingsService) storedProviders(ctx context.Context) ([]domain.AIProvider, error) {
	raw, err := s.settings.Get(ctx, settingsKeyAIProviders)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeStoredProviders(raw)
}

func (s *SettingsService) persistProviders(ctx context.Context, providers []domain.AIProvider) error {
	raw, err := json.Marshal(providers)
	if err != nil {
		return fmt.Errorf("encode providers: %w", err)
	}
	return s.settings.Put(ctx, settingsKeyAIProviders, string(raw))
}

func decodeStoredProviders(raw string) ([]domain.AIProvider, error) {
	var stored []domain.AIProvider
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, fmt.Errorf("decode stored providers: %w", err)
	}
	return stored, nil
}

// normalizeDefaults enforces the default-connector invariant in place: with a
// single provider it is always the default; with none flagged the first is;
// with several flagged the first in list order wins. Deterministic so the
// read path and the write path agree even for legacy stored lists.
func normalizeDefaults(providers []domain.AIProvider) {
	if len(providers) == 0 {
		return
	}
	marked := false
	for i := range providers {
		if providers[i].IsDefault {
			if marked {
				providers[i].IsDefault = false
			} else {
				marked = true
			}
		}
	}
	if !marked {
		providers[0].IsDefault = true
	}
}

// providerNumber extracts the numeric suffix of a generated "provider-N" id.
func providerNumber(id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, "provider-")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
