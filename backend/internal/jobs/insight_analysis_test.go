package jobs

import (
	"strings"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

func hist(date, unit string, unitValue float64, cents int64) domain.ProductHistoryEntry {
	return domain.ProductHistoryEntry{
		ProductID: 1, Name: "Chips", Unit: unit, UnitValue: &unitValue,
		UnitPriceCents: cents, Currency: "EUR", Date: date, Source: "b", SourceID: 42,
	}
}

func line(unit string, unitValue float64, cents int64) domain.InsightLine {
	return domain.InsightLine{
		ProductID: 1, Name: "Chips", Unit: unit, UnitValue: &unitValue,
		UnitPriceCents: cents, Currency: "EUR", Date: "2026-10-05",
	}
}

func product() domain.Product {
	return domain.Product{ID: 1, Name: "Chips", GenericName: "Potato Chips"}
}

// Trigger A: same unit price, smaller pack → shrinkflation with the rise in
// the facts.
func TestShrinkflationFires(t *testing.T) {
	prev, cur := hist("2026-09-01", "g", 200, 200), line("g", 150, 200)
	findings := evaluateProductTriggers(cur, product(), []domain.ProductHistoryEntry{prev}, 10)
	if len(findings) != 1 || findings[0].kind != domain.ProductInsightShrinkflation {
		t.Fatalf("expected a shrinkflation finding, got %+v", findings)
	}
	if got := (*findings[0].data.NewPPU / *findings[0].data.OldPPU - 1) * 100; got < 30 || got > 40 {
		t.Fatalf("expected ~33%% PPU rise (200g→150g), got %.1f", got)
	}
	if findings[0].data.OldPriceCents == nil || *findings[0].data.OldPriceCents != 200 {
		t.Fatalf("expected the old unit price in the facts, got %+v", findings[0].data)
	}
	if !strings.Contains(findings[0].message, "150 g") || !strings.Contains(findings[0].message, "200 g") {
		t.Fatalf("message should name both sizes: %q", findings[0].message)
	}
}

// Trigger A stays silent when the size did not shrink or the price is not flat.
func TestShrinkflationRequiresPriceFlatAndSizeDrop(t *testing.T) {
	if _, hit := shrinkflationFinding(product(), "EUR", newLinePpu("g", 200, 200), newPpu("g", 200, 200)); hit {
		t.Fatal("same size must not trigger")
	}
	// 10% price rise is not "price stayed".
	if _, hit := shrinkflationFinding(product(), "EUR", newLinePpu("g", 150, 220), newPpu("g", 200, 200)); hit {
		t.Fatal("a 10%% dearer unit price must not read as flat")
	}
	// 3% size drop is inside the shrink tolerance.
	if _, hit := shrinkflationFinding(product(), "EUR", newLinePpu("g", 194, 200), newPpu("g", 200, 200)); hit {
		t.Fatal("a 3%% size drop must not trigger")
	}
}

// Trigger C: three consecutive PPU rises with the total above the threshold.
// History arrives newest-first, so the purchase before the current one is
// rows[1] and the baseline is the oldest row.
func TestPriceCreepFires(t *testing.T) {
	rows := []domain.ProductHistoryEntry{
		hist("2026-09-01", "l", 1, 124), // previous purchase
		hist("2026-08-01", "l", 1, 112),
		hist("2026-07-01", "l", 1, 100), // baseline, 1.00/l
	}
	findings := evaluateProductTriggers(line("l", 1, 138), product(), rows, 10) // 1.38 → +38%
	if len(findings) != 1 || findings[0].kind != domain.ProductInsightPriceCreep {
		t.Fatalf("expected a price-creep finding, got %+v", findings)
	}
}

// Trigger C stays silent on flat/sub-threshold trends and on interleaved drops.
func TestPriceCreepRequiresSteadyRise(t *testing.T) {
	base := []domain.ProductHistoryEntry{
		hist("2026-09-01", "l", 1, 138),
		hist("2026-08-01", "l", 1, 138),
		hist("2026-07-01", "l", 1, 138),
	}
	if got := evaluateProductTriggers(line("l", 1, 138), product(), base, 10); len(got) != 0 {
		t.Fatalf("flat prices must not trigger, got %+v", got)
	}

	// The total rise is inside the 10% threshold even though each step rose.
	small := []domain.ProductHistoryEntry{
		hist("2026-09-01", "l", 1, 135),
		hist("2026-08-01", "l", 1, 131),
		hist("2026-07-01", "l", 1, 127),
	}
	if got := evaluateProductTriggers(line("l", 1, 138), product(), small, 10); len(got) != 0 {
		t.Fatalf("a 9%% creep must stay under the default threshold, got %+v", got)
	}

	// Rising 2 of 3 transitions is not "steady": one step dipped.
	dipped := []domain.ProductHistoryEntry{
		hist("2026-09-01", "l", 1, 138),
		hist("2026-08-01", "l", 1, 118), // dipped against the baseline path
		hist("2026-07-01", "l", 1, 124),
	}
	if got := evaluateProductTriggers(line("l", 1, 138), product(), dipped, 10); len(got) != 0 {
		t.Fatalf("a dip among the rises must break the creep story, got %+v", got)
	}
}

// History newer-first: the newest row is the purchase before the current one.
func TestCreepReadsNewestFirst(t *testing.T) {
	// oldest → newest across three older purchases, each dearer
	old := []domain.ProductHistoryEntry{
		hist("2026-09-01", "l", 1, 124), // previous purchase (newest of the three)
		hist("2026-08-01", "l", 1, 112),
		hist("2026-07-01", "l", 1, 100),
	}
	findings := evaluateProductTriggers(line("l", 1, 138), product(), old, 10)
	if len(findings) != 1 {
		t.Fatalf("expected creep across the newest-first order, got %+v", findings)
	}
	if len(findings[0].data.Purchases) != 4 {
		t.Fatalf("facts should carry all four purchases, got %+v", findings[0].data.Purchases)
	}
}

// A mixed-size history never compares across canonical units: a g-row against
// a ml-row family is skipped, but kg/g fold together.
func TestCanonicalUnitFolding(t *testing.T) {
	// previous purchase stored "g", current stores "kg": 1500g ≙ 1.5kg, price flat.
	prevKg := hist("2026-09-01", "kg", 1.5, 300)
	findings := evaluateProductTriggers(line("g", 1500, 300), product(), []domain.ProductHistoryEntry{prevKg}, 10)
	if len(findings) != 0 {
		t.Fatalf("kg/g rows fold to the same canonical size — no shrink, got %+v", findings)
	}

	// ml against l folds the same way: 3000ml ≙ 3l at the same price → no hit.
	prevL := hist("2026-09-01", "l", 3, 450)
	if got := evaluateProductTriggers(line("ml", 3000, 450), product(), []domain.ProductHistoryEntry{prevL}, 10); len(got) != 0 {
		t.Fatalf("l/ml rows fold to the same canonical size, got %+v", got)
	}

	// Unanalyzable lines (unknown unit) never produce findings.
	bad := line("cl", 50, 200)
	if got := evaluateProductTriggers(bad, product(), []domain.ProductHistoryEntry{hist("2026-09-01", "l", 1, 200)}, 10); len(got) != 0 {
		t.Fatalf("unknown unit 'cl' must be skipped, got %+v", got)
	}
}

// Trigger B: the usual small pack of a family costs clearly more per unit
// than a bigger one the user bought once.
func TestBulkBuyFires(t *testing.T) {
	family := []domain.ProductHistoryEntry{
		hist("2026-10-01", "l", 1, 150),      // small, 1.50/l — the habit
		hist("2026-09-01", "l", 1, 155),      // small
		histOf(2, "2026-09-01", "l", 4, 500), // big jug, 1.25/l, bought once
	}

	cur := line("l", 1, 150) // 1.50/l — another small one
	f, hit := evaluateBulkBuy(cur, product(), family, 10)
	if !hit {
		t.Fatal("bulk-buy should fire: 1.50/l vs 1.25/l is a 17%% saving")
	}
	if f.kind != domain.ProductInsightBulkBuy || f.currency != "EUR" {
		t.Fatalf("unexpected finding %+v", f)
	}
	if !strings.Contains(f.message, "4 l") || !strings.Contains(f.message, "1 l") {
		t.Fatalf("message should name both sizes: %q", f.message)
	}
}

// Trigger B: pcs sizes cannot be bulk-compared, and buying the big pack
// itself is not "try the big pack".
func TestBulkBuyGuards(t *testing.T) {
	family := []domain.ProductHistoryEntry{
		hist("2026-10-01", "pcs", 6, 150),
		hist("2026-10-01", "pcs", 6, 155),
		histOf(2, "2026-09-01", "pcs", 12, 280),
	}
	if _, hit := evaluateBulkBuy(line("pcs", 6, 150), product(), family, 10); hit {
		t.Fatal("counted pieces have no scalable PPU")
	}

	// The current purchase IS the big pack — nothing to advise.
	big := []domain.ProductHistoryEntry{
		hist("2026-10-01", "l", 1, 150),
		hist("2026-10-01", "l", 1, 155),
		histOf(2, "2026-09-01", "l", 4, 500),
	}
	if _, hit := evaluateBulkBuy(line("l", 4, 500), product(), big, 10); hit {
		t.Fatal("buying the big pack must not trigger the bulk tip")
	}
}

func newPpu(unit string, uv float64, cents int64) ppuRow {
	r, _ := newPPURow("2026-09-01", "", unit, uv, cents)
	return r
}

func newLinePpu(unit string, uv float64, cents int64) ppuRow {
	return newPpu(unit, uv, cents)
}

func histOf(id int64, date, unit string, uv float64, cents int64) domain.ProductHistoryEntry {
	e := hist(date, unit, uv, cents)
	e.ProductID = id
	e.Name = "Whole Milk"
	return e
}
