package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// OfferSearcher abstracts the AI search engine (implemented by
// internal/extractor). log receives the per-call tracing the worker includes
// in its own logs.
type OfferSearcher interface {
	SearchOffers(ctx context.Context, provider domain.AIProvider, prompt string, log *slog.Logger) (domain.OfferResult, error)
}

// The search workers drain the token queue and run AI searches detached from
// any HTTP request, mirroring the bill-scan pipeline: the DB row is the source
// of truth, the queue is only a "please look at this" hint.
const (
	offerSearchQueueCapacity = 64
	offerSearchWorkers       = 2
	// offerFailedSearchTTL bounds failed searches; done rows are the kept
	// record and are never swept.
	offerFailedSearchTTL = 7 * 24 * time.Hour
	// Grounded web searches round-trip the search tool several times and run
	// slower than one receipt scan, so the LLM_TIMEOUT budget is doubled.
	offerSearchTimeoutFactor = 2
	maxOfferSearchItems      = 20
)

// OfferSearchService runs the purchase-cart pipeline: a confirmed cart is
// snapshotted into a persisted search row and an AI connector looks for
// current offers of those products in the user's local markets.
type OfferSearchService struct {
	searches      OfferSearchStore
	products      ProductStore
	stores        StoreStore
	providers     *SettingsService
	searcher      OfferSearcher
	searchTimeout time.Duration
	log           *slog.Logger

	queue  chan string
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex // guards lastSweep only
	lastSweep time.Time
}

// NewOfferSearchService wires the offer-search workflow and starts the
// background search workers. llmTimeout is the configured LLM_TIMEOUT; the
// effective search timeout is a multiple of it. Rows still searching after a
// restart are re-enqueued.
func NewOfferSearchService(
	searches OfferSearchStore,
	products ProductStore,
	stores StoreStore,
	providers *SettingsService,
	searcher OfferSearcher,
	llmTimeout time.Duration,
	log *slog.Logger,
) *OfferSearchService {
	if llmTimeout <= 0 {
		llmTimeout = 5 * time.Minute
	}
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &OfferSearchService{
		searches:      searches,
		products:      products,
		stores:        stores,
		providers:     providers,
		searcher:      searcher,
		searchTimeout: llmTimeout * offerSearchTimeoutFactor,
		log:           log,
		queue:         make(chan string, offerSearchQueueCapacity),
		ctx:           ctx,
		cancel:        cancel,
	}
	s.recoverSearches(ctx)
	for i := 1; i <= offerSearchWorkers; i++ {
		s.wg.Add(1)
		go s.runWorker(ctx, i)
	}
	s.wg.Add(1)
	go s.runSweeper(ctx)
	return s
}

// Search snapshots the cart lines and enqueues an offer search. It returns
// immediately (status searching); the client polls Get until the status
// leaves "searching".
func (s *OfferSearchService) Search(ctx context.Context, in domain.OfferSearchInput) (domain.OfferSearch, error) {
	if len(in.Items) == 0 {
		return domain.OfferSearch{}, validationError("the cart is empty — add products before searching for offers")
	}
	if len(in.Items) > maxOfferSearchItems {
		return domain.OfferSearch{}, validationError("too many cart items (%d) — search for at most %d products at once", len(in.Items), maxOfferSearchItems)
	}

	// Search scope: pinned stores and name-match mode ride along with the
	// snapshot (request_json) so the result renders the scope and retries
	// reuse it.
	nameMatch := in.NameMatch
	if nameMatch == "" {
		nameMatch = domain.OfferNameStrict
	}
	if !nameMatch.Valid() {
		return domain.OfferSearch{}, validationError("unknown name match %q — want \"strict\" or \"loose\"", in.NameMatch)
	}
	pinnedStores := make([]string, 0, len(in.Stores))
	seenStores := map[string]bool{}
	for _, st := range in.Stores {
		name := strings.TrimSpace(st)
		if name == "" || seenStores[name] {
			continue
		}
		seenStores[name] = true
		pinnedStores = append(pinnedStores, name)
	}
	if len(pinnedStores) > maxOfferSearchItems {
		return domain.OfferSearch{}, validationError("too many pinned stores (%d) — pin at most %d stores", len(pinnedStores), maxOfferSearchItems)
	}

	snapshot := make([]domain.OfferSearchProduct, 0, len(in.Items))
	seen := map[int64]bool{}
	for _, item := range in.Items {
		if item.ProductID <= 0 {
			return domain.OfferSearch{}, validationError("each cart item needs a product id")
		}
		if seen[item.ProductID] {
			return domain.OfferSearch{}, validationError("product %d appears twice in the cart", item.ProductID)
		}
		seen[item.ProductID] = true
		product, err := s.products.GetByID(ctx, item.ProductID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return domain.OfferSearch{}, validationError("product %d does not exist", item.ProductID)
			}
			return domain.OfferSearch{}, fmt.Errorf("load product %d: %w", item.ProductID, err)
		}
		quantity := 1.0
		if item.Quantity > 0 {
			quantity = item.Quantity
		}
		line := domain.OfferSearchProduct{
			ProductID: product.ID,
			Name:      product.Name,
			Unit:      product.Unit,
			Quantity:  quantity,
		}
		if item.Brand != "" {
			line.Brand = item.Brand
		} else {
			line.Brand = product.Brand
		}
		if product.BestPriceCents != nil && product.PriceCurrency != "" {
			line.LastPriceCents = product.BestPriceCents
			line.Currency = product.PriceCurrency
		}
		snapshot = append(snapshot, line)
	}

	provider, err := s.providers.DefaultProvider(ctx)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.OfferSearch{}, validationError("no AI provider is configured — add and mark a default connector in Settings")
		}
		return domain.OfferSearch{}, fmt.Errorf("resolve AI provider: %w", err)
	}

	token, err := newScanToken()
	if err != nil {
		return domain.OfferSearch{}, err
	}
	created, err := s.searches.Create(ctx, domain.OfferSearch{
		SearchToken: token,
		ProviderID:  provider.ID,
		Products:    snapshot,
		Stores:      pinnedStores,
		NameMatch:   nameMatch,
	})
	if err != nil {
		return domain.OfferSearch{}, err
	}
	s.log.Info("offer search created",
		"token", token, "provider", provider.Model, "family", provider.Type,
		"products", len(snapshot))
	if !s.enqueue(token) {
		// Queue full: the row stays searching and the sweeper re-enqueues it
		// within minutes — but say so, the search is not immediate.
		s.log.Warn("offer search dropped by full queue", "token", token)
	}
	s.sweepStaleSearches()
	return created, nil
}

// Get returns one search row by token (polled while searching).
func (s *OfferSearchService) Get(ctx context.Context, token string) (domain.OfferSearch, error) {
	return s.searches.GetByToken(ctx, token)
}

// ListSearches returns recent searches (default 50) in the given states — all
// states when none is given.
func (s *OfferSearchService) ListSearches(ctx context.Context, statuses []domain.OfferSearchStatus, limit int) ([]domain.OfferSearch, error) {
	for _, st := range statuses {
		if !st.Valid() {
			return nil, validationError("unknown search status %q", st)
		}
	}
	return s.searches.List(ctx, statuses, limit)
}

// Retry re-runs the search, optionally with a different provider. The row
// returns to the searching state and the client polls again.
func (s *OfferSearchService) Retry(ctx context.Context, token, providerID string) (domain.OfferSearch, error) {
	provider, err := s.resolveProvider(ctx, providerID)
	if err != nil {
		return domain.OfferSearch{}, err
	}
	claimed, err := s.searches.ClaimRetry(ctx, token, provider.ID)
	if err != nil {
		return domain.OfferSearch{}, err
	}
	if !claimed {
		// Either the token is unknown or the search is still running.
		if _, gerr := s.searches.GetByToken(ctx, token); gerr != nil {
			return domain.OfferSearch{}, fmt.Errorf("offer search %s not found or expired: %w", token, domain.ErrNotFound)
		}
		return domain.OfferSearch{}, validationError("offer search is currently running — wait for it to finish")
	}
	if !s.enqueue(token) {
		// Queue full: the row stays searching and the sweeper re-enqueues it.
		s.log.Warn("offer search retry dropped by full queue", "token", token)
	}
	return domain.OfferSearch{
		SearchToken: token,
		Status:      domain.OfferSearchSearching,
		ProviderID:  provider.ID,
	}, nil
}

// resolveProvider picks the pinned provider or falls back to the default one.
func (s *OfferSearchService) resolveProvider(ctx context.Context, providerID string) (domain.AIProvider, error) {
	if providerID == "" {
		return s.providers.DefaultProvider(ctx)
	}
	return s.providers.GetProvider(ctx, providerID)
}

// Delete removes a search row (the persisted result goes with it).
func (s *OfferSearchService) Delete(ctx context.Context, token string) error {
	return s.searches.Delete(ctx, token)
}

// Close stops the search workers. It cancels the pool and waits at most the
// shutdown drain window for workers between iterations — it never waits for a
// running search; the row stays searching and is recovered on next boot.
func (s *OfferSearchService) Close() {
	s.cancel()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(shutdownDrainWindow):
	}
}
