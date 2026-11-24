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

// processSearchSafe guarantees a panic inside one search never kills the
// River worker: the search is marked failed instead, so the UI shows the
// failure and offers a retry.
func (s *OfferSearchService) processSearchSafe(ctx context.Context, token string, log *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("offer search panicked", "token", token, "panic", r, "stack", string(debug.Stack()))
			s.markSearchPanicked(token, r, log)
		}
	}()
	s.processSearch(ctx, token, log)
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
// caller is the River worker. A row still searching after a crash is recovered
// by the queue (running jobs are re-run) or by RecoverSearches on boot.
func (s *OfferSearchService) processSearch(ctx context.Context, token string, log *slog.Logger) {
	// Detached from any HTTP request: the cart may be long gone. The job
	// context is the parent so a shutdown (StopAndCancel) aborts the search.
	parent := ctx

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

// resolveOffersHead returns the offer-search prompt head: the managed
// ai_prompts row when the resolver is wired, the built-in default otherwise
// (tests). A resolve failure degrades to a failed search row via the
// processSearch error path.
func (s *OfferSearchService) resolveOffersHead(ctx context.Context) (string, error) {
	if s.prompts != nil {
		head, err := s.prompts.ResolvePrompt(ctx, domain.PromptKeyOfferSearch)
		if err != nil {
			return "", fmt.Errorf("resolve offer-search prompt: %w", err)
		}
		return head, nil
	}
	return defaultPrompt(domain.PromptKeyOfferSearch), nil
}

// runSearch builds the prompt from the managed offer-search head plus the
// cart snapshot and the user's own market names and runs the AI search.
func (s *OfferSearchService) runSearch(ctx context.Context, provider domain.AIProvider, search domain.OfferSearch, out *domain.OfferResult, log *slog.Logger) error {
	storeNames := []string{}
	if stores, err := s.stores.List(ctx); err == nil {
		for _, st := range stores {
			if st.Name != "" {
				storeNames = append(storeNames, st.Name)
			}
		}
	}
	head, err := s.resolveOffersHead(ctx)
	if err != nil {
		return err
	}
	prompt := BuildOffersPrompt(head, search.Products, storeNames, search.Stores, search.NameMatch)
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
			if offer.PriceCents <= 0 || offer.Availability.Unavailable() {
				// Not-available/not-published rows never compete for best/worst
				// even if a stray price slipped through parsing.
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
