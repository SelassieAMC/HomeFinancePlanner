package jobs

import (
	"fmt"
	"strconv"
	"strings"

	"home-finance-planner/backend/internal/domain"
)

// The deterministic heart of the insight job: pure PPU trigger evaluation on
// price-per-unit rows (unit_price_cents / unit_value, normalized to the
// canonical unit exactly like the analytics read: g→per-kg, ml→per-l). No
// repositories, no AI — the tests drive these without SQLite.

// ppuRow is one comparable purchase: the price per canonical unit (cents) and
// the size in canonical units, keeping the printed unit/value for the facts.
type ppuRow struct {
	date           string // YYYY-MM-DD
	store          string
	unit           string  // the printed unit the size pairs with
	unitValue      float64 // the printed magnitude ("500" for 500 g)
	canonical      string  // kg / l / pcs
	size           float64 // unit_value scaled to the canonical unit
	unitPriceCents int64
	ppu            float64 // cents per canonical unit
}

// ppuCanonical folds a printed measure into the canonical comparison unit
// the PPU math compares in (kg/l/pcs) — g→kg, ml→l, like the analytics read —
// returning the size scale (g→kg: 500 means 0.5 kg and prices ×1000). The
// raw spelling is normalized first via the unit_value job's normalizer, so
// printed oddities ("Gr.") still resolve. Unknown measures get ok=false.
func ppuCanonical(unit string) (string, float64, bool) {
	switch canonicalUnit(unit) {
	case "g":
		return "kg", 1000, true
	case "ml":
		return "l", 1000, true
	case "kg", "l", "pcs":
		return canonicalUnit(unit), 1, true
	default:
		return "", 0, false
	}
}

func ppuRowFromLine(line domain.InsightLine) (ppuRow, bool) {
	if line.UnitValue == nil {
		return ppuRow{}, false
	}
	return newPPURow(line.Date, "", line.Unit, *line.UnitValue, line.UnitPriceCents)
}

func ppuRowFromEntry(e domain.ProductHistoryEntry) (ppuRow, bool) {
	if e.UnitValue == nil {
		return ppuRow{}, false
	}
	return newPPURow(e.Date, e.StoreName, e.Unit, *e.UnitValue, e.UnitPriceCents)
}

func newPPURow(date, store, unit string, unitValue float64, unitPriceCents int64) (ppuRow, bool) {
	if unitPriceCents <= 0 || unitValue <= 0 {
		return ppuRow{}, false
	}
	canonical, scale, ok := ppuCanonical(unit)
	if !ok {
		return ppuRow{}, false
	}
	return ppuRow{
		date:           date,
		store:          store,
		unit:           unit,
		unitValue:      unitValue,
		canonical:      canonical,
		size:           unitValue / scale,
		unitPriceCents: unitPriceCents,
		ppu:            float64(unitPriceCents) / unitValue * scale,
	}, true
}

// evaluateProductTriggers runs the per-product triggers (shrinkflation, price
// creep) for one freshly saved line against its own purchase history, newest
// first and already excluded from the analyzed purchase.
func evaluateProductTriggers(line domain.InsightLine, product domain.Product, history []domain.ProductHistoryEntry, threshold float64) []insightFinding {
	cur, ok := ppuRowFromLine(line)
	if !ok {
		return nil
	}
	rows := []ppuRow{cur}
	for _, en := range history {
		if r, ok := ppuRowFromEntry(en); ok && r.canonical == cur.canonical {
			rows = append(rows, r)
		}
	}

	var findings []insightFinding
	if len(rows) >= 2 {
		if f, hit := shrinkflationFinding(product, line.Currency, rows[0], rows[1]); hit {
			findings = append(findings, f)
		}
	}
	// Both triggers are rise stories — when the shrink already explains this
	// purchase's rise, the creep story would tell the same thing twice.
	if !hasFinding(findings, domain.ProductInsightShrinkflation) && len(rows) >= 1+insightCreepSteps {
		if f, hit := priceCreepFinding(product, line.Currency, rows[:1+insightCreepSteps], threshold); hit {
			findings = append(findings, f)
		}
	}
	return findings
}

// shrinkflationFinding hits when the pack shrank (≥ insightSizeShrinkPct)
// while the unit price stayed (≤ insightPriceFlatTolPct rise) — the user now
// pays more per unit for less.
func shrinkflationFinding(product domain.Product, currency string, cur, prev ppuRow) (insightFinding, bool) {
	if cur.canonical != prev.canonical {
		return insightFinding{}, false
	}
	dropped := (prev.size - cur.size) / prev.size * 100
	if dropped < insightSizeShrinkPct {
		return insightFinding{}, false
	}
	priceRose := (float64(cur.unitPriceCents) - float64(prev.unitPriceCents)) / float64(prev.unitPriceCents) * 100
	if priceRose > insightPriceFlatTolPct {
		return insightFinding{}, false
	}
	if prev.ppu <= 0 {
		return insightFinding{}, false
	}
	changePct := (cur.ppu/prev.ppu - 1) * 100

	data := domain.ProductInsightData{
		Unit:          cur.canonical,
		Threshold:     changePct,
		OldPriceCents: &prev.unitPriceCents,
		NewPriceCents: &cur.unitPriceCents,
		OldUnitValue:  &prev.unitValue,
		NewUnitValue:  &cur.unitValue,
		OldPPU:        &prev.ppu,
		NewPPU:        &cur.ppu,
		ChangePct:     changePct,
		Purchases: []domain.ProductInsightPurchase{
			purchaseFacts(cur), purchaseFacts(prev),
		},
	}
	msg := fmt.Sprintf(
		"Shrinkflation alert: %s is smaller now — the pack went from %s to %s while the unit price stayed at %s. You now pay about %.0f%% more per %s.",
		product.Name, sizeText(prev), sizeText(cur), moneyText(prev.unitPriceCents, currency),
		changePct, cur.canonical)
	return insightFinding{
		kind:      domain.ProductInsightShrinkflation,
		productID: product.ID,
		name:      product.Name,
		generic:   product.GenericName,
		currency:  currency,
		data:      data,
		message:   msg,
	}, true
}

// priceCreepFinding hits when the PPU rose in each of the last transitions
// (four purchases) and the cumulated rise passed the threshold — steady creep
// that merits trying another brand or store.
func priceCreepFinding(product domain.Product, currency string, rows []ppuRow, threshold float64) (insightFinding, bool) {
	for i := 0; i+1 < len(rows); i++ {
		if rows[i+1].ppu <= 0 || rows[i].ppu <= rows[i+1].ppu {
			return insightFinding{}, false
		}
		step := (rows[i].ppu - rows[i+1].ppu) / rows[i+1].ppu * 100
		if step < insightCreepStepMinPct {
			return insightFinding{}, false
		}
	}
	base := rows[len(rows)-1].ppu
	if base <= 0 {
		return insightFinding{}, false
	}
	total := (rows[0].ppu/base - 1) * 100
	if total < threshold {
		return insightFinding{}, false
	}

	facts := make([]domain.ProductInsightPurchase, 0, len(rows))
	for _, r := range rows {
		facts = append(facts, purchaseFacts(r))
	}
	data := domain.ProductInsightData{
		Unit:      rows[0].canonical,
		Threshold: total,
		OldPPU:    &base,
		NewPPU:    &rows[0].ppu,
		ChangePct: total,
		Purchases: facts,
	}
	msg := fmt.Sprintf(
		"Price creep: %s has cost you a little more every time — the price per %s is now %s, up %.0f%% across your last %d purchases. It might be time to try a different brand or store.",
		product.Name, rows[0].canonical, ppuMoneyText(rows[0].ppu, currency, rows[0].canonical), total, len(rows)-1)
	return insightFinding{
		kind:      domain.ProductInsightPriceCreep,
		productID: product.ID,
		name:      product.Name,
		generic:   product.GenericName,
		currency:  currency,
		data:      data,
		message:   msg,
	}, true
}

// bulkSizeGroup is one package-size bucket within a product family: the
// canonical size, the average PPU and the printed unit/value of its members.
type bulkSizeGroup struct {
	size      float64 // canonical units (kg/l)
	unit      string  // printed unit of the first member, for the facts
	value     float64 // printed value of the first member, for the facts
	purchases []ppuRow
	avgPPU    float64
}

// evaluateBulkBuy hits when the family is usually bought in a smaller size
// (the group with the most purchases, ≥2 purchases) whose average PPU is ≥
// threshold above a distinct size ≥1.2× larger — with the current purchase
// being another one of the usual small ones. Only scalable sizes (kg/l)
// qualify. The finding attaches to the just-bought product.
func evaluateBulkBuy(line domain.InsightLine, product domain.Product, family []domain.ProductHistoryEntry, threshold float64) (insightFinding, bool) {
	cur, ok := ppuRowFromLine(line)
	if !ok {
		return insightFinding{}, false
	}
	switch cur.canonical {
	case "kg", "l":
	default:
		return insightFinding{}, false
	}

	groups := map[string]*bulkSizeGroup{}
	for _, en := range family {
		r, ok := ppuRowFromEntry(en)
		if !ok || r.canonical != cur.canonical {
			continue
		}
		key := sizeKey(r.size)
		g, hit := groups[key]
		if !hit {
			g = &bulkSizeGroup{size: r.size, unit: r.unit, value: r.unitValue}
			groups[key] = g
		}
		g.purchases = append(g.purchases, r)
	}
	if len(groups) < 2 {
		return insightFinding{}, false
	}
	for _, g := range groups {
		g.avgPPU = groupAvgPPU(g)
	}

	// The "usual" size: the one the family buys most often; on a tie, the
	// smaller size (a habit beats a one-off). The big candidate: the largest
	// distinct size.
	var small, large *bulkSizeGroup
	for _, g := range groups {
		if small == nil || len(g.purchases) > len(small.purchases) ||
			(len(g.purchases) == len(small.purchases) && g.size < small.size) {
			small = g
		}
		if large == nil || g.size > large.size {
			large = g
		}
	}
	if small == large || small.size >= large.size*insightBulkSizeFactor {
		return insightFinding{}, false
	}
	// The just-bought size must be the usual small one — the advice reads as
	// "try the bigger pack next time", never as history trivia.
	if cur.size != small.size || len(small.purchases) < 2 {
		return insightFinding{}, false
	}
	if small.avgPPU <= 0 || large.avgPPU <= 0 {
		return insightFinding{}, false
	}
	gap := (small.avgPPU - large.avgPPU) / small.avgPPU * 100
	if gap < threshold {
		return insightFinding{}, false
	}

	data := domain.ProductInsightData{
		Unit:         cur.canonical,
		Threshold:    gap,
		OldPPU:       &small.avgPPU, // what the user pays per unit now
		NewPPU:       &large.avgPPU, // the price the bigger pack would cost per unit
		ChangePct:    gap,
		OldUnitValue: &small.value,
		NewUnitValue: &large.value,
		Purchases:    []domain.ProductInsightPurchase{purchaseFacts(cur)},
	}
	msg := fmt.Sprintf(
		"Bulk tip: %s is cheaper in bigger sizes — instead of your usual %s pack (about %s per %s), the %s pack averages %s per %s: %.0f%% less per unit. Worth trying.",
		emptyGeneric(product.GenericName, product.Name), sizePrinted(small),
		ppuMoneyText(small.avgPPU, line.Currency, cur.canonical), cur.canonical,
		sizePrinted(large), ppuMoneyText(large.avgPPU, line.Currency, cur.canonical), cur.canonical, gap)
	return insightFinding{
		kind:      domain.ProductInsightBulkBuy,
		productID: product.ID,
		name:      product.Name,
		generic:   product.GenericName,
		currency:  line.Currency,
		data:      data,
		message:   msg,
	}, true
}

func groupAvgPPU(g *bulkSizeGroup) float64 {
	if len(g.purchases) == 0 {
		return 0
	}
	var sum float64
	for _, r := range g.purchases {
		sum += r.ppu
	}
	return sum / float64(len(g.purchases))
}

// sizeKey groups sizes by their canonical value rounded to two decimals (a
// 500 g and a 0.5 kg row are the same size; 450 g is not).
func sizeKey(size float64) string {
	return strconv.FormatFloat(float64(int64(size*100+0.5))/100, 'f', 2, 64)
}

// hasFinding reports whether the evaluator already produced a kind for this
// line (shrinkflation suppressing the redundant creep story).
func hasFinding(findings []insightFinding, kind domain.ProductInsightKind) bool {
	for _, f := range findings {
		if f.kind == kind {
			return true
		}
	}
	return false
}

func purchaseFacts(r ppuRow) domain.ProductInsightPurchase {
	return domain.ProductInsightPurchase{
		Date:            r.date,
		StoreName:       r.store,
		UnitPriceCents:  r.unitPriceCents,
		UnitValue:       r.unitValue,
		Unit:            r.unit,
		PPUCentsPerUnit: r.ppu,
	}
}

// emptyGeneric falls back to the product name when the family is unknown.
func emptyGeneric(generic, product string) string {
	if s := strings.TrimSpace(generic); s != "" {
		return s
	}
	return product
}

// moneyText renders cents as a major-currency amount ("2.50 EUR").
func moneyText(cents int64, currency string) string {
	return fmt.Sprintf("%s %s", trimmedFloat(float64(cents)/100), currency)
}

// ppuMoneyText renders a price per canonical unit ("2.50 EUR/kg").
func ppuMoneyText(ppuCentsPerUnit float64, currency, unit string) string {
	return fmt.Sprintf("%s %s/%s", trimmedFloat(ppuCentsPerUnit/100), currency, unit)
}

// sizePrinted renders a size group's size as printed ("500 g", "1.5 l").
func sizePrinted(g *bulkSizeGroup) string {
	return trimmedFloat(g.value) + " " + g.unit
}

// sizeText renders a row's size the way its receipt printed it.
func sizeText(r ppuRow) string {
	return trimmedFloat(r.unitValue) + " " + r.unit
}

// trimmedFloat renders a float without trailing zeros ("1.500" → "1.5").
func trimmedFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
