package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/riverqueue/river"

	"home-finance-planner/backend/internal/domain"
)

// River kinds of the deferred intelligence: every accepted purchase (bill
// confirm/update, or a manual transaction with linked products) enqueues one
// payload and the worker does the price-per-unit analysis away from the
// request path. Findings are persisted deterministically; the AI only phrases
// the message (with the auto wording as the always-there fallback).
const (
	BillPPUAnalysisKind        = "bill_ppu_analysis"
	TransactionPPUAnalysisKind = "transaction_ppu_analysis"
)

// BillPPUAnalysisArgs analyzes the price-per-unit history of the linked
// products of one accepted bill.
type BillPPUAnalysisArgs struct{ BillID int64 }

func (BillPPUAnalysisArgs) Kind() string { return BillPPUAnalysisKind }

// TransactionPPUAnalysisArgs is the same analysis for a manual purchase.
type TransactionPPUAnalysisArgs struct{ TransactionID int64 }

func (TransactionPPUAnalysisArgs) Kind() string { return TransactionPPUAnalysisKind }

// Analysis bounds and tolerances (percentages). The alert threshold itself is
// configured via INSIGHT_THRESHOLD_PCT.
const (
	insightHistoryLimit     = 8 // newest purchases evaluated per product
	insightFamilyLimit      = 20
	insightSizeShrinkPct    = 5.0 // a unit_value drop ≥ this counts as a shrink
	insightPriceFlatTolPct  = 2.0 // a unit-price rise ≤ this still reads as "price stayed"
	insightCreepSteps       = 3   // consecutive rises evaluated (4 purchases)
	insightCreepStepMinPct  = 0.5 // a rise below this is flat-row noise, not a "rise"
	insightBulkSizeFactor   = 1.2 // how much bigger a "big" pack must be vs the usual one
	insightCooldown         = 14 * 24 * time.Hour
	maxInsightAICallsPerRun = 12
)

// Consumer-side data contracts (the unit-value worker's pattern): the worker
// depends on narrow interfaces satisfied in main by the repositories, the
// settings/prompt services and the extractor itself.
type (
	// InsightStore persists findings and answers the cooldown check.
	InsightStore interface {
		Create(ctx context.Context, ins domain.ProductInsight) (domain.ProductInsight, error)
		RecentlyInsighted(ctx context.Context, kind domain.ProductInsightKind, productID int64, since time.Time) (bool, error)
	}
	// InsightPurchaseRepo reads the raw saved lines of the just-recorded
	// purchase (deposit/return and sizeless lines are skipped in Go).
	InsightPurchaseRepo interface {
		BillLines(ctx context.Context, billID int64) ([]domain.InsightLine, error)
		TransactionLines(ctx context.Context, txID int64) ([]domain.InsightLine, error)
	}
	// InsightHistoryRepo reads the per-product / per-family PPU history.
	InsightHistoryRepo interface {
		ProductPurchaseHistory(ctx context.Context, productID int64, limit int) ([]domain.ProductHistoryEntry, error)
		FamilyPurchaseHistory(ctx context.Context, genericName string, limit int) ([]domain.ProductHistoryEntry, error)
	}
	// InsightProducts resolves the catalogue row of an analyzed line (display
	// names, and the generic product family for the bulk-buy trigger).
	InsightProducts interface {
		GetByID(ctx context.Context, id int64) (domain.Product, error)
	}
	// InsightProvider resolves the AI connector flagged default_for_bills —
	// the read-connector every prompting background job uses.
	InsightProvider interface {
		DefaultBillProvider(ctx context.Context) (domain.AIProvider, error)
	}
	// InsightPrompts resolves the database-stored prompt text by key.
	InsightPrompts interface {
		ResolvePrompt(ctx context.Context, key string) (string, error)
	}
	// InsightAI runs the prompt-only nudge call (no image, no web tool) —
	// already implemented by *extractor.Extractor.
	InsightAI interface {
		CompleteText(ctx context.Context, provider domain.AIProvider, prompt string) (string, error)
	}
)

// insightFinding is one deterministic trigger hit, before persistence.
type insightFinding struct {
	kind      domain.ProductInsightKind
	productID int64
	name      string
	generic   string
	currency  string
	data      domain.ProductInsightData
	message   string // auto wording; the always-readable fallback
}

// BillPPUAnalysisWorker implements River's Worker interface for bills.
type BillPPUAnalysisWorker struct {
	river.WorkerDefaults[BillPPUAnalysisArgs]
	engine *insightEngine
}

// NewBillPPUAnalysisWorker wires the shared analysis engine.
func NewBillPPUAnalysisWorker(
	store InsightStore,
	history InsightHistoryRepo,
	purchases InsightPurchaseRepo,
	products InsightProducts,
	providers InsightProvider,
	prompts InsightPrompts,
	ai InsightAI,
	thresholdPct float64,
	aiTimeout time.Duration,
	log *slog.Logger,
) *BillPPUAnalysisWorker {
	return &BillPPUAnalysisWorker{engine: newInsightEngine(store, history, purchases, products,
		providers, prompts, ai, thresholdPct, aiTimeout, log)}
}

func (w *BillPPUAnalysisWorker) Work(ctx context.Context, job *river.Job[BillPPUAnalysisArgs]) error {
	return w.engine.analyze(ctx, w.engine.purchases.BillLines, "b", job.Args.BillID,
		fmt.Sprintf("bill %d", job.Args.BillID))
}

// TransactionPPUAnalysisWorker is the bill worker for manual purchases.
type TransactionPPUAnalysisWorker struct {
	river.WorkerDefaults[TransactionPPUAnalysisArgs]
	engine *insightEngine
}

// NewTransactionPPUAnalysisWorker wires the shared analysis engine.
func NewTransactionPPUAnalysisWorker(
	store InsightStore,
	history InsightHistoryRepo,
	purchases InsightPurchaseRepo,
	products InsightProducts,
	providers InsightProvider,
	prompts InsightPrompts,
	ai InsightAI,
	thresholdPct float64,
	aiTimeout time.Duration,
	log *slog.Logger,
) *TransactionPPUAnalysisWorker {
	return &TransactionPPUAnalysisWorker{engine: newInsightEngine(store, history, purchases, products,
		providers, prompts, ai, thresholdPct, aiTimeout, log)}
}

func (w *TransactionPPUAnalysisWorker) Work(ctx context.Context, job *river.Job[TransactionPPUAnalysisArgs]) error {
	return w.engine.analyze(ctx, w.engine.purchases.TransactionLines, "t", job.Args.TransactionID,
		fmt.Sprintf("transaction %d", job.Args.TransactionID))
}

type insightEngine struct {
	store     InsightStore
	history   InsightHistoryRepo
	purchases InsightPurchaseRepo
	products  InsightProducts
	providers InsightProvider
	prompts   InsightPrompts
	ai        InsightAI
	threshold float64
	aiTimeout time.Duration
	log       *slog.Logger
}

func newInsightEngine(
	store InsightStore,
	history InsightHistoryRepo,
	purchases InsightPurchaseRepo,
	products InsightProducts,
	providers InsightProvider,
	prompts InsightPrompts,
	ai InsightAI,
	thresholdPct float64,
	aiTimeout time.Duration,
	log *slog.Logger,
) *insightEngine {
	if log == nil {
		log = slog.Default()
	}
	if aiTimeout <= 0 {
		aiTimeout = 5 * time.Minute
	}
	return &insightEngine{
		store: store, history: history, purchases: purchases, products: products,
		providers: providers, prompts: prompts, ai: ai,
		threshold: thresholdPct, aiTimeout: aiTimeout, log: log,
	}
}

// analyze evaluates one saved purchase: its linked, analyzable lines against
// each product's own history (shrinkflation, price creep) and — when the
// normalization mapping knows a generic family — the family history
// (bulk-buy). Read errors warn and skip the analysis; the worker still
// succeeds, like the other learnings a confirm triggers.
func (e *insightEngine) analyze(ctx context.Context, load func(context.Context, int64) ([]domain.InsightLine, error),
	source string, id int64, label string) error {
	lines, err := load(ctx, id)
	if err != nil {
		return fmt.Errorf("load %s lines: %w", label, err)
	}

	// One line per product is enough to evaluate that product: two lines of
	// the same product in one purchase would duplicate the same history.
	type candidate struct {
		line    domain.InsightLine
		product domain.Product
	}
	analyzable := map[int64]candidate{}
	for _, line := range lines {
		if !insightLineAnalyzable(line) {
			continue
		}
		if _, seen := analyzable[line.ProductID]; seen {
			continue
		}
		p, err := e.products.GetByID(ctx, line.ProductID)
		if err != nil {
			e.log.Debug("insight analysis skipped a line: product unreachable",
				slog.String("purchase", label), slog.Int64("product_id", line.ProductID), slog.Any("error", err))
			continue
		}
		analyzable[line.ProductID] = candidate{line: line, product: p}
	}
	if len(analyzable) == 0 {
		return nil
	}

	var findings []insightFinding
	for _, c := range analyzable {
		hist, err := e.history.ProductPurchaseHistory(ctx, c.line.ProductID, insightHistoryLimit)
		if err != nil {
			e.log.Warn("insight analysis: read product history", slog.Any("error", err))
			continue
		}
		hist = excludePurchase(hist, source, id, c.line.Currency)
		findings = append(findings, evaluateProductTriggers(c.line, c.product, hist, e.threshold)...)
	}
	evaluatedFamily := map[string]bool{}
	for _, c := range analyzable {
		generic := strings.TrimSpace(c.product.GenericName)
		if generic == "" || evaluatedFamily[generic] {
			continue
		}
		fam, err := e.history.FamilyPurchaseHistory(ctx, generic, insightFamilyLimit)
		if err != nil {
			e.log.Warn("insight analysis: read family history", slog.Any("error", err))
			continue
		}
		fam = excludePurchase(fam, source, id, c.line.Currency)
		evaluatedFamily[generic] = true
		if f, hit := evaluateBulkBuy(c.line, c.product, fam, e.threshold); hit {
			findings = append(findings, f)
		}
	}
	if len(findings) == 0 {
		return nil
	}

	e.persist(ctx, findings, label)
	return nil
}

// insightLineAnalyzable repeats the repository's history guards for the
// freshly saved lines (the raw line read is unguarded): linked product, real
// positive spend, known canonical size, and never a deposit artifact.
func insightLineAnalyzable(line domain.InsightLine) bool {
	if line.ProductID == 0 || line.UnitPriceCents <= 0 || line.Currency == "" ||
		domain.IsDepositArtifact(line.Name) {
		return false
	}
	if line.UnitValue == nil || *line.UnitValue <= 0 {
		return false
	}
	_, _, ok := ppuCanonical(line.Unit)
	return ok
}

// excludePurchase drops the analyzed purchase's own rows (they are already in
// the DB when the job runs) and history rows kept in another currency — PPU
// comparisons never mix currencies.
func excludePurchase(entries []domain.ProductHistoryEntry, source string, id int64, currency string) []domain.ProductHistoryEntry {
	out := entries[:0:0]
	for _, en := range entries {
		if en.IsSource(source, id) || en.Currency != currency {
			continue
		}
		out = append(out, en)
	}
	return out
}

// persist phrases each finding with the AI when a reader connector answers
// (the auto wording stays the fallback) and writes the rows inside the
// cooldown rule. Persisting never fails the run — an insight is a nudge, not
// a ledger entry: write errors are logged and the next run re-evaluates.
func (e *insightEngine) persist(ctx context.Context, findings []insightFinding, label string) {
	provider, promptBase, aiReady := e.aiConfigured(ctx)
	callIdx := 0
	cutoff := time.Now().Add(-insightCooldown)
	for i := range findings {
		f := &findings[i]
		source := "auto"
		if aiReady && callIdx < maxInsightAICallsPerRun {
			if msg := e.phrase(ctx, provider, promptBase, *f); msg != "" {
				f.message = msg
				source = "ai"
			}
			callIdx++
		}
		ins := domain.ProductInsight{
			Kind:        f.kind,
			ProductID:   &f.productID,
			ProductName: f.name,
			GenericName: f.generic,
			Currency:    f.currency,
			Message:     f.message,
			Source:      source,
			Data:        f.data,
		}
		cooling, err := e.store.RecentlyInsighted(ctx, f.kind, f.productID, cutoff)
		if err != nil {
			e.log.Warn("insight cooldown check", slog.Any("error", err))
		}
		if cooling {
			e.log.Debug("insight suppressed by cooldown", slog.String("purchase", label),
				slog.String("kind", string(f.kind)), slog.Int64("product_id", f.productID))
			continue
		}
		if _, err := e.store.Create(ctx, ins); err != nil {
			e.log.Warn("save insight", slog.Any("error", err))
			continue
		}
		e.log.Info("insight saved", slog.String("purchase", label), slog.String("kind", string(f.kind)),
			slog.Int64("product_id", f.productID), slog.String("source", source))
	}
}

// aiConfigured resolves the default bill-read connector and prompt; either
// failing means "phrase nothing" — the auto wording is kept.
func (e *insightEngine) aiConfigured(ctx context.Context) (domain.AIProvider, string, bool) {
	provider, err := e.providers.DefaultBillProvider(ctx)
	if err != nil {
		return domain.AIProvider{}, "", false
	}
	prompt, err := e.prompts.ResolvePrompt(ctx, domain.PromptKeyProductInsights)
	if err != nil || strings.TrimSpace(prompt) == "" {
		return domain.AIProvider{}, "", false
	}
	return provider, prompt, true
}

// phrase runs one prompt-only call and returns the cleaned nudge text — ""
// keeps the auto wording.
func (e *insightEngine) phrase(ctx context.Context, provider domain.AIProvider, promptBase string, f insightFinding) string {
	callCtx, cancel := context.WithTimeout(ctx, e.aiTimeout)
	defer cancel()
	raw, err := e.ai.CompleteText(callCtx, provider, promptBase+"\n\nFinding:\n"+insightFactsJSON(f))
	if err != nil {
		e.log.Debug("insight phrasing failed — keeping the auto wording", slog.Any("error", err))
		return ""
	}
	return cleanInsightReply(raw)
}

// insightFactsJSON renders the finding as the JSON the prompt documents —
// human units and prices, never formulas.
func insightFactsJSON(f insightFinding) string {
	facts := struct {
		Kind         string                          `json:"kind"`
		Product      string                          `json:"product"`
		GenericName  string                          `json:"generic_name,omitempty"`
		Currency     string                          `json:"currency"`
		Unit         string                          `json:"unit"`
		Threshold    float64                         `json:"threshold_pct"`
		NewUnitPrice *int64                          `json:"new_unit_price,omitempty"`
		OldUnitPrice *int64                          `json:"old_unit_price,omitempty"`
		NewUnitValue *float64                        `json:"new_unit_value,omitempty"`
		OldUnitValue *float64                        `json:"old_unit_value,omitempty"`
		NewPPU       *float64                        `json:"new_price_per_unit,omitempty"`
		OldPPU       *float64                        `json:"old_price_per_unit,omitempty"`
		ChangePct    float64                         `json:"change_pct"`
		Purchases    []domain.ProductInsightPurchase `json:"purchases,omitempty"`
	}{
		Kind:         string(f.kind),
		Product:      f.name,
		GenericName:  f.generic,
		Currency:     f.currency,
		Unit:         f.data.Unit,
		Threshold:    f.data.Threshold,
		NewUnitPrice: f.data.NewPriceCents,
		OldUnitPrice: f.data.OldPriceCents,
		NewUnitValue: f.data.NewUnitValue,
		OldUnitValue: f.data.OldUnitValue,
		NewPPU:       f.data.NewPPU,
		OldPPU:       f.data.OldPPU,
		ChangePct:    f.data.ChangePct,
		Purchases:    f.data.Purchases,
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// cleanInsightReply strips the scaffolding text-completion models like to
// add (quotes, code fences, "Here is the nudge:").
func cleanInsightReply(raw string) string {
	s := strings.TrimSpace(raw)
	for _, fence := range []string{"```"} {
		if strings.HasPrefix(s, fence) {
			s = strings.TrimPrefix(s, fence)
			s = strings.TrimPrefix(s, "text")
		}
		if strings.HasSuffix(s, fence) {
			s = strings.TrimSuffix(s, fence)
		}
	}
	s = strings.TrimSpace(s)
	// Drop an optional intro sentence before the first newline? No — only
	// strip wrapping quotes and hard-cap the length; everything else is text
	// the model may legitimately have written.
	for _, q := range []string{"\"", "„", "«", "‘", "“"} {
		if strings.HasPrefix(s, q) && strings.HasSuffix(s, q) && len(s) > len(q)*2 {
			s = strings.TrimSpace(s[len(q) : len(s)-len(q)])
		}
	}
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}
