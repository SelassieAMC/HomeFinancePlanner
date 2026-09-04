package extractor

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"home-finance-planner/backend/internal/domain"
)

// geminiGenerate calls the Gemini generateContent endpoint with the image as
// inline_data. JSON response mode is forced via response_mime_type.
func (e *Extractor) geminiGenerate(ctx context.Context, provider domain.AIProvider, image []byte, mimeType string) (string, error) {
	base := baseURL(provider)
	endpoint := fmt.Sprintf("%s/models/%s:generateContent?key=%s",
		base, url.PathEscape(provider.Model), url.QueryEscape(provider.APIKey))

	payload := map[string]any{
		"contents": []map[string]any{
			{
				"parts": []map[string]any{
					{"text": extractionPrompt},
					{
						"inline_data": map[string]string{
							"mime_type": mimeType,
							"data":      base64.StdEncoding.EncodeToString(image),
						},
					},
				},
			},
		},
		"generationConfig": map[string]any{
			"temperature":        0,
			"response_mime_type": "application/json",
			"maxOutputTokens":    4096,
		},
	}

	var resp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := e.postJSON(ctx, endpoint, nil, payload, &resp); err != nil {
		return "", err
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
