package extractor

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"home-finance-planner/backend/internal/domain"
)

const anthropicVersion = "2023-06-01"

// anthropicMessages calls the Messages API with the receipt files attached as
// base64 blocks (one block per part): images use the "image" block, PDF
// receipts the "document" block.
func (e *Extractor) anthropicMessages(ctx context.Context, provider domain.AIProvider, files []domain.ReceiptFile, prompt string) (string, error) {
	url := baseURL(provider) + "/messages"

	content := make([]map[string]any, 0, len(files)+1)
	content = append(content, map[string]any{"type": "text", "text": prompt})
	for _, f := range files {
		blockType := "image"
		if f.MimeType == "application/pdf" {
			blockType = "document"
		}
		content = append(content, map[string]any{
			"type": blockType,
			"source": map[string]string{
				"type":       "base64",
				"media_type": f.MimeType,
				"data":       base64.StdEncoding.EncodeToString(f.Data),
			},
		})
	}

	payload := map[string]any{
		"model":      provider.Model,
		"max_tokens": 4096,
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": content,
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
