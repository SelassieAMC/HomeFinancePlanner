package extractor

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"home-finance-planner/backend/internal/domain"
)

const anthropicVersion = "2023-06-01"

// anthropicMessages calls the Messages API with the receipt attached as a
// base64 block: images use the "image" block, PDF receipts the "document"
// block.
func (e *Extractor) anthropicMessages(ctx context.Context, provider domain.AIProvider, image []byte, mimeType string) (string, error) {
	url := baseURL(provider) + "/messages"

	encoded := base64.StdEncoding.EncodeToString(image)
	var attachment map[string]any
	if mimeType == "application/pdf" {
		attachment = map[string]any{
			"type": "document",
			"source": map[string]string{
				"type":       "base64",
				"media_type": mimeType,
				"data":       encoded,
			},
		}
	} else {
		attachment = map[string]any{
			"type": "image",
			"source": map[string]string{
				"type":       "base64",
				"media_type": mimeType,
				"data":       encoded,
			},
		}
	}

	payload := map[string]any{
		"model":      provider.Model,
		"max_tokens": 4096,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": extractionPrompt},
					attachment,
				},
			},
		},
	}

	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := e.postJSON(ctx, url, authHeaders(provider), payload, &resp); err != nil {
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
