package extractor

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// SearchOffers asks the provider for current offers of the cart products in
// the user's local markets (the prompt carries them) and returns the
// normalized result.
//
// Connector families with a native web-search tool (Gemini google_search,
// Anthropic web_search, OpenAI search models, Ollama web search with its
// client-side tool loop) get that tool attached. The remaining families
// (plain ollama, openai_compatible) are prompted and asked to report when
// they cannot browse the web; any failure there is turned into
// domain.ErrCannotSearch so the UI can tell the user to switch providers.
//
// log (may be nil) receives per-call tracing: which family ran, how long
// each provider round trip took, and how many offers came back — the data
// needed to explain a slow or empty search.
func (e *Extractor) SearchOffers(ctx context.Context, provider domain.AIProvider, prompt string, log *slog.Logger) (domain.OfferResult, error) {
	if log == nil {
		log = slog.Default()
	}
	start := time.Now()

	var raw string
	var err error
	textOnly := false // no search tool: failures likely mean "cannot browse"

	switch provider.Type {
	case domain.AIProviderGemini:
		raw, err = e.geminiSearch(ctx, provider, prompt)
	case domain.AIProviderAnthropic:
		raw, err = e.anthropicSearch(ctx, provider, prompt)
	case domain.AIProviderOpenAI:
		raw, err = e.openAISearch(ctx, provider, prompt)
	case domain.AIProviderOllamaWebSearch:
		raw, err = e.ollamaSearch(ctx, provider, prompt, log)
	case domain.AIProviderOpenAICompatible, domain.AIProviderOllama:
		textOnly = true
		raw, err = e.textChat(ctx, provider, prompt)
	default:
		err = fmt.Errorf("unsupported provider type %q", provider.Type)
	}
	log.Info("provider search call finished",
		"family", provider.Type, "model", provider.Model,
		"duration", time.Since(start), "err", err != nil,
	)
	if err != nil {
		return domain.OfferResult{}, err
	}

	res, parseErr := ParseOffersJSON(raw)
	if parseErr != nil {
		if textOnly || looksLikeCannotSearch(raw) {
			return domain.OfferResult{}, fmt.Errorf("%w: %v", domain.ErrCannotSearch, parseErr)
		}
		return domain.OfferResult{}, fmt.Errorf("provider %s (%s) returned unreadable output: %w", provider.ID, provider.Type, parseErr)
	}
	log.Info("offer result parsed", "products", len(res.Products), "cannot_search", res.CannotSearch, "offers", countOfferRows(res))
	if res.CannotSearch {
		reason := res.Reason
		if reason == "" {
			reason = "the model reported it cannot browse the web"
		}
		return domain.OfferResult{}, fmt.Errorf("%w: %s", domain.ErrCannotSearch, reason)
	}
	// A refusal without the JSON flag: the model explained in prose that it
	// cannot go online and produced no usable offers.
	if looksLikeCannotSearch(raw) && !hasAnyOffers(res) {
		return domain.OfferResult{}, fmt.Errorf("%w: the model answered without searching", domain.ErrCannotSearch)
	}
	// Prompt-only families have no tool: a structured answer without offers is
	// almost always from memory, which the user did not ask for.
	if textOnly && !hasAnyOffers(res) {
		return domain.OfferResult{}, fmt.Errorf("%w: no web search tool is available on this connector, so no verified offers could be collected", domain.ErrCannotSearch)
	}
	return res, nil
}

// hasAnyOffers reports whether at least one offer row was found anywhere.
func hasAnyOffers(res domain.OfferResult) bool {
	for _, p := range res.Products {
		if len(p.Offers) > 0 {
			return true
		}
	}
	return false
}

// countOfferRows totals the offers across all products (tracing only).
func countOfferRows(res domain.OfferResult) int {
	n := 0
	for _, p := range res.Products {
		n += len(p.Offers)
	}
	return n
}

// cannotSearchPhrases are refusal formulations models use when they cannot go
// online; detected in free-text answers missing the cannot_search flag.
var cannotSearchPhrases = []string{
	"cannot search the web", "can't search the web", "cannot browse the web", "can't browse the web",
	"cannot browse", "can't browse", "cannot access the internet", "can't access the internet",
	"no internet access", "no access to the internet", "don't have access to the internet",
	"do not have access to the internet", "unable to access the internet", "cannot search online",
	"can't search online", "no web browsing", "cannot use the web search", "cannot go online",
}

func looksLikeCannotSearch(raw string) bool {
	lower := strings.ToLower(raw)
	for _, phrase := range cannotSearchPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// geminiSearch calls generateContent text-only with google_search grounding.
// JSON response mode is NOT forced — it conflicts with grounding — so the
// fence/brace parser tolerates prose-wrapped output.
func (e *Extractor) geminiSearch(ctx context.Context, provider domain.AIProvider, prompt string) (string, error) {
	endpoint := fmt.Sprintf("%s/models/%s:generateContent?key=%s",
		baseURL(provider), url.PathEscape(provider.Model), url.QueryEscape(provider.APIKey))

	payload := map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]any{{"text": prompt}}},
		},
		"tools": []map[string]any{
			{"google_search": map[string]any{}},
		},
		"generationConfig": map[string]any{"temperature": 0},
	}

	var resp struct {
		Candidates []struct {
			FinishReason string `json:"finishReason"`
			Content      struct {
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

// anthropicSearch calls the Messages API text-only with the web_search tool
// enabled; only text blocks are returned (tool-result blocks are ignored).
func (e *Extractor) anthropicSearch(ctx context.Context, provider domain.AIProvider, prompt string) (string, error) {
	payload := map[string]any{
		"model":      provider.Model,
		"max_tokens": 4096,
		"tools": []map[string]any{
			{"type": "web_search_20250311", "name": "web_search", "max_uses": 5},
		},
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

// openAISearch first tries the Responses API with the web_search tool; when
// the deployment rejects the tool, it falls back to a prompt-only chat
// completion (whose failures surface as domain.ErrCannotSearch).
func (e *Extractor) openAISearch(ctx context.Context, provider domain.AIProvider, prompt string) (string, error) {
	payload := map[string]any{
		"model": provider.Model,
		"input": prompt,
		"tools": []map[string]any{
			{"type": "web_search"},
		},
	}
	var resp struct {
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	err := e.postJSON(ctx, baseURL(provider)+"/responses", authHeaders(provider), payload, &resp)
	if err != nil {
		if !strings.Contains(err.Error(), "status 400") &&
			!strings.Contains(err.Error(), "status 404") &&
			!strings.Contains(err.Error(), "status 422") {
			return "", err
		}
		// Tool unsupported here — try the prompt-only path before giving up.
		raw, chatErr := e.textChat(ctx, provider, prompt)
		if chatErr != nil {
			return "", err // report the responses-API failure; it is the clearer one
		}
		return raw, nil
	}

	var sb strings.Builder
	for _, item := range resp.Output {
		if item.Type != "message" {
			continue
		}
		for _, c := range item.Content {
			sb.WriteString(c.Text)
		}
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("response contained no message output")
	}
	return sb.String(), nil
}

// textChat is the prompt-only text completion used by connector families
// without a web-search tool (ollama native API, openai_compatible).
func (e *Extractor) textChat(ctx context.Context, provider domain.AIProvider, prompt string) (string, error) {
	switch provider.Type {
	case domain.AIProviderOllama:
		payload := map[string]any{
			"model":  provider.Model,
			"stream": false,
			"messages": []map[string]any{
				{"role": "user", "content": prompt},
			},
			"options": map[string]any{"temperature": 0},
		}
		var resp struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if err := e.postJSON(ctx, baseURL(provider)+"/api/chat", nil, payload, &resp); err != nil {
			return "", err
		}
		if strings.TrimSpace(resp.Message.Content) == "" {
			return "", fmt.Errorf("response contained no message content")
		}
		return resp.Message.Content, nil

	default: // openai_compatible (and the openai fallback path)
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
}
