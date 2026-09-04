package extractor

import (
	"context"
	"encoding/base64"
	"fmt"

	"home-finance-planner/backend/internal/domain"
)

// openAIChat calls the Chat Completions API with the image as a data URI.
// Also used for openai_compatible providers (OpenRouter, Groq, Ollama's
// OpenAI endpoint, …) since they share the wire format.
func (e *Extractor) openAIChat(ctx context.Context, provider domain.AIProvider, image []byte, mimeType string) (string, error) {
	url := baseURL(provider) + "/chat/completions"

	payload := map[string]any{
		"model": provider.Model,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": extractionPrompt},
					{
						"type": "image_url",
						"image_url": map[string]string{
							"url": fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(image)),
						},
					},
				},
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
