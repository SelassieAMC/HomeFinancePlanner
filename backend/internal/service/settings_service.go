package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// ListAIProviders returns configured providers with masked API keys.
func (s *SettingsService) ListAIProviders(ctx context.Context) ([]domain.AIProvider, error) {
	stored, err := s.storedProviders(ctx)
	if err != nil {
		return nil, err
	}

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
	ID      string
	Type    domain.AIProviderType
	BaseURL string
	APIKey  string // plaintext from the user; empty = keep stored key
	Model   string
}

// SaveAIProviders persists the provider list, encrypting API keys. An empty
// APIKey in the input means "keep the previously stored key".
func (s *SettingsService) SaveAIProviders(ctx context.Context, inputs []ProviderInput) ([]domain.AIProvider, error) {
	stored, err := s.storedProviders(ctx)
	if err != nil {
		return nil, err
	}
	storedByKey := make(map[string]domain.AIProvider, len(stored))
	for _, p := range stored {
		storedByKey[p.ID] = p
	}

	out := make([]domain.AIProvider, 0, len(inputs))
	for i, in := range inputs {
		id := strings.TrimSpace(in.ID)
		if id == "" {
			id = fmt.Sprintf("provider-%d", i+1)
		}
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
			ID:      id,
			Type:    in.Type,
			BaseURL: strings.TrimSpace(in.BaseURL),
			APIKey:  apiKey,
			Model:   strings.TrimSpace(in.Model),
		})
	}

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

// FirstProvider returns the first configured provider, used when the client
// does not pin one for extraction.
func (s *SettingsService) FirstProvider(ctx context.Context) (domain.AIProvider, error) {
	stored, err := s.storedProviders(ctx)
	if err != nil {
		return domain.AIProvider{}, err
	}
	if len(stored) == 0 {
		return domain.AIProvider{}, fmt.Errorf("%w: no AI provider configured", domain.ErrNotFound)
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
