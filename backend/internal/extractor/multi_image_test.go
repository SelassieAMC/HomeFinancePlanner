package extractor

// Per-family payload tests for multi-part receipts: every part of one long
// receipt must reach the provider in a single request, in upload order.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

// billDraftJSON is the minimal draft a fake provider answers with.
const billDraftJSON = `{"market_name":"REWE","date":"2026-05-03","items":[{"name":"MILCH","quantity":1,"unit_price":1.20,"line_total":1.20}]}`

// multiParts stands in for the top/middle/bottom photos of one long receipt.
func multiParts() []domain.ReceiptFile {
	return []domain.ReceiptFile{
		{Data: []byte("top-photo"), MimeType: "image/jpeg"},
		{Data: []byte("middle-photo"), MimeType: "image/png"},
		{Data: []byte("bottom-photo"), MimeType: "image/webp"},
	}
}

// newCaptureFixture spins up a fake provider endpoint that records the JSON
// body of every request and answers with respond.
func newCaptureFixture(t *testing.T, respond func(w http.ResponseWriter)) (string, *[]map[string]any) {
	t.Helper()
	var requests []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		requests = append(requests, body)
		respond(w)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &requests
}

func requireOneRequest(t *testing.T, requests *[]map[string]any) map[string]any {
	t.Helper()
	if len(*requests) != 1 {
		t.Fatalf("provider saw %d requests, want 1", len(*requests))
	}
	return (*requests)[0]
}

func TestExtract_OllamaSendsEveryPart(t *testing.T) {
	url, requests := newCaptureFixture(t, func(w http.ResponseWriter) {
		chatJSON(t, w, `{"message":{"content":`+quoteJSON(t, billDraftJSON)+`}}`)
	})
	provider := domain.AIProvider{ID: "p1", Type: domain.AIProviderOllama, BaseURL: url, Model: "llava"}
	parts := multiParts()

	draft, err := New(0).Extract(context.Background(), parts, provider, "read this receipt")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if draft.MarketName != "REWE" {
		t.Fatalf("draft = %+v, want the parsed bill", draft)
	}

	req := requireOneRequest(t, requests)
	messages, _ := req["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages = %#v, want one user turn", messages)
	}
	imgs, ok := messages[0].(map[string]any)["images"].([]any)
	if !ok || len(imgs) != 3 {
		t.Fatalf("images = %#v, want a 3-entry array", messages[0].(map[string]any)["images"])
	}
	want := []string{"top-photo", "middle-photo", "bottom-photo"}
	for i, img := range imgs {
		raw, err := base64.StdEncoding.DecodeString(img.(string))
		if err != nil || string(raw) != want[i] {
			t.Errorf("images[%d] = %q, want %q", i, img, want[i])
		}
	}
}

func TestExtract_OpenAISendsEveryPart(t *testing.T) {
	url, requests := newCaptureFixture(t, func(w http.ResponseWriter) {
		fmt.Fprint(w, `{"choices":[{"message":{"content":`+quoteJSON(t, billDraftJSON)+`}}]}`)
	})
	provider := domain.AIProvider{ID: "p1", Type: domain.AIProviderOpenAI, BaseURL: url, Model: "gpt-4o"}
	parts := multiParts()

	if _, err := New(0).Extract(context.Background(), parts, provider, "read this receipt"); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	req := requireOneRequest(t, requests)
	messages, _ := req["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages = %#v, want one user turn", messages)
	}
	content, _ := messages[0].(map[string]any)["content"].([]any)
	if len(content) != 4 {
		t.Fatalf("content parts = %d, want text + 3 images", len(content))
	}
	if content[0].(map[string]any)["type"] != "text" {
		t.Error("first content part must be the prompt text")
	}
	wantPrefix := []string{"data:image/jpeg;base64,", "data:image/png;base64,", "data:image/webp;base64,"}
	for i, part := range content[1:] {
		m := part.(map[string]any)
		if m["type"] != "image_url" {
			t.Errorf("content part %d type = %v, want image_url", i+1, m["type"])
			continue
		}
		dataURI := m["image_url"].(map[string]any)["url"].(string)
		if !strings.HasPrefix(dataURI, wantPrefix[i]) {
			t.Errorf("content part %d url = %q, want prefix %q", i+1, dataURI, wantPrefix[i])
		}
	}
}

func TestExtract_GeminiSendsEveryPart(t *testing.T) {
	url, requests := newCaptureFixture(t, func(w http.ResponseWriter) {
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":`+quoteJSON(t, billDraftJSON)+`}]}}]}`)
	})
	provider := domain.AIProvider{ID: "p1", Type: domain.AIProviderGemini, BaseURL: url, Model: "gemini-pro"}
	parts := multiParts()

	if _, err := New(0).Extract(context.Background(), parts, provider, "read this receipt"); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	req := requireOneRequest(t, requests)
	contents, _ := req["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("contents = %#v, want one turn", contents)
	}
	partsOut, _ := contents[0].(map[string]any)["parts"].([]any)
	if len(partsOut) != 4 {
		t.Fatalf("parts = %d, want text + 3 inline_data", len(partsOut))
	}
	if _, ok := partsOut[0].(map[string]any)["text"]; !ok {
		t.Error("first part must be the prompt text")
	}
	wantMimes := []string{"image/jpeg", "image/png", "image/webp"}
	for i, part := range partsOut[1:] {
		inline := part.(map[string]any)["inline_data"].(map[string]any)
		if inline["mime_type"] != wantMimes[i] {
			t.Errorf("inline_data %d mime = %v, want %q", i+1, inline["mime_type"], wantMimes[i])
		}
	}
}

// Anthropic reads mixed groups too: photos become image blocks, a PDF part
// becomes a document block.
func TestExtract_AnthropicSendsEveryPart(t *testing.T) {
	url, requests := newCaptureFixture(t, func(w http.ResponseWriter) {
		fmt.Fprint(w, `{"content":[{"type":"text","text":`+quoteJSON(t, billDraftJSON)+`}]}`)
	})
	provider := domain.AIProvider{ID: "p1", Type: domain.AIProviderAnthropic, BaseURL: url, Model: "claude-sonnet-5"}
	parts := []domain.ReceiptFile{
		{Data: []byte("top-photo"), MimeType: "image/jpeg"},
		{Data: []byte("middle-photo"), MimeType: "image/jpeg"},
		{Data: []byte("bottom-pdf"), MimeType: "application/pdf"},
	}

	if _, err := New(0).Extract(context.Background(), parts, provider, "read this receipt"); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	req := requireOneRequest(t, requests)
	messages, _ := req["messages"].([]any)
	content, _ := messages[0].(map[string]any)["content"].([]any)
	if len(content) != 4 {
		t.Fatalf("content blocks = %d, want text + 3 files", len(content))
	}
	wantTypes := []string{"image", "image", "document"}
	for i, block := range content[1:] {
		if block.(map[string]any)["type"] != wantTypes[i] {
			t.Errorf("block %d type = %v, want %q", i+1, block.(map[string]any)["type"], wantTypes[i])
		}
	}
}

// An unsupported part fails the whole group before any API call, naming the
// offending part.
func TestExtract_UnsupportedPartIsNamed(t *testing.T) {
	url, requests := newCaptureFixture(t, func(w http.ResponseWriter) {
		t.Error("provider must not be called when a part is unsupported")
	})
	provider := domain.AIProvider{ID: "p1", Type: domain.AIProviderOllama, BaseURL: url, Model: "llava"}

	_, err := New(0).Extract(context.Background(), []domain.ReceiptFile{
		{Data: []byte("top-photo"), MimeType: "image/jpeg"},
		{Data: []byte("receipt.pdf"), MimeType: "application/pdf"},
	}, provider, "read this receipt")
	if err == nil || !strings.Contains(err.Error(), "file 2 of 2") || !strings.Contains(err.Error(), "cannot read PDF") {
		t.Fatalf("expected the offending part to be named, got %v", err)
	}
	if len(*requests) != 0 {
		t.Fatalf("expected no provider request, got %d", len(*requests))
	}
}

// The vision discipline is code-owned and rides on the prompt of every
// provider family, not just the Ollama one (which grew the endless-deliberation
// loop the discipline was written for).
func TestExtract_AppendsVisionDisciplineToOpenAI(t *testing.T) {
	url, requests := newCaptureFixture(t, func(w http.ResponseWriter) {
		fmt.Fprint(w, `{"choices":[{"message":{"content":`+quoteJSON(t, billDraftJSON)+`}}]}`)
	})
	provider := domain.AIProvider{ID: "p1", Type: domain.AIProviderOpenAI, BaseURL: url, Model: "gpt-4o"}

	if _, err := New(0).Extract(context.Background(), multiParts(), provider, "read this receipt"); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	req := requireOneRequest(t, requests)
	messages, _ := req["messages"].([]any)
	text, _ := messages[0].(map[string]any)["content"].([]any)
	promptText, _ := text[0].(map[string]any)["text"].(string)
	if !strings.HasPrefix(promptText, "read this receipt") {
		t.Errorf("prompt text = %q, want the managed prompt first", promptText)
	}
	for _, rule := range []string{"OUTPUT DISCIPLINE", "DISCOUNT MARKERS"} {
		if !strings.Contains(promptText, rule) {
			t.Errorf("prompt text missing %q", rule)
		}
	}
}

// quoteJSON marshals s into a JSON string literal (quote escaping included).
func quoteJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
