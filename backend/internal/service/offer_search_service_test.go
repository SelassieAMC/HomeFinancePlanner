package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
		{
			name: "unavailable rows get no flags even with a stray price",
			offers: []domain.OfferRow{
				{Market: "REWE", PriceCents: 129, Currency: "EUR", Availability: domain.OfferNotAvailable},
				{Market: "Lidl", PriceCents: 99, Currency: "EUR"},
				{Market: "Edeka", PriceCents: 0, Currency: "EUR", Availability: domain.OfferNotPublished},
			},
			best:  []string{}, // Lidl alone in the priced group → no flags
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
	prompt := BuildOffersPrompt(products, []string{"REWE", "Lidl"}, nil, domain.OfferNameStrict)
	for _, want := range []string{
		`"product_id": 3`, `"name": "Olive Oil"`, `"brand_hint": "Basso"`,
		`"quantity_to_buy": 2`, `1.99 EUR`, "REWE, Lidl", "STRICT",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\n---\n%s", want, prompt)
		}
	}

	empty := BuildOffersPrompt(nil, nil, nil, "")
	if !strings.Contains(empty, "(none recorded)") {
		t.Error("empty context should be spelled out")
	}
	if !strings.Contains(empty, "STRICT") {
		t.Error("empty name match must default to strict")
	}
}

// Pinned markets switch the prompt to the discriminating scope: one entry per
// market per product, unavailable markets reported, none added outside.
func TestBuildOffersPrompt_PinnedStores(t *testing.T) {
	products := []domain.OfferSearchProduct{{ProductID: 1, Name: "Avocado"}}
	prompt := BuildOffersPrompt(products, []string{"REWE", "Lidl"}, []string{"REWE", "Edeka"}, domain.OfferNameStrict)
	for _, want := range []string{
		"ONLY these markets: REWE, Edeka",
		"one offer entry per listed market",
		`"not_available"`, `"not_published"`,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("pinned prompt missing %q\n---\n%s", want, prompt)
		}
	}
	// The unbounded known-markets line must not appear when stores are pinned.
	if strings.Contains(prompt, "other local markets are allowed") {
		t.Errorf("pinned prompt leaked the open scope line\n---\n%s", prompt)
	}
}

func TestBuildOffersPrompt_LooseMatch(t *testing.T) {
	prompt := BuildOffersPrompt(nil, nil, nil, domain.OfferNameLoose)
	for _, want := range []string{"LOOSE", `"variety"`, "Hass"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("loose prompt missing %q\n---\n%s", want, prompt)
		}
	}
}

type offerStoreFake struct {
	OfferSearchStore
	created domain.OfferSearch
}

func (f *offerStoreFake) Create(_ context.Context, s domain.OfferSearch) (domain.OfferSearch, error) {
	s.ID = 1
	f.created = s
	return s, nil
}

// The service-side sweeper touches these on every Search; no-op fakes keep
// the queue-only test fixture from needing the embedded interface.
func (f *offerStoreFake) DeleteStale(context.Context, time.Time) ([]string, error) {
	return nil, nil
}

func (f *offerStoreFake) List(context.Context, []domain.OfferSearchStatus, int) ([]domain.OfferSearch, error) {
	return nil, nil
}

type offerProductStoreFake struct {
	ProductStore
}

func (f offerProductStoreFake) GetByID(_ context.Context, id int64) (domain.Product, error) {
	if id == 99 {
		return domain.Product{}, fmt.Errorf("%w: product 99", domain.ErrNotFound)
	}
	return domain.Product{ID: id, Name: "Avocado", Unit: "pc"}, nil
}

type offerSettingsStoreFake struct {
	SettingsStore
}

func (f offerSettingsStoreFake) Get(context.Context, string) (string, error) {
	return `[{"id":"p1","type":"gemini","model":"gemini-2"}]`, nil
}

// An invalid name match is rejected up front, before any store is touched.
func TestSearch_NameMatchValidation(t *testing.T) {
	s := &OfferSearchService{}
	_, err := s.Search(context.Background(), domain.OfferSearchInput{
		Items:     []domain.OfferSearchInputItem{{ProductID: 1}},
		NameMatch: domain.OfferNameMatch("fuzzy"),
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("want validation error, got %v", err)
	}
	for _, want := range []string{"fuzzy", `"strict"`, `"loose"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message missing %q: %s", want, err.Error())
		}
	}
}

// Stores are trimmed, deduped and empty-dropped; an empty name match is
// persisted as the strict default.
func TestSearch_NormalizesStoresAndDefaultsStrict(t *testing.T) {
	store := &offerStoreFake{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := &OfferSearchService{
		searches:      store,
		products:      offerProductStoreFake{},
		providers:     NewSettingsService(offerSettingsStoreFake{}, wrapBox{}, nil),
		log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		queue:         make(chan string, 1),
		ctx:           ctx,
		cancel:        cancel,
		searchTimeout: time.Minute,
	}
	search, err := s.Search(context.Background(), domain.OfferSearchInput{
		Items:  []domain.OfferSearchInputItem{{ProductID: 1}},
		Stores: []string{" REWE ", "", "REWE", "Edeka"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.created.Stores) != 2 || store.created.Stores[0] != "REWE" || store.created.Stores[1] != "Edeka" {
		t.Errorf("stores not normalized: %v", store.created.Stores)
	}
	if store.created.NameMatch != domain.OfferNameStrict {
		t.Errorf("empty name match must default to strict, got %q", store.created.NameMatch)
	}
	if search.NameMatch != domain.OfferNameStrict {
		t.Errorf("returned search: name match %q", search.NameMatch)
	}
}

func TestSearch_RejectsBadItems(t *testing.T) {
	s := &OfferSearchService{
		products: offerProductStoreFake{},
	}
	cases := []struct {
		name    string
		input   domain.OfferSearchInput
		wantMsg string
	}{
		{"empty cart", domain.OfferSearchInput{}, "cart is empty"},
		{"unknown product", domain.OfferSearchInput{Items: []domain.OfferSearchInputItem{{ProductID: 99}}}, "product 99 does not exist"},
		{"duplicate product", domain.OfferSearchInput{Items: []domain.OfferSearchInputItem{{ProductID: 1}, {ProductID: 1}}}, "appears twice"},
		{"bad name match value", domain.OfferSearchInput{Items: []domain.OfferSearchInputItem{{ProductID: 1}}, NameMatch: "nope"}, "unknown name match"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.Search(context.Background(), tt.input)
			if !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("want validation error with %q, got %v", tt.wantMsg, err)
			}
		})
	}
}
