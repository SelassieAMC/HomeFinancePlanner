// Package fx fetches reference exchange rates (Frankfurter API, ECB data)
// and converts integer-cent amounts between currencies. It is the
// outbound-HTTP side, wired behind the service-layer RateFetcher interface.
package fx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// fetchURL returns the full ECB reference table quoted against EUR. No API
// key; one request covers every supported currency. It is a variable so
// tests can point the Fetcher at a local httptest server.
var fetchURL = "https://api.frankfurter.app/latest?from=EUR"

// Fetcher downloads rate snapshots from the Frankfurter API.
type Fetcher struct {
	client *http.Client
}

// New builds a Fetcher whose outbound call honors the given timeout.
func New(timeout time.Duration) *Fetcher {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Fetcher{client: &http.Client{Timeout: timeout}}
}

// frankfurterResponse is the wire shape of /latest?from=EUR.
type frankfurterResponse struct {
	Amount float64            `json:"amount"` // always 1.0
	Base   string             `json:"base"`
	Date   string             `json:"date"`
	Rates  map[string]float64 `json:"rates"`
}

// FetchRates returns one snapshot: pivot EUR, one rate per ECB reference
// currency (EUR itself implicit — RateSnapshot.Rate handles the pivot).
func (f *Fetcher) FetchRates(ctx context.Context) (domain.RateSnapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return domain.RateSnapshot{}, fmt.Errorf("build request: %w", err)
	}

	res, err := f.client.Do(req)
	if err != nil {
		return domain.RateSnapshot{}, fmt.Errorf("request failed: %w", err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return domain.RateSnapshot{}, fmt.Errorf("read response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return domain.RateSnapshot{}, fmt.Errorf("rates provider returned status %d: %s", res.StatusCode, truncate(string(raw), 300))
	}

	var resp frankfurterResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return domain.RateSnapshot{}, fmt.Errorf("decode response: %w", err)
	}
	pivot := resp.Base
	if pivot == "" {
		pivot = "EUR"
	}
	if len(resp.Rates) == 0 {
		return domain.RateSnapshot{}, fmt.Errorf("rates provider returned an empty table")
	}
	return domain.RateSnapshot{
		Pivot:     pivot,
		Date:      resp.Date,
		FetchedAt: time.Now().UTC(),
		Rates:     resp.Rates,
	}, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
