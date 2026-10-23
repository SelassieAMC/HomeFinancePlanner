// Package fx fetches reference exchange rates (Frankfurter API, ECB data,
// with open.er-api.com filling currencies the ECB does not publish) and
// converts integer-cent amounts between currencies. It is the outbound-HTTP
// side, wired behind the service-layer RateFetcher interface.
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

// fallbackURL is the second source, open.er-api.com's no-key endpoint quoted
// against EUR. The ECB table (Frankfurter) deliberately skips most Latin
// American currencies — COP, CLP, PEN — so without this fill every peso
// amount degrades to a silent 1:1 conversion. Both requests are made once
// per snapshot (one per cache TTL), well inside any free rate limit.
var fallbackURL = "https://open.er-api.com/v6/latest/EUR"

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

// erAPIResponse is the wire shape of open.er-api.com/v6/latest/{base}.
type erAPIResponse struct {
	Result   string             `json:"result"`    // "success"
	BaseCode string             `json:"base_code"` // must match the requested base — the snapshot's pivot math depends on it
	Rates    map[string]float64 `json:"rates"`
}

// FetchRates returns one snapshot: pivot EUR, one rate per reference currency
// (EUR itself implicit — RateSnapshot.Rate handles the pivot). The ECB table
// is the primary source; a fallback source fills the currencies the ECB does
// not publish (COP, …) and takes over as the whole table when the primary is
// down. The fallback failing alone is not an error — a partial-but-honest
// table beats refusing the refresh.
func (f *Fetcher) FetchRates(ctx context.Context) (domain.RateSnapshot, error) {
	snap, err := f.fetchFrankfurter(ctx)
	if err != nil {
		fb, ferr := f.fetchFallback(ctx)
		if ferr != nil {
			return domain.RateSnapshot{}, fmt.Errorf("rate sources failed: %v; %w", err, ferr)
		}
		return fb, nil
	}
	if fb, ferr := f.fetchFallback(ctx); ferr == nil {
		for code, r := range fb.Rates {
			if code == snap.Pivot {
				continue
			}
			if _, ok := snap.Rates[code]; !ok {
				snap.Rates[code] = r
			}
		}
	}
	return snap, nil
}

func (f *Fetcher) fetchFrankfurter(ctx context.Context) (domain.RateSnapshot, error) {
	raw, headerDate, err := f.fetchTable(ctx, fetchURL)
	if err != nil {
		return domain.RateSnapshot{}, err
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
		Date:      firstNonEmpty(resp.Date, headerDate),
		FetchedAt: time.Now().UTC(),
		Rates:     resp.Rates,
	}, nil
}

// fetchFallback reads the er-api table, quoted against EUR. Anything else
// than a "success" EUR response is rejected — rates in another pivot would
// poison every conversion.
func (f *Fetcher) fetchFallback(ctx context.Context) (domain.RateSnapshot, error) {
	raw, _, err := f.fetchTable(ctx, fallbackURL)
	if err != nil {
		return domain.RateSnapshot{}, err
	}
	var resp erAPIResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return domain.RateSnapshot{}, fmt.Errorf("decode fallback response: %w", err)
	}
	if resp.Result != "success" {
		return domain.RateSnapshot{}, fmt.Errorf("fallback rates provider answered %q", resp.Result)
	}
	if strings.ToUpper(strings.TrimSpace(resp.BaseCode)) != "EUR" {
		return domain.RateSnapshot{}, fmt.Errorf("fallback rates provider answered against %q, not EUR", resp.BaseCode)
	}
	if len(resp.Rates) == 0 {
		return domain.RateSnapshot{}, fmt.Errorf("fallback rates provider returned an empty table")
	}
	return domain.RateSnapshot{
		Pivot:     "EUR",
		FetchedAt: time.Now().UTC(),
		Rates:     resp.Rates,
	}, nil
}

// fetchTable performs one outbound GET and returns the body (limited) plus
// the provider's date header value when present.
func (f *Fetcher) fetchTable(ctx context.Context, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build request: %w", err)
	}

	res, err := f.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("request failed: %w", err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, "", fmt.Errorf("read response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, "", fmt.Errorf("rates provider returned status %d: %s", res.StatusCode, truncate(string(raw), 300))
	}
	return raw, res.Header.Get("date"), nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
