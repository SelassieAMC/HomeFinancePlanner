package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"home-finance-planner/backend/internal/domain"
)

func (s *OfferSearchService) runWorker(ctx context.Context, n int) {
	defer s.wg.Done()
	log := s.log.With("component", "offer_search_worker", "worker", n)
	for {
		select {
		case <-ctx.Done():
			return
		case token := <-s.queue:
			s.processSearchSafe(token, log)
		}
	}
}

// processSearchSafe guarantees a panic inside one search never kills its
// worker: the pool would silently shrink and later searches would sit in
// "searching" forever with nothing re-running them. The search is marked
// failed instead, so the UI shows the failure and offers a retry.
func (s *OfferSearchService) processSearchSafe(token string, log *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("offer search panicked", "token", token, "panic", r, "stack", string(debug.Stack()))
			s.markSearchPanicked(token, r, log)
		}
	}()
	s.processSearch(token, log)
}

// markSearchPanicked best-effort records the panic on the search row. It must
// never panic in turn — that would defeat processSearchSafe's recovery.
func (s *OfferSearchService) markSearchPanicked(token string, r any, log *slog.Logger) {
	defer func() { _ = recover() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.searches.MarkFailed(ctx, token, fmt.Sprintf("internal error during offer search: %v", r)); err != nil {
		log.Warn("mark panicked offer search as failed", "token", token, "error", err)
	}
}

// processSearch runs one offer search and persists the result. All errors land
// in the search row (status failed) — nothing is propagated, because the
// caller is a background goroutine. A row still searching after a lost write
// is recovered by the sweeper.
func (s *OfferSearchService) processSearch(token string, log *slog.Logger) {
	// Detached from any HTTP request: the cart may be long gone.
	parent := context.Background()

	search, err := s.searches.GetByToken(parent, token)
	if err != nil || search.Status != domain.OfferSearchSearching {
		// Deleted or retried meanwhile — nothing to do.
		log.Debug("skip vanished offer search", "token", token)
		return
	}

	searchCtx, cancel := context.WithTimeout(parent, s.searchTimeout)
	defer cancel()

	searchLog := log.With("token", token)
	start := time.Now()
	provider, providerErr := s.providers.GetProvider(searchCtx, search.ProviderID)
	var result domain.OfferResult
	var searchErr error
	if providerErr == nil {
		searchLog.Info("offer search started",
			"provider", provider.Model, "family", provider.Type,
			"products", len(search.Products), "timeout", s.searchTimeout)
		searchErr = s.runSearch(searchCtx, provider, search, &result, searchLog)
	}

	// The result write uses a fresh background context so a finished result
	// survives a shutdown that cancelled the worker pool.
	writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer writeCancel()

	switch {
	case providerErr != nil:
		searchLog.Error("offer search failed to resolve provider",
			"provider_id", search.ProviderID, "duration", time.Since(start), "err", providerErr)
		s.persistSearchResult(writeCtx, token, log, nil, "AI provider unavailable: "+providerErr.Error())
	case searchErr != nil:
		searchLog.Warn("offer search finished with error",
			"duration", time.Since(start), "err", searchErr)
		s.persistSearchResult(writeCtx, token, log, nil, describeSearchError(searchErr, provider, s.searchTimeout))
	default:
		searchLog.Info("offer search succeeded",
			"duration", time.Since(start), "products", len(result.Products))
		markBestWorst(&result)
		result.SearchedAt = time.Now().UTC().Format(time.RFC3339)
		s.persistSearchResult(writeCtx, token, log, &result, "")
	}
}

// runSearch builds the prompt from the cart snapshot plus the user's own
// market names and runs the AI search.
func (s *OfferSearchService) runSearch(ctx context.Context, provider domain.AIProvider, search domain.OfferSearch, out *domain.OfferResult, log *slog.Logger) error {
	storeNames := []string{}
	if stores, err := s.stores.List(ctx); err == nil {
		for _, st := range stores {
			if st.Name != "" {
				storeNames = append(storeNames, st.Name)
			}
		}
	}
	prompt := BuildOffersPrompt(search.Products, storeNames)
	res, err := s.searcher.SearchOffers(ctx, provider, prompt, log)
	// The wire echoes product ids from the request; keep the snapshot's names
	// authoritative so renamed products still render the result correctly.
	for i := range res.Products {
		for _, line := range search.Products {
			if line.ProductID == res.Products[i].ProductID {
				res.Products[i].Name = line.Name
				if res.Products[i].Brand == "" {
					res.Products[i].Brand = line.Brand
				}
				break
			}
		}
	}
	*out = res
	return err
}

// describeSearchError turns raw search errors into actionable messages. The
// cannot-search case names the configured model and the connector families
// that can search the web; timeouts note that grounded searches are slower.
func describeSearchError(err error, provider domain.AIProvider, timeout time.Duration) string {
	detail := cannotSearchDetail(err)
	if detail != "" {
		return fmt.Sprintf(
			"the configured model %q (type %s) cannot search the web — %s. Configure a connector with native web search in Settings (Gemini with google_search, Anthropic with web_search, an OpenAI search model, or an Ollama web-search connector with an API key) and retry",
			provider.Model, provider.Type, detail,
		)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf(
			"the offer search timed out after %s — grounded web searches are slower than receipt scans; retry, or configure a faster connector",
			timeout,
		)
	}
	return err.Error()
}

// cannotSearchDetail returns the reason text when err wraps
// domain.ErrCannotSearch, else "".
func cannotSearchDetail(err error) string {
	if !errors.Is(err, domain.ErrCannotSearch) {
		return ""
	}
	detail := strings.TrimSpace(strings.TrimPrefix(err.Error(), domain.ErrCannotSearch.Error()+": "))
	if detail == "" {
		detail = "the model reported it cannot browse the web"
	}
	return detail
}

// persistSearchResult writes done/failed to the search row, logging (not
// propagating) a failure — the row stays searching and is re-enqueued by the
// sweeper.
func (s *OfferSearchService) persistSearchResult(ctx context.Context, token string, log *slog.Logger, result *domain.OfferResult, failure string) {
	var err error
	if result != nil {
		err = s.searches.MarkDone(ctx, token, result)
	} else {
		err = s.searches.MarkFailed(ctx, token, failure)
	}
	if err != nil {
		log.Error("persist offer search result", "token", token, "error", err)
	}
}

// recoverSearches re-enqueues searches still in the searching state — a
// restart or crash mid-search leaves them there and they resume on boot.
func (s *OfferSearchService) recoverSearches(ctx context.Context) {
	pending, err := s.searches.List(ctx, []domain.OfferSearchStatus{domain.OfferSearchSearching}, offerSearchQueueCapacity)
	if err != nil {
		s.log.Error("recover searching offer searches", "error", err)
		return
	}
	for _, search := range pending {
		if !s.enqueue(search.SearchToken) {
			s.log.Warn("recovery queue full", "token", search.SearchToken)
		}
	}
	if len(pending) > 0 {
		s.log.Info("recovered searching offer searches", "count", len(pending))
	}
}

// runSweeper periodically runs the stale-search maintenance so recovery does
// not depend on someone confirming a new cart: a stranded "searching" row
// (lost enqueue, lost worker) is re-enqueued even on a quiet instance.
func (s *OfferSearchService) runSweeper(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepStaleSearches()
		}
	}
}

// sweepStaleSearches lazily (at most once a minute) deletes failed searches
// past the TTL and re-enqueues searching searches that stopped making
// progress (e.g. a queue-full enqueue or a lost worker).
func (s *OfferSearchService) sweepStaleSearches() {
	s.mu.Lock()
	if time.Since(s.lastSweep) <= time.Minute {
		s.mu.Unlock()
		return
	}
	s.lastSweep = time.Now()
	s.mu.Unlock()

	cutoff := time.Now().Add(-offerFailedSearchTTL)
	if _, err := s.searches.DeleteStale(s.ctx, cutoff); err != nil {
		s.log.Error("sweep stale offer searches", "error", err)
	}

	// A stranded searching row gets re-enqueued once it is older than two
	// search timeouts (never while an in-flight search can still be running).
	staleBefore := time.Now().Add(-2 * s.searchTimeout)
	if stale, err := s.searches.List(s.ctx, []domain.OfferSearchStatus{domain.OfferSearchSearching}, offerSearchQueueCapacity); err == nil {
		for _, search := range stale {
			if search.UpdatedAt.Before(staleBefore) {
				if !s.enqueue(search.SearchToken) {
					break // queue still full — next sweep retries
				}
			}
		}
	}
}

// enqueue hands a token to the workers without ever blocking: a full queue
// leaves the row in the DB for the next sweep to re-enqueue.
func (s *OfferSearchService) enqueue(token string) bool {
	select {
	case s.queue <- token:
		return true
	default:
		return false
	}
}

// markBestWorst flags the cheapest and the most expensive offer per product —
// within one currency group (comparing across currencies would be wrong).
// Only positive prices count; all tied rows are flagged and single-row groups
// get neither flag.
func markBestWorst(res *domain.OfferResult) {
	if res == nil {
		return
	}
	for i := range res.Products {
		product := &res.Products[i]
		byCurrency := map[string][]int{}
		for j, offer := range product.Offers {
			if offer.PriceCents <= 0 {
				continue
			}
			byCurrency[offer.Currency] = append(byCurrency[offer.Currency], j)
		}
		for _, idx := range byCurrency {
			if len(idx) < 2 {
				continue
			}
			min, max := idx[0], idx[0]
			for _, j := range idx {
				if product.Offers[j].PriceCents < product.Offers[min].PriceCents {
					min = j
				}
				if product.Offers[j].PriceCents > product.Offers[max].PriceCents {
					max = j
				}
			}
			best := product.Offers[min].PriceCents
			worst := product.Offers[max].PriceCents
			for _, j := range idx {
				switch product.Offers[j].PriceCents {
				case best:
					// When all prices are equal (best == worst) every row is a
					// cheapest offer; flag it as best only, never as worst too.
					product.Offers[j].BestPrice = true
				case worst:
					product.Offers[j].WorstPrice = true
				}
			}
		}
	}
}
