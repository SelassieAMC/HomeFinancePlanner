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
	"home-finance-planner/backend/internal/fx"
)

// TransactionCategoryAnalysisKind is the River kind of the anomaly half of
// the deferred intelligence: every saved manual expense transaction with a
// category enqueues one payload, and the worker checks whether that
// category's spend in the transaction's own month came out clearly above the
// average of the preceding months (through the same day-of-month). The
// trigger is deterministic; the AI only phrases the message.
const TransactionCategoryAnalysisKind = "transaction_category_analysis"

// TransactionCategoryAnalysisArgs analyzes the month-vs-average spend of the
// category of one saved manual expense transaction.
type TransactionCategoryAnalysisArgs struct{ TransactionID int64 }

func (TransactionCategoryAnalysisArgs) Kind() string { return TransactionCategoryAnalysisKind }

// Anomaly bounds and guards. The rise threshold and the baseline floor are
// configured via ANOMALY_RISE_PCT / ANOMALY_MIN_BASELINE_CENTS; the rest is
// policy: three months of baseline, at least two of them carrying spend.
const (
	anomalyHistoryMonths = 3 // preceding months forming the baseline
	anomalyMinDataMonths = 2 // prior months with spend required before a baseline counts
	anomalyCooldown      = insightCooldown
)

// Consumer-side contracts (the insight engine's pattern): narrow interfaces
// the worker depends on, satisfied in main by the repositories and services.
type (
	// AnomalyStore persists category findings and answers the cooldown check.
	AnomalyStore interface {
		Create(ctx context.Context, ins domain.ProductInsight) (domain.ProductInsight, error)
		RecentlyInsightedCategory(ctx context.Context, kind domain.ProductInsightKind, categoryID int64, month string, since time.Time) (bool, error)
	}
	// AnomalyTransactions resolves the saved transaction (kind, category, date).
	AnomalyTransactions interface {
		GetByID(ctx context.Context, id int64) (domain.Transaction, error)
	}
	// AnomalyCategories resolves the category's display name.
	AnomalyCategories interface {
		GetByID(ctx context.Context, id int64) (domain.Category, error)
	}
	// AnomalySpendSource reads the category's day-by-day expense-transaction
	// spend since a date (per native currency).
	AnomalySpendSource interface {
		CategoryDaySpend(ctx context.Context, categoryID int64, from string) ([]domain.CategoryDayCurrencyTotal, error)
	}
	// AnomalyRates supplies the FX snapshot the base-currency conversion uses.
	AnomalyRates interface {
		Snapshot(ctx context.Context) (domain.RateSnapshot, error)
	}
	// AnomalySettings resolves the user's base display currency.
	AnomalySettings interface {
		BaseCurrency(ctx context.Context) (string, error)
	}
)

// TransactionCategoryAnalysisWorker implements River's Worker interface.
type TransactionCategoryAnalysisWorker struct {
	river.WorkerDefaults[TransactionCategoryAnalysisArgs]
	engine *categoryAnomalyEngine
}

// NewTransactionCategoryAnalysisWorker wires the shared anomaly engine.
func NewTransactionCategoryAnalysisWorker(
	store AnomalyStore,
	transactions AnomalyTransactions,
	categories AnomalyCategories,
	spend AnomalySpendSource,
	settings AnomalySettings,
	rates AnomalyRates,
	providers InsightProvider,
	prompts InsightPrompts,
	ai InsightAI,
	risePct float64,
	minBaselineCents int64,
	aiTimeout time.Duration,
	log *slog.Logger,
) *TransactionCategoryAnalysisWorker {
	return &TransactionCategoryAnalysisWorker{engine: newCategoryAnomalyEngine(store, transactions,
		categories, spend, settings, rates, providers, prompts, ai, risePct, minBaselineCents, aiTimeout, log)}
}

func (w *TransactionCategoryAnalysisWorker) Work(ctx context.Context, job *river.Job[TransactionCategoryAnalysisArgs]) error {
	return w.engine.analyzeTransaction(ctx, job.Args.TransactionID)
}

type categoryAnomalyEngine struct {
	store        AnomalyStore
	transactions AnomalyTransactions
	categories   AnomalyCategories
	spend        AnomalySpendSource
	settings     AnomalySettings
	rates        AnomalyRates
	providers    InsightProvider
	prompts      InsightPrompts
	ai           InsightAI
	risePct      float64
	minBaseline  int64
	aiTimeout    time.Duration
	log          *slog.Logger
}

func newCategoryAnomalyEngine(
	store AnomalyStore,
	transactions AnomalyTransactions,
	categories AnomalyCategories,
	spend AnomalySpendSource,
	settings AnomalySettings,
	rates AnomalyRates,
	providers InsightProvider,
	prompts InsightPrompts,
	ai InsightAI,
	risePct float64,
	minBaselineCents int64,
	aiTimeout time.Duration,
	log *slog.Logger,
) *categoryAnomalyEngine {
	if log == nil {
		log = slog.Default()
	}
	if aiTimeout <= 0 {
		aiTimeout = 5 * time.Minute
	}
	if risePct <= 0 {
		risePct = 30
	}
	if minBaselineCents <= 0 {
		minBaselineCents = 2000
	}
	return &categoryAnomalyEngine{
		store: store, transactions: transactions, categories: categories, spend: spend,
		settings: settings, rates: rates, providers: providers, prompts: prompts, ai: ai,
		risePct: risePct, minBaseline: minBaselineCents, aiTimeout: aiTimeout, log: log,
	}
}

// analyzeTransaction evaluates one saved manual expense transaction: its
// category's spend in the transaction's own month against the average of the
// preceding months. Non-expense rows, category-less rows and read errors exit
// quietly — the check is a nudge, never a failure of the save that triggered
// it.
func (e *categoryAnomalyEngine) analyzeTransaction(ctx context.Context, txID int64) error {
	tx, err := e.transactions.GetByID(ctx, txID)
	if err != nil {
		return fmt.Errorf("load transaction %d for category analysis: %w", txID, err)
	}
	if tx.Kind != domain.TransactionExpense || tx.CategoryID == nil {
		return nil
	}
	today := time.Now().UTC()
	e.analyzeCategory(ctx, *tx.CategoryID, tx.Date, today)
	return nil
}

// analyzeCategory compares throughDay (the triggering transaction's date,
// "YYYY-MM-DD") for its month against the anomalyHistoryMonths preceding
// months. A fully past month compares as a complete month (full spans on
// both sides); a month still in progress compares month-to-date against the
// preceding months through the same day-of-month — the fair partial-month
// basis a flat month-to-date-vs-full-average comparison would never give.
func (e *categoryAnomalyEngine) analyzeCategory(ctx context.Context, categoryID int64, date string, today time.Time) {
	evaluateMonth := monthOf(date)

	base, err := e.settings.BaseCurrency(ctx)
	if err != nil {
		e.log.Warn("category anomaly: resolve base currency", slog.Any("error", err))
		return
	}
	snap, err := e.rates.Snapshot(ctx)
	if err != nil {
		e.log.Warn("category anomaly: read rate snapshot", slog.Any("error", err))
		return
	}

	from := firstOfMonthAdd(evaluateMonth, -anomalyHistoryMonths)
	rows, err := e.spend.CategoryDaySpend(ctx, categoryID, from.Format("2006-01-02"))
	if err != nil {
		e.log.Warn("category anomaly: read category spend", slog.Any("error", err))
		return
	}

	conv := &fxConverter{base: base, snap: snap}
	evalMonthEnd := monthEndOf(evaluateMonth, today) // "" = still in progress
	monthly := foldCategoryDays(rows, conv, evaluateMonth, evalMonthEnd, today)

	prior := make([]domain.ProductInsightMonth, 0, anomalyHistoryMonths)
	baselineSum, baselineMonths := int64(0), 0
	for i := 1; i <= anomalyHistoryMonths; i++ {
		key := addMonths(evaluateMonth, -i)
		cents := monthly[key]
		prior = append(prior, domain.ProductInsightMonth{Month: key, Cents: cents})
		if cents > 0 {
			baselineSum += cents
			baselineMonths++
		}
	}
	if baselineMonths < anomalyMinDataMonths {
		e.log.Debug("category anomaly skipped: baseline too sparse",
			slog.Int64("category_id", categoryID), slog.String("month", evaluateMonth),
			slog.Int("months_with_spend", baselineMonths))
		return
	}
	baseline := baselineSum / int64(baselineMonths)
	current := monthly[evaluateMonth]

	triggered, risePct := evaluateSpike(current, baseline, e.risePct, e.minBaseline)
	if !triggered {
		return
	}

	// The cooldown keys on category AND evaluated month: the same month's
	// finding is suppressed for 14 days, but a backfilled earlier month (or
	// the next month's own trigger) still reports independently.
	cooling, err := e.store.RecentlyInsightedCategory(ctx,
		domain.ProductInsightCategorySpike, categoryID, evaluateMonth, time.Now().Add(-anomalyCooldown))
	if err != nil {
		e.log.Warn("category anomaly cooldown check", slog.Any("error", err))
	}
	if cooling {
		e.log.Debug("category anomaly suppressed by cooldown",
			slog.Int64("category_id", categoryID), slog.String("month", evaluateMonth))
		return
	}

	cat, err := e.categories.GetByID(ctx, categoryID)
	if err != nil {
		e.log.Debug("category anomaly skipped: category unreachable",
			slog.Int64("category_id", categoryID), slog.Any("error", err))
		return
	}

	finding := categoryFinding{
		categoryID: categoryID,
		name:       cat.Name,
		currency:   base,
		data: domain.ProductInsightData{
			Threshold:     e.risePct,
			ChangePct:     risePct,
			Month:         evaluateMonth,
			CurrentCents:  current,
			BaselineCents: baseline,
			PriorMonths:   prior,
		},
		message: fmt.Sprintf(
			"%s spending %s is running %.0f%% above your recent monthly average.",
			cat.Name, monthDisplay(evaluateMonth), risePct),
	}
	e.persist(ctx, finding)
}

// persist phrases the finding with the AI when a reader connector answers
// (the auto wording stays the fallback) and stores it. Storage errors are
// logged, never returned — an anomaly is a nudge, not a ledger entry.
func (e *categoryAnomalyEngine) persist(ctx context.Context, f categoryFinding) {
	provider, promptBase, aiReady := e.aiConfigured(ctx)
	source := "auto"
	if aiReady {
		if msg := e.phrase(ctx, provider, promptBase, f); msg != "" {
			f.message = msg
			source = "ai"
		}
	}
	ins := domain.ProductInsight{
		Kind:         domain.ProductInsightCategorySpike,
		CategoryID:   &f.categoryID,
		CategoryName: f.name,
		Currency:     f.currency,
		Message:      f.message,
		Source:       source,
		Data:         f.data,
	}
	if _, err := e.store.Create(ctx, ins); err != nil {
		e.log.Warn("save category anomaly", slog.Any("error", err))
		return
	}
	e.log.Info("category anomaly saved", slog.String("kind", string(domain.ProductInsightCategorySpike)),
		slog.Int64("category_id", f.categoryID), slog.String("month", f.data.Month), slog.String("source", source))
}

// aiConfigured resolves the default bill-read connector and prompt; either
// failing means "phrase nothing" — the auto wording is kept.
func (e *categoryAnomalyEngine) aiConfigured(ctx context.Context) (domain.AIProvider, string, bool) {
	provider, err := e.providers.DefaultBillProvider(ctx)
	if err != nil {
		return domain.AIProvider{}, "", false
	}
	prompt, err := e.prompts.ResolvePrompt(ctx, domain.PromptKeyCategoryAnomaly)
	if err != nil || strings.TrimSpace(prompt) == "" {
		return domain.AIProvider{}, "", false
	}
	return provider, prompt, true
}

// phrase runs one prompt-only call and returns the cleaned nudge text — ""
// keeps the auto wording.
func (e *categoryAnomalyEngine) phrase(ctx context.Context, provider domain.AIProvider, promptBase string, f categoryFinding) string {
	callCtx, cancel := context.WithTimeout(ctx, e.aiTimeout)
	defer cancel()
	raw, err := e.ai.CompleteText(callCtx, provider, promptBase+"\n\nFinding:\n"+anomalyFactsJSON(f))
	if err != nil {
		e.log.Debug("category anomaly phrasing failed — keeping the auto wording", slog.Any("error", err))
		return ""
	}
	return cleanInsightReply(raw)
}

// categoryFinding is one trigger hit, before persistence.
type categoryFinding struct {
	categoryID int64
	name       string
	currency   string
	data       domain.ProductInsightData
	message    string
}

// anomalyFactsJSON renders the finding as the JSON the prompt documents —
// human amounts in the base currency, never formulas.
func anomalyFactsJSON(f categoryFinding) string {
	facts := struct {
		Kind          string                       `json:"kind"`
		Category      string                       `json:"category"`
		Currency      string                       `json:"currency"`
		Month         string                       `json:"month"`
		CurrentCents  int64                        `json:"current_cents"`
		BaselineCents int64                        `json:"baseline_cents"`
		RisePct       float64                      `json:"rise_pct"`
		ThresholdPct  float64                      `json:"threshold_pct"`
		PriorMonths   []domain.ProductInsightMonth `json:"prior_months"`
	}{
		Kind:          string(domain.ProductInsightCategorySpike),
		Category:      f.name,
		Currency:      f.currency,
		Month:         f.data.Month,
		CurrentCents:  f.data.CurrentCents,
		BaselineCents: f.data.BaselineCents,
		RisePct:       f.data.ChangePct,
		ThresholdPct:  f.data.Threshold,
		PriorMonths:   f.data.PriorMonths,
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// evaluateSpike applies the deterministic trigger: month spend at least the
// rise threshold above a baseline that itself clears the noise floor. The
// caller has already required at least anomalyMinDataMonths prior months
// with spend; those (only) formed the baseline — a zero month would deflate
// the average and make the trigger easier. Returns the rise in percent.
func evaluateSpike(currentCents, baselineCents int64, risePct float64, minBaseline int64) (bool, float64) {
	if currentCents <= 0 || baselineCents <= 0 || baselineCents < minBaseline {
		return false, 0
	}
	rise := (float64(currentCents)/float64(baselineCents) - 1) * 100
	if rise < risePct {
		return false, rise
	}
	return true, rise
}

// foldCategoryDays converts the native-currency day rows into base-currency
// month buckets:
//   - the evaluated in-progress month counts through today (an in-month
//     backfill never invents future spend), while prior months count through
//     the same day-of-month — the fair partial-month basis;
//   - a fully past evaluated month counts its complete span on all sides;
//   - rows after the evaluated month (a clock race) never count, so a future
//     month evaluates as empty and cannot trigger on current <= 0.
func foldCategoryDays(rows []domain.CategoryDayCurrencyTotal, conv *fxConverter, evaluateMonth, evalMonthEnd string, today time.Time) map[string]int64 {
	out := map[string]int64{}
	todayDate := today.Format("2006-01-02")
	// The evaluation cutoff for the evaluated month itself: its end when the
	// month is already over, today when it is still in progress.
	evalLimit := todayDate
	if evalMonthEnd != "" {
		evalLimit = evalMonthEnd
	}
	// The day-of-month prior months are cut at: the evaluated month's end
	// day when it is complete (full spans everywhere), else today's day.
	cutoffDay := evalLimit[len(evalLimit)-2:]

	for _, r := range rows {
		m := monthOf(r.Date)
		if m > evaluateMonth {
			continue
		}
		if m == evaluateMonth {
			if r.Date > evalLimit {
				continue
			}
		} else if evalMonthEnd == "" && r.Date[8:10] > cutoffDay {
			// Prior months mirror the partial evaluation only when the
			// evaluated month is itself partial.
			continue
		}
		out[m] += conv.add(r.Currency, r.TotalCents)
	}
	return out
}

// fxConverter is the jobs-local base-currency conversion: one snapshot, 1:1
// when a rate is missing — the summary service's convention (data stays
// visible instead of being dropped) for what is a nudge, not an aggregate.
type fxConverter struct {
	base string
	snap domain.RateSnapshot
}

func (c *fxConverter) add(currency string, cents int64) int64 {
	rate, ok := c.snap.Rate(currency, c.base)
	if !ok {
		rate = 1
	}
	return fx.ConvertCents(cents, rate)
}

// monthOf returns the "YYYY-MM" of a "YYYY-MM-DD" date.
func monthOf(date string) string {
	if len(date) >= 7 {
		return date[:7]
	}
	return date
}

// firstOfMonthAdd parses "YYYY-MM" (zero time on a malformed label) and
// returns the first day of the month shiftBy months before it.
func firstOfMonthAdd(month string, shiftBy int) time.Time {
	t, err := time.Parse("2006-01", month)
	if err != nil {
		return time.Time{}
	}
	return t.AddDate(0, shiftBy, 0)
}

// addMonths shifts a "YYYY-MM" label (unchanged when malformed).
func addMonths(month string, shiftBy int) string {
	t := firstOfMonthAdd(month, shiftBy)
	if t.IsZero() {
		return month
	}
	return t.Format("2006-01")
}

// monthEndOf returns the last day of the evaluated month — "" when the month
// is still in progress or beyond today (a future-dated evaluateMonth).
func monthEndOf(month string, today time.Time) string {
	first := firstOfMonthAdd(month, 0)
	if first.IsZero() {
		return ""
	}
	end := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	if end.After(today) {
		return ""
	}
	return end.Format("2006-01-02")
}

// monthDisplay renders a "YYYY-MM" for the auto wording.
func monthDisplay(month string) string {
	t := firstOfMonthAdd(month, 0)
	if t.IsZero() {
		return month
	}
	return t.Format("January 2006")
}
