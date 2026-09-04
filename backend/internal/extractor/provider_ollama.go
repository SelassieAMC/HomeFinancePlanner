package extractor

import (
	"context"
	"encoding/base64"
	"fmt"
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
