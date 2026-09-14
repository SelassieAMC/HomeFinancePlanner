package service

import (
	"context"
	"math"
	"sort"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// AnalyticsStore is the persistence contract for chart aggregates. It returns
// native-currency rows; conversion into the base currency and all index math
// happen here, mirroring SummaryService.
type AnalyticsStore interface {
	ItemPrices(ctx context.Context, from, to string) ([]domain.ItemPriceRow, error)
	CategorySpend(ctx context.Context, from, to string) ([]domain.CategorySpendRow, error)
	DailyExpenseByMonth(ctx context.Context, months []string) ([]domain.DayExpenseRow, error)
	Heatmap(ctx context.Context, from, to string) ([]domain.SectionDayRow, error)
	SpendByMonthAndFixed(ctx context.Context, months []string) ([]domain.MonthFixedRow, error)
	OpenBudgetTotal(ctx context.Context) (int64, error)
}

// ValidateRange checks an inclusive YYYY-MM-DD from/to pair. Exported for
// handlers that decode the same params.
func ValidateRange(from, to string) error {
	if err := validateDate(from, "from"); err != nil {
		return err
	}
	if err := validateDate(to, "to"); err != nil {
		return err
	}
	if to < from {
		return validationError("to %q must not be before from %q", to, from)
	}
	return nil
}

// StoreNameSource supplies store display names (implemented by StoreService).
type StoreNameSource interface {
	List(ctx context.Context) ([]domain.Store, error)
}

// AnalyticsService computes the six dashboard chart payloads.
type AnalyticsService struct {
	store    AnalyticsStore
	settings *SettingsService
	rates    RateSource
	stores   StoreNameSource
}

func NewAnalyticsService(store AnalyticsStore, settings *SettingsService, rates RateSource, stores StoreNameSource) *AnalyticsService {
	return &AnalyticsService{store: store, settings: settings, rates: rates, stores: stores}
}

// Sample-size thresholds and tuning knobs for the chart math. Below these the
// charts show nothing rather than a misleading index.
const (
	minStoresPerProduct = 2    // a product must be sold by ≥2 stores to compare
	minMonthsForSeries  = 3    // a staple needs ≥3 months of purchases to trend
	topSeriesCount      = 8    // staples shown in the personal price index
	sunburstFoldShare   = 0.02 // slices under 2% of the total fold into "Other"
	sunburstMaxSegments = 12
)

// baseCurrencyAndConverter is the shared preamble of every analytics method:
// base currency from settings plus a rate-snapshot converter.
func (s *AnalyticsService) baseCurrencyAndConverter(ctx context.Context) (string, *converter, error) {
	base, err := s.settings.BaseCurrency(ctx)
	if err != nil {
		return "", nil, err
	}
	snap, err := s.rates.Snapshot(ctx)
	if err != nil {
		return base, nil, err
	}
	return base, newConverter(base, snap), nil
}

// productKey identifies a "standardized product": an item name (lowercased,
// trimmed) in one base unit. Milk 1l and Milk 0.5l are intentionally distinct
// keys — pack sizes are not price-comparable.
type productKey struct {
	name string
	unit string
}

// --- Chart 1: Store Price Efficiency Comparison -----------------------------

// StorePriceIndex compares stores on the products they both sell: for every
// item name+unit sold by at least minStoresPerProduct stores in the range,
// each store's mean price is measured against the mean of store means; a
// store's index is the average of its products' relative prices (100 =
// overall average, lower = cheaper).
func (s *AnalyticsService) StorePriceIndex(ctx context.Context, from, to string) (domain.StorePriceIndex, error) {
	out := domain.StorePriceIndex{From: from, To: to, MinStores: minStoresPerProduct, Rows: []domain.StorePriceIndexRow{}}
	if err := validateDate(from, "from"); err != nil {
		return out, err
	}
	if err := validateDate(to, "to"); err != nil {
		return out, err
	}
	if to < from {
		return out, validationError("to %q must not be before from %q", to, from)
	}
	base, conv, err := s.baseCurrencyAndConverter(ctx)
	if err != nil {
		return out, err
	}
	out.Currency = base

	rows, err := s.store.ItemPrices(ctx, from, to)
	if err != nil {
		return out, err
	}

	// product → store → converted prices (store_id 0 = bill without a store).
	products := map[productKey]map[int64][]float64{}
	for _, r := range rows {
		if r.StoreID == 0 {
			continue
		}
		key := productKey{name: r.Key, unit: r.BaseUnit}
		if products[key] == nil {
			products[key] = map[int64][]float64{}
		}
		products[key][r.StoreID] = append(products[key][r.StoreID], conv.addFloat(r.Currency, r.PricePerUnit))
	}

	// Per store: average relative price across its overlapping products.
	type storeAcc struct {
		sum   float64
		count int
	}
	perStore := map[int64]*storeAcc{}
	for _, prices := range products {
		if len(prices) < minStoresPerProduct {
			continue
		}
		means := make(map[int64]float64, len(prices))
		overall := 0.0
		for storeID, ps := range prices {
			means[storeID] = mean(ps)
			overall += means[storeID]
		}
		overall /= float64(len(prices))
		if overall <= 0 {
			continue
		}
		for storeID, m := range means {
			if perStore[storeID] == nil {
				perStore[storeID] = &storeAcc{}
			}
			perStore[storeID].sum += m / overall * 100
			perStore[storeID].count++
		}
	}

	names, err := s.storeNames(ctx)
	if err != nil {
		return out, err
	}
	for storeID, acc := range perStore {
		if acc.count == 0 {
			continue
		}
		out.Rows = append(out.Rows, domain.StorePriceIndexRow{
			StoreID:      storeID,
			StoreName:    names[storeID],
			Index:        round1(acc.sum / float64(acc.count)),
			ProductCount: acc.count,
		})
	}
	sort.Slice(out.Rows, func(i, j int) bool { return out.Rows[i].Index < out.Rows[j].Index })
	out.ConversionWarnings = conv.warnings()
	return out, nil
}

// storeNames resolves store IDs to display names in one list call.
func (s *AnalyticsService) storeNames(ctx context.Context) (map[int64]string, error) {
	stores, err := s.stores.List(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[int64]string, len(stores))
	for _, st := range stores {
		names[st.ID] = st.Name
	}
	return names, nil
}

// --- Chart 2: Micro-Category Spending Distribution (sunburst) ---------------

// CategorySunburst aggregates accepted bill lines into a two-level hierarchy:
// inner ring = category section, outer ring = product category. Sections and
// categories under sunburstFoldShare of the grand total fold into "Other" so
// tiny slices don't become unreadable slivers.
func (s *AnalyticsService) CategorySunburst(ctx context.Context, from, to string) (domain.CategorySunburst, error) {
	out := domain.CategorySunburst{From: from, To: to, Segments: []domain.SunburstSegment{}}
	if err := validateDate(from, "from"); err != nil {
		return out, err
	}
	if err := validateDate(to, "to"); err != nil {
		return out, err
	}
	if to < from {
		return out, validationError("to %q must not be before from %q", to, from)
	}
	base, conv, err := s.baseCurrencyAndConverter(ctx)
	if err != nil {
		return out, err
	}
	out.Currency = base

	rows, err := s.store.CategorySpend(ctx, from, to)
	if err != nil {
		return out, err
	}

	type catAgg struct {
		id    int64
		name  string
		cents int64
	}
	cats := map[int64]*catAgg{}
	sections := map[int64]string{} // category → section label
	var grand int64
	for _, r := range rows {
		cents := conv.add(r.Currency, r.TotalCents)
		if cats[r.CategoryID] == nil {
			cats[r.CategoryID] = &catAgg{id: r.CategoryID, name: r.Name}
		}
		cats[r.CategoryID].cents += cents
		sections[r.CategoryID] = sunburstSection(r.Section)
		grand += cents
	}
	if grand <= 0 {
		out.ConversionWarnings = conv.warnings()
		return out, nil
	}

	// Collect categories per section, largest first.
	bySection := map[string][]*catAgg{}
	sectionTotals := map[string]int64{}
	for id, c := range cats {
		section := sections[id]
		bySection[section] = append(bySection[section], c)
		sectionTotals[section] += c.cents
	}
	for section := range bySection {
		cs := bySection[section]
		sort.Slice(cs, func(i, j int) bool { return cs[i].cents > cs[j].cents })
	}

	// Inner ring: fold small sections into "Other", cap the arc count.
	type segName struct{ name string }
	sectionNames := make([]string, 0, len(sectionTotals))
	for name := range sectionTotals {
		sectionNames = append(sectionNames, name)
	}
	sort.Slice(sectionNames, func(i, j int) bool { return sectionTotals[sectionNames[i]] > sectionTotals[sectionNames[j]] })

	other := &domain.SunburstSegment{Section: "Other", Children: []domain.SunburstLeaf{}}
	kept := 0
	for _, name := range sectionNames {
		seg := &domain.SunburstSegment{
			Section:    name,
			TotalCents: sectionTotals[name],
			Share:      float64(sectionTotals[name]) / float64(grand),
		}
		if seg.Share < sunburstFoldShare || kept >= sunburstMaxSegments {
			other.TotalCents += seg.TotalCents
			for _, c := range bySection[name] {
				other.Children = append(other.Children, domain.SunburstLeaf{
					CategoryID: c.id, Name: c.name, TotalCents: c.cents, Share: float64(c.cents) / float64(grand),
				})
			}
			continue
		}
		// Outer ring: fold small categories into the segment's "Other" leaf.
		leafOther := &domain.SunburstLeaf{Name: "Other"}
		for i, c := range bySection[name] {
			leaf := domain.SunburstLeaf{CategoryID: c.id, Name: c.name, TotalCents: c.cents, Share: float64(c.cents) / float64(grand)}
			if leaf.Share < sunburstFoldShare || i >= sunburstMaxSegments {
				leafOther.TotalCents += leaf.TotalCents
				continue
			}
			seg.Children = append(seg.Children, leaf)
		}
		if leafOther.TotalCents > 0 {
			seg.Children = append(seg.Children, *leafOther)
		}
		out.Segments = append(out.Segments, *seg)
		kept++
	}
	if other.TotalCents > 0 {
		sort.Slice(other.Children, func(i, j int) bool { return other.Children[i].TotalCents > other.Children[j].TotalCents })
		out.Segments = append(out.Segments, *other)
	}
	out.TotalCents = grand
	out.ConversionWarnings = conv.warnings()
	return out, nil
}

// sunburstSection labels the inner ring; an empty section folds into "Other".
func sunburstSection(section string) string {
	if section == "" {
		return "Other"
	}
	return section
}

// --- Chart 3: Monthly Run Rate vs. Rolling Budget ---------------------------

// RunRate builds day-by-day cumulative spend for one month plus references:
// the previous month's cumulative curve, the 3-month average curve, and a
// linear budget pace line from the open envelope total. Points after today
// (current month) or after a reference month's last day are nil, so the
// frontend renders breaks, not zeros. Note that budgets are lifetime
// envelopes since 0015 — the pace line is a target, not a monthly cap.
func (s *AnalyticsService) RunRate(ctx context.Context, month string) (domain.RunRate, error) {
	out := domain.RunRate{Month: month, Points: []domain.RunRatePoint{}}
	if err := validateMonth(month, "month"); err != nil {
		return out, err
	}
	first, err := timeParseDate(month + "-01")
	if err != nil {
		return out, validationError("month %q is not a real month", month)
	}
	daysInMonth := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	out.DaysInMonth = daysInMonth

	months := monthWindowList(month, 4) // current + 3 previous
	base, conv, err := s.baseCurrencyAndConverter(ctx)
	if err != nil {
		return out, err
	}
	out.Currency = base

	rows, err := s.store.DailyExpenseByMonth(ctx, months)
	if err != nil {
		return out, err
	}

	// Cumulative sums per month (converted), zero-filled to each month's length.
	cum := map[string][]int64{}
	for _, m := range months {
		cum[m] = make([]int64, daysInMonthOf(m))
	}
	for _, r := range rows {
		cents := conv.add(r.Currency, r.AmountCents)
		for d := r.Day; d <= len(cum[r.Month]); d++ {
			cum[r.Month][d-1] += cents
		}
	}

	budgetTotal, err := s.store.OpenBudgetTotal(ctx)
	if err != nil {
		return out, err
	}
	out.BudgetTotalCents = budgetTotal

	today := time.Now().UTC()
	isCurrentMonth := month == today.Format("2006-01")
	for day := 1; day <= daysInMonth; day++ {
		point := domain.RunRatePoint{Day: day}

		// Current month: cut off after today; past months render in full.
		if day <= daysInMonthOf(month) && (!isCurrentMonth || day <= today.Day()) {
			v := cum[month][day-1]
			point.CurrentCents = &v
		}
		// Previous month reference: only up to that month's length.
		if prev := months[len(months)-2]; day <= daysInMonthOf(prev) {
			v := cum[prev][day-1]
			point.PreviousCents = &v
		}
		// 3-month average: only over the reference months that have this day.
		var sum int64
		var n int64
		for _, m := range months[1:] {
			if day <= daysInMonthOf(m) {
				sum += cum[m][day-1]
				n++
			}
		}
		if n > 0 {
			avg := (sum + n/2) / n
			point.AverageCents = &avg
		}
		// Linear budget pace across the whole month.
		if budgetTotal > 0 {
			pace := (budgetTotal*int64(day) + int64(daysInMonth)/2) / int64(daysInMonth)
			point.BudgetPaceCents = &pace
		}
		out.Points = append(out.Points, point)
	}
	out.ConversionWarnings = conv.warnings()
	return out, nil
}

// --- Chart 4: Personal Consumer Price Index ---------------------------------

// PriceIndex tracks the median per-base-unit price of the top staples over
// time, indexed to 100 at each series' first month with data. Medians are
// computed here (SQLite has no percentile); series need purchases in at least
// minMonthsForSeries distinct months to qualify, ranked by purchase count.
func (s *AnalyticsService) PriceIndex(ctx context.Context, from, to string) (domain.PriceIndex, error) {
	out := domain.PriceIndex{From: from, To: to, Series: []domain.PriceIndexSeries{}, Average: []domain.PriceIndexPoint{}}
	if err := validateDate(from, "from"); err != nil {
		return out, err
	}
	if err := validateDate(to, "to"); err != nil {
		return out, err
	}
	if to < from {
		return out, validationError("to %q must not be before from %q", to, from)
	}
	base, conv, err := s.baseCurrencyAndConverter(ctx)
	if err != nil {
		return out, err
	}
	out.Currency = base

	rows, err := s.store.ItemPrices(ctx, from, to)
	if err != nil {
		return out, err
	}

	months := monthRangeList(from, to)

	// key → month → converted prices; plus a purchase counter for ranking.
	type seriesAgg struct {
		unit    string
		byMonth map[string][]float64
		count   int
	}
	series := map[productKey]*seriesAgg{}
	for _, r := range rows {
		key := productKey{name: r.Key, unit: r.BaseUnit}
		agg := series[key]
		if agg == nil {
			agg = &seriesAgg{unit: r.BaseUnit, byMonth: map[string][]float64{}}
			series[key] = agg
		}
		month := r.Date[:7]
		agg.byMonth[month] = append(agg.byMonth[month], conv.addFloat(r.Currency, r.PricePerUnit))
		agg.count++
	}

	// Qualify (≥ minMonthsForSeries distinct months) and rank by frequency.
	type candidate struct {
		key    productKey
		agg    *seriesAgg
		months int
	}
	var qualified []candidate
	for key, agg := range series {
		if len(agg.byMonth) >= minMonthsForSeries {
			qualified = append(qualified, candidate{key: key, agg: agg, months: len(agg.byMonth)})
		}
	}
	sort.Slice(qualified, func(i, j int) bool {
		if qualified[i].agg.count != qualified[j].agg.count {
			return qualified[i].agg.count > qualified[j].agg.count
		}
		return qualified[i].key.name < qualified[j].key.name
	})
	if len(qualified) > topSeriesCount {
		qualified = qualified[:topSeriesCount]
	}
	if len(qualified) == 0 {
		out.ConversionWarnings = conv.warnings()
		return out, nil
	}

	// Build indexed series. Each series is indexed against its own first
	// month with data so a mid-window staple still reads correctly.
	out.BaseMonth = months[0]
	for _, m := range months {
		found := false
		for _, cand := range qualified {
			if _, ok := cand.agg.byMonth[m]; ok {
				found = true
				break
			}
		}
		if found {
			out.BaseMonth = m
			break
		}
	}
	type indexed struct {
		label    string
		unit     string
		purchase int
		indices  map[string]float64 // month → index
	}
	var built []indexed
	for _, cand := range qualified {
		firstPriceMonth := ""
		medians := map[string]float64{}
		for _, m := range months {
			if ps, ok := cand.agg.byMonth[m]; ok {
				medians[m] = median(ps)
				if firstPriceMonth == "" {
					firstPriceMonth = m
				}
			}
		}
		basePrice := medians[firstPriceMonth]
		if basePrice <= 0 {
			continue
		}
		indices := make(map[string]float64, len(medians))
		for m, p := range medians {
			indices[m] = round1(p / basePrice * 100)
		}
		built = append(built, indexed{label: cand.key.name, unit: cand.agg.unit, purchase: cand.agg.count, indices: indices})
	}

	for _, b := range built {
		s := domain.PriceIndexSeries{Label: b.label, BaseUnit: b.unit, PurchaseCount: b.purchase, Points: []domain.PriceIndexPoint{}}
		for _, m := range months {
			point := domain.PriceIndexPoint{Month: m}
			if idx, ok := b.indices[m]; ok {
				v := idx
				point.Index = &v
			}
			s.Points = append(s.Points, point)
		}
		out.Series = append(out.Series, s)
	}

	// Average line: mean of available series' indices per month.
	for _, m := range months {
		point := domain.PriceIndexPoint{Month: m}
		sum := 0.0
		n := 0
		for _, b := range built {
			if v, ok := b.indices[m]; ok {
				sum += v
				n++
			}
		}
		if n > 0 {
			avg := round1(sum / float64(n))
			point.Index = &avg
		}
		out.Average = append(out.Average, point)
	}
	out.ConversionWarnings = conv.warnings()
	return out, nil
}

// --- Chart 5: Day-of-Week × Purchase-Type Heatmap ---------------------------

// Heatmap groups: grocery sections, everyday essentials, everything else.
// The mapping lives here (not in a migration) so it stays a presentation
// concern over the existing category sections.
var heatmapGrocerySections = map[string]bool{
	"Fridge": true, "Freezer": true, "Pantry & Cupboards": true, "Beverages": true,
	"Sweets & Chocolate": true, "Fruits": true, "Vegetables": true, "Root Vegetables": true,
	"Meats": true, "Seafood": true, "Dairy & Eggs": true, "Coffee & Tea": true,
	"Wine": true, "Beer": true, "Spirits & Liqueurs": true, "Water & Iced Tea": true,
	"Juice": true, "Salty Snacks": true, "Spices & Baking": true, "Breakfast & Spreads": true,
	"Canned & Jarred": true, "Oils & Vinegars": true, "Pasta, Rice & Grains": true,
	"Nuts & Dried Fruits": true, "Milk Drinks & Alternatives": true, "Deli & Ready-to-Eat": true,
}

var heatmapEssentialSections = map[string]bool{
	"Cleaning & Dish": true, "Food Wrap & Storage": true, "Paper Goods": true,
	"Non-Food Essentials": true, "Pharmacy & Health": true, "Beauty & Personal Care": true,
	"Baby & Kids": true, "Pets": true, "Home & Hardware": true, "Vehicle & Fuel": true,
	"Utilities": true, "Vehicle & Transport": true, "Housing & Home": true, "Health & Wellness": true,
}

const (
	heatmapGroupGroceries  = "Groceries"
	heatmapGroupEssentials = "Household & Essentials"
	heatmapGroupOther      = "Other"
)

// heatmapGroup maps a category section to its purchase-type group.
func heatmapGroup(section string) string {
	switch {
	case heatmapGrocerySections[section]:
		return heatmapGroupGroceries
	case heatmapEssentialSections[section]:
		return heatmapGroupEssentials
	default:
		return heatmapGroupOther
	}
}

// SpendHeatmap sums accepted bill lines into a weekday × purchase-type grid.
// Cells are Monday-first and zero-filled; MaxCents anchors the color scale.
func (s *AnalyticsService) SpendHeatmap(ctx context.Context, from, to string) (domain.SpendHeatmap, error) {
	out := domain.SpendHeatmap{
		From:   from,
		To:     to,
		Groups: []string{heatmapGroupGroceries, heatmapGroupEssentials, heatmapGroupOther},
		Rows:   []domain.HeatmapRow{},
	}
	if err := validateDate(from, "from"); err != nil {
		return out, err
	}
	if err := validateDate(to, "to"); err != nil {
		return out, err
	}
	if to < from {
		return out, validationError("to %q must not be before from %q", to, from)
	}
	base, conv, err := s.baseCurrencyAndConverter(ctx)
	if err != nil {
		return out, err
	}
	out.Currency = base

	rows, err := s.store.Heatmap(ctx, from, to)
	if err != nil {
		return out, err
	}

	totals := map[string][7]int64{} // group → dow → cents
	for _, r := range rows {
		group := heatmapGroup(r.Section)
		cell := totals[group]
		if r.Dow >= 0 && r.Dow < 7 {
			cell[r.Dow] += conv.add(r.Currency, r.TotalCents)
		}
		totals[group] = cell
	}
	for _, group := range out.Groups {
		cells := totals[group]
		row := domain.HeatmapRow{Group: group, Cells: make([]domain.HeatmapCell, 7)}
		for d := 0; d < 7; d++ {
			row.Cells[d] = domain.HeatmapCell{TotalCents: cells[d]}
			if cells[d] > out.MaxCents {
				out.MaxCents = cells[d]
			}
		}
		out.Rows = append(out.Rows, row)
	}
	out.ConversionWarnings = conv.warnings()
	return out, nil
}

// --- Chart 6: Fixed vs. Discretionary Cash Flow Split -----------------------

// FixedSplit splits monthly expense transactions into fixed commitments
// (categories with is_fixed) and discretionary spending, over the months in
// the range. Unclassified spend (no category — how bill transactions are
// created) counts as discretionary but is reported separately for a footnote.
func (s *AnalyticsService) FixedSplit(ctx context.Context, from, to string) (domain.FixedSplit, error) {
	out := domain.FixedSplit{From: from, To: to, Months: []domain.FixedSplitMonth{}}
	if err := validateDate(from, "from"); err != nil {
		return out, err
	}
	if err := validateDate(to, "to"); err != nil {
		return out, err
	}
	if to < from {
		return out, validationError("to %q must not be before from %q", to, from)
	}
	base, conv, err := s.baseCurrencyAndConverter(ctx)
	if err != nil {
		return out, err
	}
	out.Currency = base

	months := monthRangeList(from, to)
	rows, err := s.store.SpendByMonthAndFixed(ctx, months)
	if err != nil {
		return out, err
	}

	fixed := map[string]int64{}
	discretionary := map[string]int64{}
	unclassified := map[string]int64{}
	for _, r := range rows {
		cents := conv.add(r.Currency, r.TotalCents)
		switch {
		case r.Unclassified:
			unclassified[r.Month] += cents
			discretionary[r.Month] += cents // still discretionary in the stack
		case r.IsFixed:
			fixed[r.Month] += cents
		default:
			discretionary[r.Month] += cents
		}
	}
	for _, m := range months {
		f, d := fixed[m], discretionary[m]
		out.Months = append(out.Months, domain.FixedSplitMonth{
			Month:              m,
			FixedCents:         f,
			DiscretionaryCents: d,
			TotalCents:         f + d,
		})
		out.UnclassifiedCents += unclassified[m]
	}
	out.ConversionWarnings = conv.warnings()
	return out, nil
}

// --- Month helpers -----------------------------------------------------------

// monthWindowOf returns the month m shifted by offset months as YYYY-MM.
func monthWindowOf(m string, offset int) string {
	t, err := time.Parse("2006-01", m)
	if err != nil {
		return m
	}
	return t.AddDate(0, offset, 0).Format("2006-01")
}

// daysInMonthOf returns the number of days in month YYYY-MM.
func daysInMonthOf(m string) int {
	t, err := time.Parse("2006-01", m)
	if err != nil {
		return 30
	}
	return time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// monthWindowList returns n months ending at m, oldest first.
func monthWindowList(m string, n int) []string {
	out := make([]string, 0, n)
	for i := n - 1; i >= 0; i-- {
		out = append(out, monthWindowOf(m, -i))
	}
	return out
}

// monthRangeList returns the calendar months covered by an inclusive
// YYYY-MM-DD range, oldest first.
func monthRangeList(from, to string) []string {
	first, err1 := time.Parse("2006-01", from[:7])
	last, err2 := time.Parse("2006-01", to[:7])
	if err1 != nil || err2 != nil || last.Before(first) {
		return []string{from[:7]}
	}
	out := []string{}
	for t := first; !t.After(last); t = t.AddDate(0, 1, 0) {
		out = append(out, t.Format("2006-01"))
	}
	return out
}

// mean averages a float slice; returns 0 for empty input.
func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// median computes the middle value (mean of the two middles for even counts).
// SQLite has no percentile function, so item-price medians are computed here.
func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// round1 rounds to one decimal place, the index resolution used on the wire.
func round1(v float64) float64 { return math.Round(v*10) / 10 }
