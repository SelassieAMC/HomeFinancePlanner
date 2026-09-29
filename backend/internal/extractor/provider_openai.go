package extractor

import (
	"context"
	"encoding/base64"
	"fmt"

	"home-finance-planner/backend/internal/domain"
)

// openAIChat calls the Chat Completions API with the receipt files as data
// URIs (one image_url part per file). Also used for openai_compatible
// providers (OpenRouter, Groq, Ollama's OpenAI endpoint, …) since they share
// the wire format.
func (e *Extractor) openAIChat(ctx context.Context, provider domain.AIProvider, files []domain.ReceiptFile, prompt string) (string, error) {
	url := baseURL(provider) + "/chat/completions"

	content := make([]map[string]any, 0, len(files)+1)
	content = append(content, map[string]any{"type": "text", "text": prompt})
	for _, f := range files {
		content = append(content, map[string]any{
			"type": "image_url",
			"image_url": map[string]string{
				"url": fmt.Sprintf("data:%s;base64,%s", f.MimeType, base64.StdEncoding.EncodeToString(f.Data)),
			},
		})
	}
	payload := map[string]any{
		"model": provider.Model,
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": content,
			},
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
	if err := e.postJSON(ctx, url, authHeaders(provider), payload, &resp); err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("response contained no choices")
	}
	return resp.Choices[0].Message.Content, nil
}
