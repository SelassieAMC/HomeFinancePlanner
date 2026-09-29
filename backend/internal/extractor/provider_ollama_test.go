package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// newOllamaChatFixture spins up a fake plain-Ollama chat endpoint and records
// every request body (the reasoning-model paths assert on the payload).
func newOllamaChatFixture(t *testing.T, handler http.HandlerFunc) (domain.AIProvider, *[]map[string]any) {
	t.Helper()
	var requests []map[string]any
	chat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		requests = append(requests, body)
		handler(w, r)
	}))
	t.Cleanup(chat.Close)
	provider := domain.AIProvider{
		ID: "p1", Type: domain.AIProviderOllama,
		BaseURL: chat.URL, Model: "glm-5.3-flash:cloud",
	}
	return provider, &requests
}

// Reasoning models are asked not to think, and a turn that ends with the
// answer inside the reasoning block (empty content) is still used.
func TestCompleteText_OllamaThinkingFallback(t *testing.T) {
	calls := 0
	provider, requests := newOllamaChatFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1: // thinking model put the answer in the reasoning block
			chatJSON(t, w, `{"message":{"content":"","thinking":"{\"items\":[{\"name\":\"MILCH\",\"standard_name\":\"Milk\"}]}"}}`)
		default: // content present wins over any thinking
			chatJSON(t, w, `{"message":{"content":"plain answer","thinking":"musing"}}`)
		}
	})
	ctx := context.Background()

	got, err := New(0).CompleteText(ctx, provider, "normalize")
	if err != nil {
		t.Fatalf("thinking-only answer: %v", err)
	}
	if !strings.Contains(got, `"standard_name":"Milk"`) {
		t.Errorf("thinking fallback returned %q, want the reasoning-block answer", got)
	}
	got, err = New(0).CompleteText(ctx, provider, "normalize")
	if err != nil {
		t.Fatalf("content answer: %v", err)
	}
	if got != "plain answer" {
		t.Errorf("content answer = %q, want the content, not the thinking", got)
	}

	// Both calls asked the model not to think (reasoning models musing for
	// the whole turn leave the answer field empty).
	for i, req := range *requests {
		if req["think"] != false {
			t.Errorf("request %d think = %v, want false", i, req["think"])
		}
	}
}

// A turn with neither content nor reasoning is an error naming the model.
func TestCompleteText_OllamaEmptyEverywhere(t *testing.T) {
	provider, _ := newOllamaChatFixture(t, func(w http.ResponseWriter, r *http.Request) {
		chatJSON(t, w, `{"message":{"content":"   ","thinking":""}}`)
	})
	_, err := New(0).CompleteText(context.Background(), provider, "normalize")
	if err == nil || !strings.Contains(err.Error(), "glm-5.3-flash:cloud") {
		t.Errorf("error = %v, want the model named in the empty-response message", err)
	}
}

// The bill-scan path uses the same answer-first request and reasoning-block
// fallback: a thinking-only turn still yields a parsed draft.
func TestExtract_OllamaThinkingFallback(t *testing.T) {
	provider, requests := newOllamaChatFixture(t, func(w http.ResponseWriter, r *http.Request) {
		chatJSON(t, w, `{"message":{"content":"","thinking":"{\"market_name\":\"REWE\",\"date\":\"2026-05-03\",\"payment_method\":\"card\",\"items\":[{\"name\":\"MILCH\",\"quantity\":1,\"unit_price\":1.20,\"line_total\":1.20}]}"}}`)
	})
	draft, err := New(0).Extract(context.Background(), []domain.ReceiptFile{{Data: []byte("fake-image"), MimeType: "image/jpeg"}}, provider, "read this receipt")
	if err != nil {
		t.Fatalf("thinking-only bill read: %v", err)
	}
	if draft.MarketName != "REWE" || len(draft.Items) != 1 || draft.Items[0].Name != "MILCH" {
		t.Errorf("draft = %+v, want the reasoning-block answer parsed", draft)
	}
	if len(*requests) != 1 || (*requests)[0]["think"] != false {
		t.Errorf("bill scan request think = %v, want false", (*requests)[0]["think"])
	}
}

// The web-search loop's final round also falls back to the reasoning block.
func TestSearchOffers_OllamaWebSearch_ThinkingFinalRound(t *testing.T) {
	calls := 0
	provider, requests := newOllamaSearchFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			chatJSON(t, w, `{"message":{"content":"","tool_calls":[{"function":{"name":"web_search","arguments":{"query":"milk"}}}]}}`)
			return
		}
		// Final round: the answer landed in the reasoning block.
		chatJSON(t, w, `{"message":{"content":"","thinking":`+strconv.Quote(testFinalOffers)+`}}`)
	})
	res, err := New(0).SearchOffers(context.Background(), provider, "find offers", nil)
	if err != nil {
		t.Fatalf("thinking-only final round: %v", err)
	}
	if len(res.Products) != 1 || res.Products[0].Offers[0].PriceCents != 129 {
		t.Errorf("parsed result: %+v", res)
	}
	for i, req := range *requests {
		if req["think"] != false {
			t.Errorf("search round %d think = %v, want false", i, req["think"])
		}
	}
}
