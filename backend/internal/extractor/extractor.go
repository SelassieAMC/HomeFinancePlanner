// Package extractor converts receipt images into structured bill data using
// configured AI vision connectors. One connector family per provider type.
package extractor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// Extractor runs bill extraction against an AI connector.
type Extractor struct {
	client *http.Client
}

// New builds an Extractor whose outbound calls honor the given timeout.
func New(timeout time.Duration) *Extractor {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return &Extractor{client: &http.Client{Timeout: timeout}}
}

// Extract sends the receipt image to the provider and returns the normalized
// draft plus the provider id that produced it.
func (e *Extractor) Extract(ctx context.Context, image []byte, mimeType string, provider domain.AIProvider) (domain.BillDraft, error) {
	if err := checkFileTypeSupport(provider, mimeType); err != nil {
		return domain.BillDraft{}, err
	}

	var raw string
	var err error

	switch provider.Type {
	case domain.AIProviderOllama:
		raw, err = e.ollamaChat(ctx, provider, image)
	case domain.AIProviderOpenAI, domain.AIProviderOpenAICompatible:
		raw, err = e.openAIChat(ctx, provider, image, mimeType)
	case domain.AIProviderGemini:
		raw, err = e.geminiGenerate(ctx, provider, image, mimeType)
	case domain.AIProviderAnthropic:
		raw, err = e.anthropicMessages(ctx, provider, image, mimeType)
	default:
		err = fmt.Errorf("unsupported provider type %q", provider.Type)
	}
	if err != nil {
		return domain.BillDraft{}, fmt.Errorf("extract via %s: %w", provider.Type, err)
	}

	draft, err := ParseBillJSON(raw)
	if err != nil {
		return domain.BillDraft{}, fmt.Errorf("provider %s (%s) returned unreadable output: %w", provider.ID, provider.Type, err)
	}
	return draft, nil
}

// TestConnection verifies a provider is reachable and authorized with a
// cheap metadata request (no image, no generation).
func (e *Extractor) TestConnection(ctx context.Context, provider domain.AIProvider) error {
	base := strings.TrimRight(defaultBaseURL(provider.Type), "/")
	if provider.BaseURL != "" {
		base = strings.TrimRight(provider.BaseURL, "/")
	}
	auth := func(req *http.Request) {}

	var url string
	switch provider.Type {
	case domain.AIProviderOllama:
		url = base + "/api/tags"
	case domain.AIProviderOpenAI, domain.AIProviderOpenAICompatible:
		url = base + "/models"
		auth = func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+provider.APIKey) }
	case domain.AIProviderGemini:
		url = base + "/models?key=" + provider.APIKey
	case domain.AIProviderAnthropic:
		url = base + "/models"
		auth = func(req *http.Request) {
			req.Header.Set("x-api-key", provider.APIKey)
			req.Header.Set("anthropic-version", anthropicVersion)
		}
	default:
		return fmt.Errorf("unsupported provider type %q", provider.Type)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	auth(req)

	res, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("provider returned status %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// --- shared HTTP helpers -----------------------------------------------------

// checkFileTypeSupport verifies the provider family can read the uploaded
// file type before an API call is wasted. PDF receipts are supported by
// Gemini and Anthropic (document block); HEIC/HEIF photos by Gemini only.
// The error names the configured model so the message is actionable.
func checkFileTypeSupport(provider domain.AIProvider, mimeType string) error {
	switch mimeType {
	case "application/pdf":
		switch provider.Type {
		case domain.AIProviderGemini, domain.AIProviderAnthropic:
			return nil
		default:
			return fmt.Errorf("the configured model %q (type %s) cannot read PDF receipts — convert the receipt to a photo (JPEG/PNG), or configure a Gemini or Anthropic connector", provider.Model, provider.Type)
		}
	case "image/heic", "image/heif":
		if provider.Type != domain.AIProviderGemini {
			return fmt.Errorf("the configured model %q (type %s) does not support HEIC/HEIF photos — convert the photo to JPEG before uploading, or configure a Gemini connector which reads iPhone photos directly", provider.Model, provider.Type)
		}
		return nil
	default:
		return nil
	}
}

func (e *Extractor) postJSON(ctx context.Context, url string, headers map[string]string, payload, dest any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	res, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("provider returned status %d: %s", res.StatusCode, truncate(string(raw), 300))
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func defaultBaseURL(t domain.AIProviderType) string {
	switch t {
	case domain.AIProviderOllama:
		return "http://localhost:11434"
	case domain.AIProviderOpenAI, domain.AIProviderOpenAICompatible:
		return "https://api.openai.com/v1"
	case domain.AIProviderGemini:
		return "https://generativelanguage.googleapis.com/v1beta"
	case domain.AIProviderAnthropic:
		return "https://api.anthropic.com/v1"
	default:
		return ""
	}
}

// baseURL resolves a provider's endpoint, falling back to the family default.
func baseURL(provider domain.AIProvider) string {
	if provider.BaseURL != "" {
		return strings.TrimRight(provider.BaseURL, "/")
	}
	return strings.TrimRight(defaultBaseURL(provider.Type), "/")
}

// defaultHeaders returns auth headers per provider family.
func authHeaders(provider domain.AIProvider) map[string]string {
	switch provider.Type {
	case domain.AIProviderOpenAI, domain.AIProviderOpenAICompatible:
		return map[string]string{"Authorization": "Bearer " + provider.APIKey}
	case domain.AIProviderAnthropic:
		return map[string]string{
			"x-api-key":         provider.APIKey,
			"anthropic-version": anthropicVersion,
		}
	default:
		return nil // gemini uses key-as-query-param, ollama needs none
	}
}
