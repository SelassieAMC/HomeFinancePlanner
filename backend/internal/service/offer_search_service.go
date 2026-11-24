package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// OfferSearcher abstracts the AI search engine (implemented by
// internal/extractor). log receives the per-call tracing the worker includes
// in its own logs.
type OfferSearcher interface {
	SearchOffers(ctx context.Context, provider domain.AIProvider, prompt string, log *slog.Logger) (domain.OfferResult, error)
}

// The search runs detached from any HTTP request: the DB row is the source of
// truth, and the River queue (via SetJobEnqueuer) is only a "please look at
// this" hint.
const (
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
	prompts       PromptResolver
	searcher      OfferSearcher
	searchTimeout time.Duration
	// jobs enqueues the search onto the River queue (SetJobEnqueuer). nil falls
	// back to an in-process goroutine so the feature still works without the
	// queue (tests, or a boot where River failed to build).
	jobs JobEnqueuer
	log  *slog.Logger
}

// NewOfferSearchService wires the offer-search workflow. llmTimeout is the
// configured LLM_TIMEOUT; the effective search timeout is a multiple of it.
// The River queue is attached separately with SetJobEnqueuer (the service is
// constructed before the job manager so its worker can be registered).
func NewOfferSearchService(
	searches OfferSearchStore,
	products ProductStore,
	stores StoreStore,
	providers *SettingsService,
	prompts PromptResolver,
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
	return &OfferSearchService{
		searches:      searches,
		products:      products,
		stores:        stores,
		providers:     providers,
		prompts:       prompts,
		searcher:      searcher,
		searchTimeout: llmTimeout * offerSearchTimeoutFactor,
		log:           log,
	}
}

// SetJobEnqueuer wires the River queue enqueuer (main does this after the job
// manager is built). nil leaves the in-process fallback in charge.
func (s *OfferSearchService) SetJobEnqueuer(j JobEnqueuer) { s.jobs = j }

// RecoverSearches re-enqueues searches still in the searching state after a
// restart (a clean shutdown cancels the running River job but leaves the row
// searching). Called by main once the queue is wired; idempotent via River's
// unique-by-args dedup.
func (s *OfferSearchService) RecoverSearches(ctx context.Context) {
	if s.jobs == nil {
		return
	}
	pending, err := s.searches.List(ctx, []domain.OfferSearchStatus{domain.OfferSearchSearching}, 200)
	if err != nil {
		s.log.Error("recover searching offer searches", "error", err)
		return
	}
	for _, search := range pending {
		if err := s.jobs.EnqueueOfferSearch(ctx, search.SearchToken); err != nil {
			s.log.Warn("re-enqueue searching offer search", "token", search.SearchToken, "error", err)
		}
	}
	if len(pending) > 0 {
		s.log.Info("recovered searching offer searches", "count", len(pending))
	}
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

	provider, err := s.providers.DefaultSearchProvider(ctx)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.OfferSearch{}, validationError("no AI provider is configured — add a connector and mark it default for web search in Settings")
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
	if s.jobs == nil {
		// No queue (tests, or a boot where River failed): run the search
		// in-process so the feature still works.
		go s.processSearchSafe(context.Background(), token, s.log)
	} else if err := s.jobs.EnqueueOfferSearch(ctx, token); err != nil {
		// The row would stay "searching" with no worker behind it — fail it so
		// the user can retry instead of leaving it stranded.
		s.searches.MarkFailed(context.Background(), token, "offer search could not be queued: "+err.Error())
		s.log.Warn("offer search enqueue failed", "token", token, "error", err)
		return created, nil
	}
	s.deleteStaleSearches(ctx)
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
	if s.jobs == nil {
		go s.processSearchSafe(context.Background(), token, s.log)
	} else if err := s.jobs.EnqueueOfferSearch(ctx, token); err != nil {
		// Fail the row rather than leave it stranded with no worker.
		s.searches.MarkFailed(context.Background(), token, "offer search could not be queued: "+err.Error())
		s.log.Warn("offer search retry enqueue failed", "token", token, "error", err)
	}
	return domain.OfferSearch{
		SearchToken: token,
		Status:      domain.OfferSearchSearching,
		ProviderID:  provider.ID,
	}, nil
}

// resolveProvider picks the pinned provider or falls back to the connector
// flagged as the default for web search (any configured one when none is).
func (s *OfferSearchService) resolveProvider(ctx context.Context, providerID string) (domain.AIProvider, error) {
	if providerID == "" {
		return s.providers.DefaultSearchProvider(ctx)
	}
	return s.providers.GetProvider(ctx, providerID)
}

// Delete removes a search row (the persisted result goes with it).
func (s *OfferSearchService) Delete(ctx context.Context, token string) error {
	return s.searches.Delete(ctx, token)
}

// deleteStaleSearches opportunistically removes failed searches past the TTL
// (done rows are the kept record). It runs inline on a new search — the
// periodic sweeper is gone with the worker pool.
func (s *OfferSearchService) deleteStaleSearches(ctx context.Context) {
	if _, err := s.searches.DeleteStale(ctx, time.Now().Add(-offerFailedSearchTTL)); err != nil {
		s.log.Warn("delete stale offer searches", "error", err)
	}
}
