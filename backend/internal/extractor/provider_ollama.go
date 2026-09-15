package extractor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"home-finance-planner/backend/internal/domain"
)

// ollamaChat calls the native Ollama chat API with the image attached to the
// message. Works with vision models (llama3.2-vision, gemma3, moondream, …).
func (e *Extractor) ollamaChat(ctx context.Context, provider domain.AIProvider, image []byte) (string, error) {
	url := baseURL(provider) + "/api/chat"

	payload := map[string]any{
		"model":  provider.Model,
		"stream": false,
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": extractionPrompt,
				"images":  []string{base64.StdEncoding.EncodeToString(image)},
			},
		},
		"options": map[string]any{"temperature": 0},
	}

	var resp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := e.postJSON(ctx, url, nil, payload, &resp); err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.Message.Content) == "" {
		return "", fmt.Errorf("response contained no message content")
	}
	return resp.Message.Content, nil
}

// --- offer-search tool loop --------------------------------------------------

// ollamaWebAPI is the hosted Ollama endpoint serving the web-search tools.
// It is always ollama.com — local Ollama servers do not serve these — and is
// a variable so tests can point it at a fake.
var ollamaWebAPI = "https://ollama.com"

// ollamaSearchMaxRounds bounds the agent loop before giving up; the search
// context timeout bounds wall-clock time on top of this.
const ollamaSearchMaxRounds = 8

// ollamaToolResultMax caps each tool result fed back to the model — fetches
// can span thousands of tokens and would blow up the context window.
const ollamaToolResultMax = 8000

// ollamaSearchTools are the function tools offered to the model. Executing
// them is this server's job: Ollama has no server-side grounding, the model
// only declares tool calls (see https://docs.ollama.com/capabilities/web-search).
var ollamaSearchTools = []map[string]any{
	{
		"type": "function",
		"function": map[string]any{
			"name":        "web_search",
			"description": "Search the web and return current results. Use for anything requiring up-to-date information such as current prices.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":       map[string]any{"type": "string", "description": "The web search query"},
					"max_results": map[string]any{"type": "integer", "description": "Maximum number of results to return (default 8, max 10)"},
				},
				"required": []string{"query"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "web_fetch",
			"description": "Fetch the content of a web page by URL",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{"type": "string", "description": "The URL to fetch"},
				},
				"required": []string{"url"},
			},
		},
	},
}

// ollamaToolCall mirrors the tool_calls entries of a chat response.
// Arguments stay raw: some models send an object, others a JSON string.
type ollamaToolCall struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

// ollamaSearch runs the offers prompt as a tool-calling agent loop: the model
// declares web_search/web_fetch calls, this server executes them against the
// hosted Ollama web API (which requires an ollama.com API key), and the
// results are fed back until the model produces its final answer.
func (e *Extractor) ollamaSearch(ctx context.Context, provider domain.AIProvider, prompt string) (string, error) {
	headers := map[string]string{}
	if provider.APIKey != "" {
		headers["Authorization"] = "Bearer " + provider.APIKey
	}

	messages := []map[string]any{{"role": "user", "content": prompt}}
	for round := 0; round < ollamaSearchMaxRounds; round++ {
		payload := map[string]any{
			"model":    provider.Model,
			"stream":   false,
			"messages": messages,
			"tools":    ollamaSearchTools,
			"options":  map[string]any{"temperature": 0},
		}
		var resp struct {
			Message struct {
				Content   string           `json:"content"`
				ToolCalls []ollamaToolCall `json:"tool_calls"`
			} `json:"message"`
		}
		if err := e.postJSON(ctx, baseURL(provider)+"/api/chat", headers, payload, &resp); err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "status 400") && strings.Contains(msg, "tool") {
				return "", fmt.Errorf("the configured model %q does not support tool calling — use a tools-capable model (e.g. qwen3, gpt-oss) for the Ollama web-search connector", provider.Model)
			}
			return "", err
		}
		if len(resp.Message.ToolCalls) == 0 {
			if strings.TrimSpace(resp.Message.Content) == "" {
				return "", fmt.Errorf("response contained no message content")
			}
			return resp.Message.Content, nil
		}

		// Echo the assistant turn, then one tool result message per call —
		// in call order, per the Ollama tool-calling contract.
		messages = append(messages, map[string]any{
			"role":       "assistant",
			"content":    resp.Message.Content,
			"tool_calls": resp.Message.ToolCalls,
		})
		for _, call := range resp.Message.ToolCalls {
			result, err := e.runOllamaWebTool(ctx, headers, call)
			if err != nil {
				// A rejected key cannot recover on retry within this loop.
				msg := err.Error()
				if strings.Contains(msg, "status 401") || strings.Contains(msg, "status 403") {
					return "", err
				}
				// Any other tool failure is fed back so the model can adapt
				// (retry with another query, skip a dead link, …).
				result = "tool failed: " + msg
			}
			messages = append(messages, map[string]any{
				"role":      "tool",
				"tool_name": call.Function.Name,
				"content":   result,
			})
		}
	}
	return "", fmt.Errorf("the model kept requesting web searches without producing a final answer after %d rounds", ollamaSearchMaxRounds)
}

// runOllamaWebTool executes one web_search or web_fetch tool call against the
// hosted Ollama web API and returns the result text for the model.
func (e *Extractor) runOllamaWebTool(ctx context.Context, headers map[string]string, call ollamaToolCall) (string, error) {
	args, err := decodeToolArgs(call.Function.Arguments)
	if err != nil {
		return "", fmt.Errorf("tool call %q: %w", call.Function.Name, err)
	}

	switch call.Function.Name {
	case "web_search":
		query, _ := args["query"].(string)
		if strings.TrimSpace(query) == "" {
			return "", fmt.Errorf(`tool call "web_search": missing "query" argument`)
		}
		payload := map[string]any{"query": query, "max_results": 8}
		var resp struct {
			Results []struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Content string `json:"content"`
			} `json:"results"`
		}
		if err := e.postOllamaWeb(ctx, headers, "/api/web_search", payload, &resp); err != nil {
			return "", err
		}
		var sb strings.Builder
		for i, r := range resp.Results {
			fmt.Fprintf(&sb, "[%d] %s\n%s\n%s\n\n", i+1, strings.TrimSpace(r.Title), r.URL, strings.TrimSpace(r.Content))
		}
		return truncate(sb.String(), ollamaToolResultMax), nil

	case "web_fetch":
		pageURL, _ := args["url"].(string)
		if strings.TrimSpace(pageURL) == "" {
			return "", fmt.Errorf(`tool call "web_fetch": missing "url" argument`)
		}
		payload := map[string]any{"url": pageURL}
		var resp struct {
			Title   string `json:"title"`
			Content string `json:"content"`
		}
		if err := e.postOllamaWeb(ctx, headers, "/api/web_fetch", payload, &resp); err != nil {
			return "", err
		}
		return truncate(strings.TrimSpace(resp.Title)+"\n\n"+strings.TrimSpace(resp.Content), ollamaToolResultMax), nil

	default:
		return "", fmt.Errorf("model requested unknown tool %q", call.Function.Name)
	}
}

// postOllamaWeb posts to the hosted Ollama web API and turns auth failures
// into the actionable key-check message.
func (e *Extractor) postOllamaWeb(ctx context.Context, headers map[string]string, path string, payload, dest any) error {
	if err := e.postJSON(ctx, ollamaWebAPI+path, headers, payload, dest); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "status 401") || strings.Contains(msg, "status 403") {
			return fmt.Errorf("%s — check the Ollama API key in Settings (create one at ollama.com/settings/keys)", msg)
		}
		return fmt.Errorf("Ollama web API call failed: %s", msg)
	}
	return nil
}

// decodeToolArgs accepts tool-call arguments as a JSON object (native Ollama
// shape) or as a string containing one (some models emit it that way).
func decodeToolArgs(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("unreadable arguments")
	}
	obj = map[string]any{}
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		return nil, fmt.Errorf("unreadable arguments")
	}
	return obj, nil
}

// testOllamaChat checks the chat endpoint is reachable (Bearer when a key is
// set — Ollama Cloud requires it, local servers ignore it).
func (e *Extractor) testOllamaChat(ctx context.Context, base string, provider domain.AIProvider) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if provider.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	}
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

// testOllamaWebSearch validates the ollama.com API key with a minimal search
// call — the one capability this connector family exists for.
func (e *Extractor) testOllamaWebSearch(ctx context.Context, provider domain.AIProvider) error {
	headers := map[string]string{}
	if provider.APIKey != "" {
		headers["Authorization"] = "Bearer " + provider.APIKey
	}
	var resp struct {
		Results []json.RawMessage `json:"results"`
	}
	return e.postOllamaWeb(ctx, headers, "/api/web_search", map[string]any{"query": "test", "max_results": 1}, &resp)
}
