package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

const testFinalOffers = `{"products":[{"product_id":1,"name":"Milk","offers":[{"market":"REWE","price":1.29,"currency":"EUR"}]}]}`

// newOllamaSearchFixture spins up a fake Ollama chat endpoint plus a fake
// hosted web API, points ollamaWebAPI at the latter, and returns a provider
// wired to both. The chat handler reports its requests via chatRequests; the
// web handler verifies the Bearer key itself.
func newOllamaSearchFixture(t *testing.T, chatHandler http.HandlerFunc) (domain.AIProvider, *[]map[string]any) {
	t.Helper()
	var chatRequests []map[string]any
	chat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		chatRequests = append(chatRequests, body)
		chatHandler(w, r)
	}))
	t.Cleanup(chat.Close)

	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key-123" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"unauthorized"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/web_fetch") {
			fmt.Fprint(w, `{"title":"Flyer","content":"milk 1.29 EUR at REWE"}`)
			return
		}
		fmt.Fprint(w, `{"results":[{"title":"REWE milk 1L","url":"https://example.com/rewe","content":"1.29 EUR this week"}]}`)
	}))
	t.Cleanup(web.Close)

	orig := ollamaWebAPI
	t.Cleanup(func() { ollamaWebAPI = orig })
	ollamaWebAPI = web.URL

	provider := domain.AIProvider{
		ID: "p1", Type: domain.AIProviderOllamaWebSearch,
		BaseURL: chat.URL, APIKey: "key-123", Model: "qwen3",
	}
	return provider, &chatRequests
}

func chatJSON(t *testing.T, w http.ResponseWriter, raw string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, raw)
}

// chatFinalOffers responds with a final assistant turn whose content is the
// offers JSON (properly nested, so the quotes survive marshaling).
func chatFinalOffers(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"message": map[string]any{"role": "assistant", "content": testFinalOffers},
	})
}

// A full round trip: the model asks for a web search, the server executes it
// against the hosted web API, feeds the result back, and the model answers
// with the offers JSON.
func TestSearchOffers_OllamaWebSearch_ToolLoop(t *testing.T) {
	calls := 0
	provider, requests := newOllamaSearchFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer key-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if calls == 1 {
			chatJSON(t, w, `{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"web_search","arguments":{"query":"milk price REWE"}}}]}}`)
			return
		}
		chatFinalOffers(t, w)
	})

	res, err := New(0).SearchOffers(context.Background(), provider, "find offers", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Products) != 1 || res.Products[0].Offers[0].PriceCents != 129 {
		t.Errorf("parsed result: %+v", res)
	}
	if calls != 2 {
		t.Errorf("chat calls = %d, want 2 (tool round + final)", calls)
	}

	// The follow-up request must echo the assistant turn and carry the tool
	// result message with the search content.
	if len(*requests) != 2 {
		t.Fatalf("recorded requests = %d, want 2", len(*requests))
	}
	msgs, _ := (*requests)[1]["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("follow-up messages = %d, want user+assistant+tool", len(msgs))
	}
	tool, _ := msgs[2].(map[string]any)
	if tool["role"] != "tool" || tool["tool_name"] != "web_search" {
		t.Errorf("tool message: %v", tool)
	}
	if content, _ := tool["content"].(string); !strings.Contains(content, "1.29 EUR") {
		t.Errorf("tool content missing search results: %q", content)
	}
}

// Some models wrap tool arguments in a JSON string instead of an object.
func TestSearchOffers_OllamaWebSearch_StringArguments(t *testing.T) {
	calls := 0
	provider, _ := newOllamaSearchFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			chatJSON(t, w, `{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"web_search","arguments":"{\"query\":\"milk\"}"}}]}}`)
			return
		}
		chatFinalOffers(t, w)
	})

	if _, err := New(0).SearchOffers(context.Background(), provider, "find offers", nil); err != nil {
		t.Fatalf("string-form arguments should still work: %v", err)
	}
}

// The loop is bounded: a model that never stops requesting tools fails the
// search instead of hanging until the context timeout.
func TestSearchOffers_OllamaWebSearch_LoopCap(t *testing.T) {
	provider, _ := newOllamaSearchFixture(t, func(w http.ResponseWriter, r *http.Request) {
		chatJSON(t, w, `{"message":{"content":"","tool_calls":[{"function":{"name":"web_search","arguments":{"query":"more"}}}]}}`)
	})

	_, err := New(0).SearchOffers(context.Background(), provider, "find offers", nil)
	if err == nil || !strings.Contains(err.Error(), "without producing a final answer") {
		t.Errorf("error = %v, want loop-cap message", err)
	}
}

// A rejected key is fatal for the loop — retrying inside it cannot recover.
func TestSearchOffers_OllamaWebSearch_BadKey(t *testing.T) {
	provider, _ := newOllamaSearchFixture(t, func(w http.ResponseWriter, r *http.Request) {
		chatJSON(t, w, `{"message":{"content":"","tool_calls":[{"function":{"name":"web_search","arguments":{"query":"milk"}}}]}}`)
	})
	// Fixture's web server rejects anything but "Bearer key-123"; use another.
	provider.APIKey = "wrong-key"

	_, err := New(0).SearchOffers(context.Background(), provider, "find offers", nil)
	if err == nil || !strings.Contains(err.Error(), "check the Ollama API key in Settings") {
		t.Errorf("error = %v, want the key-check message", err)
	}
}

// A 400 about tools is turned into an actionable model recommendation.
func TestSearchOffers_OllamaWebSearch_NoToolSupport(t *testing.T) {
	provider, _ := newOllamaSearchFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"registry does not support tools"}`)
	})

	_, err := New(0).SearchOffers(context.Background(), provider, "find offers", nil)
	if err == nil || !strings.Contains(err.Error(), "does not support tool calling") {
		t.Errorf("error = %v, want the tool-support message", err)
	}
}

// A web_fetch tool call is served by the web API's fetch endpoint.
func TestSearchOffers_OllamaWebSearch_WebFetch(t *testing.T) {
	calls := 0
	provider, requests := newOllamaSearchFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			chatJSON(t, w, `{"message":{"content":"","tool_calls":[{"function":{"name":"web_fetch","arguments":{"url":"https://example.com/flyer"}}}]}}`)
			return
		}
		chatFinalOffers(t, w)
	})

	if _, err := New(0).SearchOffers(context.Background(), provider, "find offers", nil); err != nil {
		t.Fatal(err)
	}
	msgs, _ := (*requests)[1]["messages"].([]any)
	tool, _ := msgs[2].(map[string]any)
	if tool["tool_name"] != "web_fetch" {
		t.Errorf("tool message: %v", tool)
	}
	if content, _ := tool["content"].(string); !strings.Contains(content, "1.29 EUR at REWE") {
		t.Errorf("fetch content missing page text: %q", content)
	}
}
