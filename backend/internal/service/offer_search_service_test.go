package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"home-finance-planner/backend/internal/domain"
)

func TestMarkBestWorst(t *testing.T) {
	tests := []struct {
		name   string
		offers []domain.OfferRow
		best   []string // markets expected flagged best_price
		worst  []string // markets expected flagged worst_price
	}{
		{
			name: "cheapest and most expensive flagged",
			offers: []domain.OfferRow{
				{Market: "REWE", PriceCents: 129, Currency: "EUR"},
				{Market: "Lidl", PriceCents: 99, Currency: "EUR"},
				{Market: "Edeka", PriceCents: 159, Currency: "EUR"},
			},
			best:  []string{"Lidl"},
			worst: []string{"Edeka"},
		},
		{
			name: "ties flagged together",
			offers: []domain.OfferRow{
				{Market: "REWE", PriceCents: 100, Currency: "EUR"},
				{Market: "Lidl", PriceCents: 100, Currency: "EUR"},
				{Market: "Edeka", PriceCents: 150, Currency: "EUR"},
			},
			best:  []string{"REWE", "Lidl"},
			worst: []string{"Edeka"},
		},
		{
			name: "single offer gets no flags",
			offers: []domain.OfferRow{
				{Market: "REWE", PriceCents: 129, Currency: "EUR"},
			},
			best:  []string{},
			worst: []string{},
		},
		{
			name: "currencies compared separately",
			offers: []domain.OfferRow{
				{Market: "REWE", PriceCents: 129, Currency: "EUR"},
				{Market: "Lidl", PriceCents: 99, Currency: "EUR"},
				{Market: "Mercadona", PriceCents: 150, Currency: "USD"},
			},
			best:  []string{"Lidl"},
			worst: []string{"REWE"}, // worst of the EUR group; 150 USD is alone in its group → no flag
		},
		{
			name: "zero and negative prices ignored",
			offers: []domain.OfferRow{
				{Market: "REWE", PriceCents: 0, Currency: "EUR"},
				{Market: "Lidl", PriceCents: -50, Currency: "EUR"},
				{Market: "Edeka", PriceCents: 120, Currency: "EUR"},
			},
			best:  []string{},
			worst: []string{},
		},
		{
			name: "all equal prices flagged as best only",
			offers: []domain.OfferRow{
				{Market: "REWE", PriceCents: 100, Currency: "EUR"},
				{Market: "Lidl", PriceCents: 100, Currency: "EUR"},
			},
			best:  []string{"REWE", "Lidl"},
			worst: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := &domain.OfferResult{
				Products: []domain.OfferProductResult{{ProductID: 1, Offers: tt.offers}},
			}
			markBestWorst(res)
			product := res.Products[0]

			var best, worst []string
			for _, o := range product.Offers {
				if o.BestPrice {
					best = append(best, o.Market)
				}
				if o.WorstPrice {
					worst = append(worst, o.Market)
				}
			}
			if len(best) != len(tt.best) || !equalAnyOrder(best, tt.best) {
				t.Errorf("best = %v (want %v)", best, tt.best)
			}
			if len(worst) != len(tt.worst) || !equalAnyOrder(worst, tt.worst) {
				t.Errorf("worst = %v (want %v)", worst, tt.worst)
			}
		})
	}
}

// equalAnyOrder compares two string slices ignoring order.
func equalAnyOrder(a, b []string) bool {
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestDescribeSearchError_CannotSearch(t *testing.T) {
	provider := domain.AIProvider{ID: "p1", Type: domain.AIProviderOllama, Model: "llama3.1"}
	err := fmt.Errorf("%w: the model answered without searching", domain.ErrCannotSearch)

	msg := describeSearchError(err, provider, 10*time.Minute)
	for _, want := range []string{`"llama3.1"`, "ollama", "Settings", "Gemini", "Anthropic", "Ollama web-search"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %s", want, msg)
		}
	}
}

func TestDescribeSearchError_Timeout(t *testing.T) {
	provider := domain.AIProvider{ID: "p1", Type: domain.AIProviderGemini, Model: "gemini-2"}
	msg := describeSearchError(context.DeadlineExceeded, provider, 10*time.Minute)
	if !strings.Contains(msg, "timed out after 10m0s") {
		t.Errorf("timeout not explained: %s", msg)
	}
}

func TestDescribeSearchError_Passthrough(t *testing.T) {
	provider := domain.AIProvider{ID: "p1", Type: domain.AIProviderGemini, Model: "gemini-2"}
	raw := errors.New("provider returned status 500: boom")
	if msg := describeSearchError(raw, provider, time.Minute); msg != raw.Error() {
		t.Errorf("raw errors should pass through: %s", msg)
	}
}

func TestCannotSearchDetail(t *testing.T) {
	wrapped := fmt.Errorf("%w: no browsing capability", domain.ErrCannotSearch)
	if got := cannotSearchDetail(wrapped); got != "no browsing capability" {
		t.Errorf("detail: %q", got)
	}
	bare := fmt.Errorf("%w", domain.ErrCannotSearch)
	if got := cannotSearchDetail(bare); got == "" {
		t.Error("bare sentinel should get a fallback detail")
	}
	if got := cannotSearchDetail(errors.New("boom")); got != "" {
		t.Errorf("non-cannot-search error: %q", got)
	}
}

func TestBuildOffersPrompt_IncludesContext(t *testing.T) {
	lastPrice := int64(199)
	products := []domain.OfferSearchProduct{
		{ProductID: 3, Name: "Olive Oil", Brand: "Basso", Unit: "l", Quantity: 2, LastPriceCents: &lastPrice, Currency: "EUR"},
	}
	prompt := BuildOffersPrompt(products, []string{"REWE", "Lidl"})
	for _, want := range []string{
		`"product_id": 3`, `"name": "Olive Oil"`, `"brand_hint": "Basso"`,
		`"quantity_to_buy": 2`, `1.99 EUR`, "REWE, Lidl",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\n---\n%s", want, prompt)
		}
	}

	empty := BuildOffersPrompt(nil, nil)
	if !strings.Contains(empty, "(none recorded)") {
		t.Error("empty context should be spelled out")
	}
}
