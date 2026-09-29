package extractor

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"home-finance-planner/backend/internal/domain"
)

// CompleteText runs a prompt-only text completion against any connector
// family — no image, no web-search tool. It backs the product-name
// normalization job; responses are parsed by the caller (the JSON shape is
// pinned by the prompt, not by this transport).
func (e *Extractor) CompleteText(ctx context.Context, provider domain.AIProvider, prompt string) (string, error) {
	switch provider.Type {
	case domain.AIProviderGemini:
		return e.geminiText(ctx, provider, prompt)
	case domain.AIProviderAnthropic:
		return e.anthropicText(ctx, provider, prompt)
	case domain.AIProviderOpenAI:
		return e.openAIText(ctx, provider, prompt)
	case domain.AIProviderOllamaWebSearch:
		// The web-search connector points at a plain Ollama server; the tool
		// loop is irrelevant for a text completion, so speak vanilla Ollama.
		ollama := provider
		ollama.Type = domain.AIProviderOllama
		return e.textChat(ctx, ollama, prompt)
	case domain.AIProviderOllama, domain.AIProviderOpenAICompatible:
		return e.textChat(ctx, provider, prompt)
	default:
		return "", fmt.Errorf("unsupported provider type %q", provider.Type)
	}
}

// geminiText calls generateContent without tools or grounding.
func (e *Extractor) geminiText(ctx context.Context, provider domain.AIProvider, prompt string) (string, error) {
	endpoint := fmt.Sprintf("%s/models/%s:generateContent?key=%s",
		baseURL(provider), url.PathEscape(provider.Model), url.QueryEscape(provider.APIKey))

	payload := map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]any{{"text": prompt}}},
		},
		"generationConfig": map[string]any{"temperature": 0},
	}

	var resp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if err := e.postJSON(ctx, endpoint, nil, payload, &resp); err != nil {
		return "", err
	}
	if resp.PromptFeedback.BlockReason != "" {
		return "", fmt.Errorf("request blocked by the provider (%s)", resp.PromptFeedback.BlockReason)
	}

	var sb strings.Builder
	for _, c := range resp.Candidates {
		for _, p := range c.Content.Parts {
			sb.WriteString(p.Text)
		}
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("response contained no candidates")
	}
	return sb.String(), nil
}

// anthropicText calls the Messages API text-only, without tools.
func (e *Extractor) anthropicText(ctx context.Context, provider domain.AIProvider, prompt string) (string, error) {
	payload := map[string]any{
		"model":      provider.Model,
		"max_tokens": 4096,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
	}

	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := e.postJSON(ctx, baseURL(provider)+"/messages", authHeaders(provider), payload, &resp); err != nil {
		return "", err
	}

	var sb strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("response contained no text blocks")
	}
	return sb.String(), nil
}

// openAIText calls the chat-completions API (the Responses API adds nothing
// for a plain text completion).
func (e *Extractor) openAIText(ctx context.Context, provider domain.AIProvider, prompt string) (string, error) {
	payload := map[string]any{
		"model": provider.Model,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 4096,
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := e.postJSON(ctx, baseURL(provider)+"/chat/completions", authHeaders(provider), payload, &resp); err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("response contained no choices")
	}
	return resp.Choices[0].Message.Content, nil
}

// NormalizeNames runs the prompt-only normalization call and parses the
// {"items":[…]} answer into raw→standard(+generic) mappings (service-facing
// wrapper).
func (e *Extractor) NormalizeNames(ctx context.Context, provider domain.AIProvider, prompt string) ([]domain.ProductNameMapping, error) {
	raw, err := e.CompleteText(ctx, provider, prompt)
	if err != nil {
		return nil, err
	}
	items, err := ParseNormalizationJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("provider %s (%s) returned unreadable output: %w", provider.ID, provider.Type, err)
	}
	out := make([]domain.ProductNameMapping, 0, len(items))
	for _, it := range items {
		out = append(out, domain.ProductNameMapping{RawName: it.Name, StandardName: it.StandardName, GenericName: it.GenericName})
	}
	return out, nil
}

// NormalizedName is one raw→standard(+generic) answer of the normalization
// prompt.
type NormalizedName struct {
	Name         string `json:"name"`
	StandardName string `json:"standard_name"`
	GenericName  string `json:"generic_name"`
}

// ParseNormalizationJSON extracts the {"items":[…]} object from a model
// response and returns the raw→standard(+generic) pairs. Names are matched
// back to the prompt inputs case-insensitively by the caller; entries
// missing a standard name are dropped (the raw name then stays unmapped).
// The generic name is optional — a custom prompt without the field yields
// empty generics.
func ParseNormalizationJSON(raw string) ([]NormalizedName, error) {
	var wire struct {
		Items []NormalizedName `json:"items"`
	}
	if err := decodeBestObject(raw, &wire, func(w *struct {
		Items []NormalizedName `json:"items"`
	}) bool {
		return len(w.Items) > 0
	}); err != nil {
		return nil, err
	}
	out := []NormalizedName{}
	for _, it := range wire.Items {
		name := strings.TrimSpace(it.Name)
		standard := strings.TrimSpace(it.StandardName)
		if name == "" || standard == "" {
			continue
		}
		out = append(out, NormalizedName{Name: name, StandardName: standard, GenericName: strings.TrimSpace(it.GenericName)})
	}
	return out, nil
}
