package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/riverqueue/river"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/extractor"
)

// Completion marker written into the generic settings store once the one-time
// unit-value backfill has finished; boot enqueues the job only while it is
// absent (a settings key, not a migration, so reset flows and reruns simply
// delete the key to force another pass).
const UnitValueCompletionKey = "unit_value_backfill_done"

// UnitValueBackfillKind is the River kind of the one-time job; the boot-time
// uniqueness check uses it together with the args.
const UnitValueBackfillKind = "unit_value_backfill"

// Batch and iteration bounds for the worker's scan loops. The candidate
// queries skip deposit/return rows, and every fix a pass writes removes rows
// from the next scan, so each pass terminates; the caps are the backstop
// against a bug turning a pass into a spin.
const (
	backfillScanLimit = 500
	backfillAIBatch   = 20
	backfillPassMax   = 1000
)

// UnitValueBackfillArgs marks the one-time job filling the migration-0027
// unit_value columns: parse the printed size out of the stored raw names, then
// fall back to an AI read for the rows the names cannot be parsed from.
type UnitValueBackfillArgs struct{}

func (UnitValueBackfillArgs) Kind() string { return UnitValueBackfillKind }

// Consumer-side data contracts, satisfied by the repositories (which speak
// only domain types) and the settings repo — the same pattern the service
// layer uses, so this package stays testable and repository-free.
type (
	// UnitValueProductsRepo lists catalogue rows missing a magnitude and
	// writes the parsed/resolved ones back.
	UnitValueProductsRepo interface {
		ProductsMissingUnitValue(ctx context.Context, limit int) ([]domain.UnitValueRow, error)
		BackfillProductUnitValues(ctx context.Context, fixes []domain.UnitValueFix) error
	}
	// UnitValueBillItemsRepo does the same for scanned receipt lines.
	UnitValueBillItemsRepo interface {
		BillItemsMissingUnitValue(ctx context.Context, limit int) ([]domain.UnitValueRow, error)
		BackfillBillItemUnitValues(ctx context.Context, fixes []domain.UnitValueFix) error
	}
	// UnitValueTransactionItemsRepo does the same for manual purchase lines.
	UnitValueTransactionItemsRepo interface {
		TransactionItemsMissingUnitValue(ctx context.Context, limit int) ([]domain.UnitValueRow, error)
		BackfillTransactionItemUnitValues(ctx context.Context, fixes []domain.UnitValueFix) error
	}
	// UnitValueSettings puts the completion marker.
	UnitValueSettings interface {
		Put(ctx context.Context, key, value string) error
	}
	// UnitValueProvider resolves the AI connector flagged default_for_bills
	// (the read-connector the bill scanning uses).
	UnitValueProvider interface {
		DefaultBillProvider(ctx context.Context) (domain.AIProvider, error)
	}
	// UnitValuePrompts resolves the database-stored prompt text by key.
	UnitValuePrompts interface {
		ResolvePrompt(ctx context.Context, key string) (string, error)
	}
	// UnitValueAI runs the prompt-only unit-value call.
	UnitValueAI interface {
		UnitValues(ctx context.Context, provider domain.AIProvider, prompt string) ([]extractor.UnitValueAnswer, error)
	}
)

// UnitValueBackfillWorker implements River's Worker interface.
type UnitValueBackfillWorker struct {
	river.WorkerDefaults[UnitValueBackfillArgs]

	products  UnitValueProductsRepo
	billItems UnitValueBillItemsRepo
	txItems   UnitValueTransactionItemsRepo
	settings  UnitValueSettings
	providers UnitValueProvider
	prompts   UnitValuePrompts
	ai        UnitValueAI
	aiTimeout time.Duration
	log       *slog.Logger
}

func NewUnitValueBackfillWorker(
	products UnitValueProductsRepo,
	billItems UnitValueBillItemsRepo,
	txItems UnitValueTransactionItemsRepo,
	settings UnitValueSettings,
	providers UnitValueProvider,
	prompts UnitValuePrompts,
	ai UnitValueAI,
	aiTimeout time.Duration,
	log *slog.Logger,
) *UnitValueBackfillWorker {
	return &UnitValueBackfillWorker{
		products:  products,
		billItems: billItems,
		txItems:   txItems,
		settings:  settings,
		providers: providers,
		prompts:   prompts,
		ai:        ai,
		aiTimeout: aiTimeout,
		log:       log,
	}
}

// Work runs the whole backfill to completion in one job: products by parsing
// their raw names, bill items and transaction items by copying the product
// magnitudes just resolved (or, failing that, by parsing the line name). Rows
// neither the parser nor the AI can resolve stay unknown — magnitudes are
// never guessed. Every write is guarded by unit_value IS NULL on the
// repository side, so a retried run only works on the remainder. Success
// always sets the completion marker, even when rows stay unknown, so boot does
// not enqueue again; failing AI calls bubble up as errors and River retries
// them with the client's LLM-aware backoff (the queued-model lesson from the
// normalization job: never instantly retry a call the model may still be
// generating).
func (w *UnitValueBackfillWorker) Work(ctx context.Context, job *river.Job[UnitValueBackfillArgs]) error {
	start := time.Now()

	const (
		billTable = "bill_items"
		txTable   = "transaction_items"
	)
	productsParsed := w.passFromNames(ctx, "products", w.products.ProductsMissingUnitValue, w.products.BackfillProductUnitValues)
	billCopied := w.passCopyProducts(ctx, billTable, w.billItems.BillItemsMissingUnitValue, w.billItems.BackfillBillItemUnitValues)
	billParsed := w.passFromNames(ctx, billTable, w.billItems.BillItemsMissingUnitValue, w.billItems.BackfillBillItemUnitValues)
	txCopied := w.passCopyProducts(ctx, txTable, w.txItems.TransactionItemsMissingUnitValue, w.txItems.BackfillTransactionItemUnitValues)
	txParsed := w.passFromNames(ctx, txTable, w.txItems.TransactionItemsMissingUnitValue, w.txItems.BackfillTransactionItemUnitValues)

	remaining, err := w.countRemaining(ctx)
	if err != nil {
		return err
	}

	var productsAI, linesCopied, linesAI int
	if remaining > 0 {
		productsAI, err = w.passAI(ctx, "products", w.products.ProductsMissingUnitValue, w.products.BackfillProductUnitValues)
		if err != nil {
			return err
		}
		// The AI pass resolved more catalogue magnitudes — recopy them onto
		// their lines before asking the AI about the leftovers.
		linesCopied = w.passCopyProducts(ctx, billTable, w.billItems.BillItemsMissingUnitValue, w.billItems.BackfillBillItemUnitValues) +
			w.passCopyProducts(ctx, txTable, w.txItems.TransactionItemsMissingUnitValue, w.txItems.BackfillTransactionItemUnitValues)
		aiBill, aiErr := w.passAI(ctx, billTable, w.billItems.BillItemsMissingUnitValue, w.billItems.BackfillBillItemUnitValues)
		aiTx, txErr := w.passAI(ctx, txTable, w.txItems.TransactionItemsMissingUnitValue, w.txItems.BackfillTransactionItemUnitValues)
		linesAI = aiBill + aiTx
		if aiErr != nil {
			return aiErr
		}
		if txErr != nil {
			return txErr
		}
	}

	if err := w.settings.Put(ctx, UnitValueCompletionKey, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("write completion marker: %w", err)
	}

	w.log.Info("unit_value backfill finished",
		"duration", time.Since(start).String(),
		"productsParsed", productsParsed, "productsAI", productsAI,
		"billCopied", billCopied+linesCopied, "billParsed", billParsed,
		"txCopied", txCopied, "txParsed", txParsed, "linesAI", linesAI,
	)
	return nil
}

// countRemaining reports how many rows still lack a magnitude after the
// deterministic passes (products + both line tables), gating the AI passes.
func (w *UnitValueBackfillWorker) countRemaining(ctx context.Context) (int, error) {
	remaining := 0
	for _, list := range []func(context.Context, int) ([]domain.UnitValueRow, error){
		w.products.ProductsMissingUnitValue,
		w.billItems.BillItemsMissingUnitValue,
		w.txItems.TransactionItemsMissingUnitValue,
	} {
		rows, err := list(ctx, 1)
		if err != nil {
			return 0, fmt.Errorf("unit-value backfill: count remaining candidates: %w", err)
		}
		remaining += len(rows)
	}
	return remaining, nil
}

// passFromNames resolves rows by parsing the printed size out of their raw
// name (right-most `number + unit`); the canonical unit is adopted only where
// the row still records none, and a row whose stored unit disagrees with the
// parse's dimension stays unresolved. Re-scans until a full scan yields no new
// fixes: written rows leave the candidate query, unparseable rows remain and
// end the pass on the second scan.
func (w *UnitValueBackfillWorker) passFromNames(
	ctx context.Context,
	table string,
	list func(ctx context.Context, limit int) ([]domain.UnitValueRow, error),
	write func(ctx context.Context, fixes []domain.UnitValueFix) error,
) int {
	written := 0
	for i := 0; i < backfillPassMax; i++ {
		rows, err := list(ctx, backfillScanLimit)
		if err != nil {
			w.log.Warn("unit_value backfill: candidate read failed, stopping pass",
				slog.String("table", table), slog.String("error", err.Error()))
			return written
		}
		if len(rows) == 0 {
			return written
		}
		fixes := make([]domain.UnitValueFix, 0, len(rows))
		for _, row := range rows {
			value, unit, ok := ParsePrintedSize(row.Name)
			if !ok || !rowUnitCompatible(row, unit) {
				continue
			}
			fixes = append(fixes, fix(row, value, unit, row.Unit == ""))
		}
		if len(fixes) == 0 {
			return written
		}
		if err := write(ctx, fixes); err != nil {
			w.log.Warn("unit_value backfill: write failed, stopping pass",
				slog.String("table", table), slog.String("error", err.Error()))
			return written
		}
		written += len(fixes)
	}
	return written
}

// passCopyProducts copies the linked product's decided magnitude onto lines
// missing one: value plus the product's unit when the line records no unit of
// its own, value only when the dimensions agree.
func (w *UnitValueBackfillWorker) passCopyProducts(
	ctx context.Context,
	table string,
	list func(ctx context.Context, limit int) ([]domain.UnitValueRow, error),
	write func(ctx context.Context, fixes []domain.UnitValueFix) error,
) int {
	written := 0
	for i := 0; i < backfillPassMax; i++ {
		rows, err := list(ctx, backfillScanLimit)
		if err != nil {
			w.log.Warn("unit_value backfill: candidate read failed, stopping pass",
				slog.String("table", table), slog.String("error", err.Error()))
			return written
		}
		if len(rows) == 0 {
			return written
		}
		fixes := make([]domain.UnitValueFix, 0, len(rows))
		for _, row := range rows {
			if row.ProductUnitValue == nil || *row.ProductUnitValue <= 0 {
				continue
			}
			if row.Unit == "" {
				fixes = append(fixes, fix(row, *row.ProductUnitValue, row.ProductUnit, true))
				continue
			}
			if dimension(canonicalUnit(row.ProductUnit)) == dimension(row.Unit) {
				fixes = append(fixes, fix(row, *row.ProductUnitValue, "", false))
			}
		}
		if len(fixes) == 0 {
			return written
		}
		if err := write(ctx, fixes); err != nil {
			w.log.Warn("unit_value backfill: write failed, stopping pass",
				slog.String("table", table), slog.String("error", err.Error()))
			return written
		}
		written += len(fixes)
	}
	return written
}

// passAI asks the default bill-reading connector for the sizes of the rows the
// parser could not resolve, in batches. Names the AI cannot answer are marked
// asked and left unknown (one unresolvable name never spins the pass); failed
// calls bubble up as errors so River retries the job with the LLM-aware
// backoff — a fresh attempt starts the deterministic passes over first.
func (w *UnitValueBackfillWorker) passAI(
	ctx context.Context,
	table string,
	list func(ctx context.Context, limit int) ([]domain.UnitValueRow, error),
	write func(ctx context.Context, fixes []domain.UnitValueFix) error,
) (int, error) {
	provider, err := w.providers.DefaultBillProvider(ctx)
	if err != nil {
		return 0, fmt.Errorf("unit-value backfill needs the AI: no connector flagged default_for_bills — configure one under Settings → AI connectors so the remaining %s rows can be filled (job retries with backoff)", table)
	}
	prompt, err := w.prompts.ResolvePrompt(ctx, domain.PromptKeyUnitValueBackfill)
	if err != nil {
		return 0, fmt.Errorf("unit-value backfill: resolve prompt: %w", err)
	}

	written := 0
	asked := make(map[int64]bool)
	for i := 0; i < backfillPassMax; i++ {
		rows, err := list(ctx, backfillScanLimit)
		if err != nil {
			return written, fmt.Errorf("unit-value backfill: read %s candidates: %w", table, err)
		}

		// Rows whose name the AI has already failed to resolve never leave the
		// candidate query; mark them asked so the pass does not spin on them
		// and finishes once every remaining row has had its one question.
		var unasked []domain.UnitValueRow
		for _, row := range rows {
			if !asked[row.ID] {
				unasked = append(unasked, row)
			}
		}
		if len(unasked) == 0 {
			return written, nil
		}
		batch := unasked
		if len(batch) > backfillAIBatch {
			batch = batch[:backfillAIBatch]
		}
		for _, row := range batch {
			asked[row.ID] = true
		}

		names := make([]string, 0, len(batch))
		for _, row := range batch {
			names = append(names, row.Name)
		}
		callCtx, cancel := context.WithTimeout(ctx, w.aiTimeout)
		answers, err := w.ai.UnitValues(callCtx, provider, extractor.FormatUnitValuePrompt(prompt, names))
		cancel()
		if err != nil {
			return written, fmt.Errorf("unit-value backfill: AI read of %s batch (job retries with backoff, batch of %d): %w", table, len(batch), err)
		}

		byName := make(map[string]extractor.UnitValueAnswer, len(answers))
		for _, answer := range answers {
			byName[strings.ToLower(answer.Name)] = answer
		}
		fixes := make([]domain.UnitValueFix, 0, len(batch))
		for _, row := range batch {
			answer, ok := byName[strings.ToLower(row.Name)]
			if !ok {
				continue
			}
			unit := canonicalUnit(answer.Unit)
			if !rowUnitCompatible(row, unit) {
				continue
			}
			fixes = append(fixes, fix(row, answer.UnitValue, unit, row.Unit == ""))
		}
		if len(fixes) > 0 {
			if err := write(ctx, fixes); err != nil {
				return written, fmt.Errorf("unit-value backfill: write %s magnitudes: %w", table, err)
			}
			written += len(fixes)
		}
	}
	return written, nil
}

// fix assembles one write: the parsed value, and the adopted unit only when
// the row records none (adoptUnit).
func fix(row domain.UnitValueRow, value float64, unit string, adoptUnit bool) domain.UnitValueFix {
	if !adoptUnit || unit == "" {
		return domain.UnitValueFix{ID: row.ID, UnitValue: value}
	}
	return domain.UnitValueFix{ID: row.ID, UnitValue: value, Unit: &unit}
}

// rowUnitCompatible refuses a parsed unit for a row that already records a
// different dimension ("pcs" and a printed "500 g" stay unresolved — the row
// is left for the AI pass rather than overwritten with a contradiction; a
// matching dimension accepts the value with the stored unit kept).
func rowUnitCompatible(row domain.UnitValueRow, unit string) bool {
	if row.Unit == "" {
		return true
	}
	return dimension(unit) == dimension(row.Unit)
}

// dimension groups the canonical units into mass, volume and count so a
// printed "500 ml" never overwrites a decided unit "g" and vice versa.
func dimension(unit string) string {
	switch unit {
	case "kg", "g":
		return "mass"
	case "l", "ml":
		return "volume"
	case "pcs":
		return "count"
	default:
		return ""
	}
}

// canonicalUnit maps a stored/printed unit spelling to the canonical measure
// the system computes prices against (kg/g/l/ml/pcs), empty when it maps to
// none (unknown units are never touched).
func canonicalUnit(unit string) string {
	// the trailing dot receipts print is part of the abbreviation ("6 Stk.")
	uk := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(unit)), ".")
	switch uk {
	case "kg", "kgs", "kilogram", "kilogramme", "kilogramm":
		return "kg"
	case "g", "gr", "gram", "grams", "gramm":
		return "g"
	case "l", "lt", "ltr", "liter", "litre":
		return "l"
	case "ml", "milliliter", "millilitre":
		return "ml"
	case "pcs", "pc", "piece", "pieces", "stk", "stück", "stck", "ea", "ct":
		return "pcs"
	default:
		return ""
	}
}

// printedSize is the right-most `number + printed unit` of a name — the last
// match wins, so qualifiers earlier in the name ("Milch 3,5% Fett 1l" → "1l",
// "2 x 500g" → "500g") never shadow the size the line's measure refers to.
// Alternatives run most-specific first, each \b-anchored, so "Granulat" is
// never read as a gram size and "Gb" never as grams.
var printedSize = regexp.MustCompile(`(?i)([0-9]+(?:[.,][0-9]+)?)\s*(milliliter|millilitre|kilogram|kilogramm|kgs\b|liter|litre|ltr\b|lt\b|gramm|grams|gram\b|gr\b|ml\b|cl\b|dl\b|kg\b|pieces?\b|pcs\b|pc\b|stk\.|stück|stck\b|ea\b|ct\b|g\b|l\b)`)

// ParsePrintedSize extracts the printed size magnitude ("500" + "ml" for a
// 500ml bottle) from a raw product or line name. It never guesses: a name
// without a number+unit tail yields no match, and the caller leaves the row
// unknown. Decimal commas ("1,5 l") read as expected; a dot followed by
// exactly three digits after a non-zero leading group reads as the German
// thousands separator receipts print ("1.000 g" → 1000 g — cl/dl first scale
// into ml).
func ParsePrintedSize(name string) (float64, string, bool) {
	matches := printedSize.FindAllStringSubmatchIndex(name, -1)
	if len(matches) == 0 {
		return 0, "", false
	}
	m := matches[len(matches)-1]
	// a minus glued to the number ("-0,25l") prints a credit/return — never a
	// size; leave the row unknown (return lines are excluded upstream anyway)
	if m[2] > 0 && name[m[2]-1] == '-' {
		return 0, "", false
	}
	rawUnit := strings.ToLower(strings.TrimSpace(name[m[4]:m[5]]))
	value, ok := parseSizeNumber(name[m[2]:m[3]], rawUnit)
	if !ok {
		return 0, "", false
	}
	switch rawUnit {
	case "cl": // centilitres are not a canonical measure: scale into ml
		value *= 10
		rawUnit = "ml"
	case "dl":
		value *= 100
		rawUnit = "ml"
	}
	unit := canonicalUnit(rawUnit)
	if unit == "" {
		return 0, "", false
	}
	return value, unit, true
}

// parseSizeNumber reads the printed magnitude for a mass or volume unit:
// decimal commas ("1,5") and plain dots ("1.5") give 1.5; the German
// thousands form "1.000"/"12.500" (dot + exactly three fractional digits
// after a non-zero leading group, printed mass sizes like "1.000 g") gives
// 1000/12500. Volume magnitudes keep the decimal reading ("1.500 l" = 1.5 l —
// receipts print volumes with a comma, never thousands), and every result
// must stay positive.
func parseSizeNumber(num, rawUnit string) (float64, bool) {
	mass := rawUnit == "g" || rawUnit == "gr" || rawUnit == "gram" || rawUnit == "gramm" || rawUnit == "grams" ||
		rawUnit == "kg" || rawUnit == "kilogram" || rawUnit == "kilogramm"
	dotIdx := strings.LastIndex(num, ".")
	commaIdx := strings.LastIndex(num, ",")
	if dotIdx < 0 && commaIdx < 0 {
		v, err := strconv.ParseFloat(num, 64)
		return v, err == nil && v > 0
	}
	if mass && dotIdx > commaIdx {
		intPart, fracPart := num[:dotIdx], num[dotIdx+1:]
		if len(intPart) > 0 && intPart[0] != '0' && len(fracPart) == 3 {
			v, err := strconv.ParseFloat(strings.ReplaceAll(num, ".", ""), 64)
			return v, err == nil && v > 0
		}
	}
	if commaIdx > dotIdx { // decimal comma (German receipts)
		v, err := strconv.ParseFloat(strings.ReplaceAll(num, ",", "."), 64)
		return v, err == nil && v > 0
	}
	v, err := strconv.ParseFloat(num, 64)
	return v, err == nil && v > 0
}
