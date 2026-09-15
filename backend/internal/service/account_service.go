package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// AccountService implements account business rules.
type AccountService struct{ accounts AccountStore }

// AccountInput is the user-facing payload for create/update.
type AccountInput struct {
	Name           string
	Type           domain.AccountType
	Currency       string
	BalanceCents   int64
	CardLastDigits string // optional; last digits for bill matching
}

func (s *AccountService) List(ctx context.Context) ([]domain.Account, error) {
	return s.accounts.List(ctx)
}

func (s *AccountService) Get(ctx context.Context, id int64) (domain.Account, error) {
	return s.accounts.GetByID(ctx, id)
}

func (s *AccountService) Create(ctx context.Context, in AccountInput) (domain.Account, error) {
	a, err := s.build(in)
	if err != nil {
		return domain.Account{}, err
	}
	out, err := s.accounts.Create(ctx, a)
	if err != nil {
		return domain.Account{}, err
	}
	return out, nil
}

func (s *AccountService) Update(ctx context.Context, id int64, in AccountInput) (domain.Account, error) {
	a, err := s.build(in)
	if err != nil {
		return domain.Account{}, err
	}
	a.ID = id
	out, err := s.accounts.Update(ctx, a)
	if err != nil {
		return domain.Account{}, err
	}
	return out, nil
}

func (s *AccountService) Delete(ctx context.Context, id int64) error {
	return s.accounts.Delete(ctx, id)
}

func (s *AccountService) build(in AccountInput) (domain.Account, error) {
	if err := validateRequiredString(in.Name, "name", 100); err != nil {
		return domain.Account{}, err
	}
	if !in.Type.Valid() {
		return domain.Account{}, validationError("type %q must be one of checking, savings, credit, cash, other", in.Type)
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = DefaultBaseCurrency
	} else if cur, err := normalizeCurrency(in.Currency); err != nil {
		return domain.Account{}, err
	} else {
		currency = cur
	}
	if err := validateNonNegativeCents(in.BalanceCents, "balance_cents"); err != nil {
		return domain.Account{}, err
	}
	cardDigits := nonDigitsAccount.ReplaceAllString(strings.TrimSpace(in.CardLastDigits), "")
	if cardDigits != "" && len(cardDigits) != 4 {
		return domain.Account{}, validationError("card_last_digits %q must be exactly 4 digits", in.CardLastDigits)
	}
	return domain.Account{
		Name:           strings.TrimSpace(in.Name),
		Type:           in.Type,
		Currency:       currency,
		BalanceCents:   in.BalanceCents,
		CardLastDigits: cardDigits,
	}, nil
}

// nonDigitsAccount strips anything that is not a digit from card numbers.
var nonDigitsAccount = regexp.MustCompile(`\D`)

// CategoryService implements category business rules.
type CategoryService struct{ categories CategoryStore }

func (s *CategoryService) List(ctx context.Context) ([]domain.Category, error) {
	return s.categories.List(ctx)
}

// CategoryInput is the user-facing payload for create.
type CategoryInput struct {
	Name string
}

func (s *CategoryService) Create(ctx context.Context, in CategoryInput) (domain.Category, error) {
	if err := validateRequiredString(in.Name, "name", 60); err != nil {
		return domain.Category{}, err
	}
	c, err := s.categories.Create(ctx, domain.Category{Name: strings.TrimSpace(in.Name)})
	if err != nil {
		return domain.Category{}, err
	}
	return c, nil
}

func (s *CategoryService) Delete(ctx context.Context, id int64) error {
	return s.categories.Delete(ctx, id)
}

// TransactionService implements transaction business rules.
type TransactionService struct {
	transactions TransactionStore
	accounts     AccountStore
	categories   CategoryStore
	stores       StoreStore
	products     ProductStore
	log          *slog.Logger
}

// maxTransactionItems caps the item lines of one manual transaction, like the
// bill editor's sanity bound.
const maxTransactionItems = 200

// TransactionInput is the user-facing payload for create/update.
type TransactionInput struct {
	AccountID   int64
	CategoryID  *int64
	Kind        domain.TransactionKind
	AmountCents int64
	Description string
	Date        string
	// StoreName is the market of a manual purchase (find-or-created like a
	// bill's market name). Ignored for bill-linked rows, which keep their
	// store on the bill.
	StoreName string
	// Items are the article lines of a manually entered purchase; empty for a
	// plain amount-only transaction. Only expenses may carry them.
	Items []TransactionItemInput
}

// TransactionItemInput is one item line of the user-facing payload.
type TransactionItemInput struct {
	Name           string
	Brand          string
	Unit           string
	CategoryID     *int64
	Quantity       float64
	UnitPriceCents int64
	DiscountCents  int64
}

func (s *TransactionService) List(ctx context.Context, f TransactionFilters) ([]domain.Transaction, error) {
	if f.Month != "" {
		if err := validateMonth(f.Month, "month"); err != nil {
			return nil, err
		}
	}
	return s.transactions.List(ctx, f)
}

func (s *TransactionService) Get(ctx context.Context, id int64) (domain.Transaction, error) {
	return s.transactions.GetByID(ctx, id)
}

func (s *TransactionService) Create(ctx context.Context, in TransactionInput) (domain.Transaction, error) {
	t, err := s.build(ctx, in)
	if err != nil {
		return domain.Transaction{}, err
	}
	if len(t.Items) > 0 {
		return s.transactions.CreateWithItems(ctx, t, t.Items)
	}
	return s.transactions.Create(ctx, t)
}

func (s *TransactionService) Update(ctx context.Context, id int64, in TransactionInput) (domain.Transaction, error) {
	// A bill's transaction is a mirror of the bill row — it changes only in
	// the bills view.
	if err := s.guardNotBill(ctx, id); err != nil {
		return domain.Transaction{}, err
	}
	t, err := s.build(ctx, in)
	if err != nil {
		return domain.Transaction{}, err
	}
	t.ID = id
	if len(t.Items) > 0 {
		return s.transactions.UpdateWithItems(ctx, t, t.Items)
	}
	return s.transactions.Update(ctx, t)
}

func (s *TransactionService) Delete(ctx context.Context, id int64) error {
	// A bill's transaction dies with its bill (BillService.Delete) — deleting
	// it from here would desync the bill and the reports.
	if err := s.guardNotBill(ctx, id); err != nil {
		return err
	}
	return s.transactions.Delete(ctx, id)
}

// guardNotBill refuses writes to a transaction that was recorded for a
// scanned bill: it is readonly, edited through the bill it mirrors.
func (s *TransactionService) guardNotBill(ctx context.Context, id int64) error {
	existing, err := s.transactions.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if existing.BillID != nil {
		return conflictError(
			"this transaction was recorded for bill %d — edit or delete it in the bills view",
			*existing.BillID)
	}
	return nil
}

func (s *TransactionService) build(ctx context.Context, in TransactionInput) (domain.Transaction, error) {
	if in.AccountID <= 0 {
		return domain.Transaction{}, validationError("account_id must be a positive id")
	}
	if err := validatePositiveCents(in.AmountCents, "amount_cents"); err != nil {
		return domain.Transaction{}, err
	}
	if !in.Kind.Valid() {
		return domain.Transaction{}, validationError("kind %q must be income or expense", in.Kind)
	}
	if len(in.Items) > 0 && in.Kind != domain.TransactionExpense {
		return domain.Transaction{}, validationError("items are only supported on expense transactions")
	}
	if len(in.Items) > maxTransactionItems {
		return domain.Transaction{}, validationError("at most %d items are supported", maxTransactionItems)
	}
	if err := validateDate(in.Date, "date"); err != nil {
		return domain.Transaction{}, err
	}
	if len(in.Description) > 500 {
		return domain.Transaction{}, validationError("description must be at most 500 characters")
	}
	// Referential checks: a transaction must point at real rows.
	acc, err := s.accounts.GetByID(ctx, in.AccountID)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("validate account_id: %w", err)
	}
	if in.CategoryID != nil {
		if _, err := s.categories.GetByID(ctx, *in.CategoryID); err != nil {
			return domain.Transaction{}, fmt.Errorf("validate category_id: %w", err)
		}
	}

	// Item lines, mirroring buildBill's per-line rules minus budget
	// attribution: deposit returns ("Leergut") and lines filed under a
	// category with allows_negative (the seeded "Deposit & Returns" / Pfand
	// family) are money back — their unit price and line total may be
	// negative and reduce the items total.
	items := make([]domain.TransactionItem, 0, len(in.Items))
	itemsTotal := int64(0)
	for _, it := range in.Items {
		name := strings.TrimSpace(it.Name)
		if name == "" {
			return domain.Transaction{}, validationError("every item needs a name")
		}
		if it.Quantity <= 0 {
			return domain.Transaction{}, validationError("quantity for %q must be positive", name)
		}
		// The negative rule needs the line's category, so it is resolved
		// before the price checks — like buildBill.
		allowsNegative := isDepositReturn(name)
		if it.CategoryID != nil {
			cat, err := s.categories.GetByID(ctx, *it.CategoryID)
			if err != nil {
				return domain.Transaction{}, fmt.Errorf("validate category for %q: %w", name, err)
			}
			allowsNegative = allowsNegative || cat.AllowsNegative
		}
		if (!allowsNegative && it.UnitPriceCents < 0) || (!allowsNegative && it.DiscountCents < 0) {
			return domain.Transaction{}, validationError("prices for %q must not be negative", name)
		}
		line := it.Quantity*float64(it.UnitPriceCents) - float64(it.DiscountCents)
		lineCents := int64(math.Round(line))
		if lineCents < 0 && !allowsNegative {
			lineCents = 0
		}
		itemsTotal += lineCents
		items = append(items, domain.TransactionItem{
			Name:           name,
			Brand:          strings.TrimSpace(it.Brand),
			Unit:           strings.ToLower(strings.TrimSpace(it.Unit)),
			CategoryID:     it.CategoryID,
			Quantity:       it.Quantity,
			UnitPriceCents: it.UnitPriceCents,
			DiscountCents:  it.DiscountCents,
			LineTotalCents: lineCents,
		})
	}

	storeID, err := s.resolveTransactionStore(ctx, in.StoreName)
	if err != nil {
		return domain.Transaction{}, err
	}

	// Link each line to its catalogue product, find-or-created on first use —
	// the same lenient rule as the bill flow: a product-link problem is logged
	// and the line stays unlinked, never blocking the financial record.
	// Money-back lines (Leergut / negative prices) stay unlinked, like bill
	// returns, so purchase stats and store prices stay about real purchases.
	if s.log == nil {
		s.log = slog.Default()
	}
	for i := range items {
		if items[i].UnitPriceCents < 0 || isDepositReturn(items[i].Name) {
			continue
		}
		items[i].ProductID = s.resolveTransactionProduct(ctx, items[i])
	}

	return domain.Transaction{
		AccountID:       in.AccountID,
		CategoryID:      in.CategoryID,
		Kind:            in.Kind,
		AmountCents:     in.AmountCents,
		Currency:        acc.Currency, // manual rows are always in the account's currency
		Description:     strings.TrimSpace(in.Description),
		Date:            in.Date,
		StoreID:         storeID,
		ItemsTotalCents: itemsTotal,
		Items:           items,
	}, nil
}

// resolveTransactionStore find-or-creates the store for a manual purchase,
// matched case-insensitively on the trimmed name — the same shape as the
// bill flow's resolveStore, race-safe through the unique name index.
func (s *TransactionService) resolveTransactionStore(ctx context.Context, market string) (*int64, error) {
	name := strings.TrimSpace(market)
	if name == "" || s.stores == nil {
		return nil, nil
	}
	store, err := s.stores.FindByName(ctx, name)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		store, err = s.stores.Create(ctx, domain.Store{Name: name})
		if errors.Is(err, domain.ErrConflict) {
			// Lost a race against a concurrent write — re-read the winner.
			store, err = s.stores.FindByName(ctx, name)
		}
		if err != nil {
			return nil, fmt.Errorf("create store %q: %w", name, err)
		}
	case err != nil:
		return nil, fmt.Errorf("find store: %w", err)
	}
	id := store.ID
	return &id, nil
}

// resolveTransactionProduct links a manual item line to its catalogue
// product, find-or-created case-insensitively from the line name (CategoryID
// seeds a new product). Failures are non-fatal (logged, link left nil) — a
// catalogue problem must not block recording the purchase; the next save
// retries.
func (s *TransactionService) resolveTransactionProduct(ctx context.Context, it domain.TransactionItem) *int64 {
	if s.products == nil {
		return nil
	}
	name := strings.TrimSpace(it.Name)
	if name == "" {
		return nil
	}
	product, err := s.products.FindByName(ctx, name)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		product, err = s.products.Create(ctx, domain.Product{
			Name:       name,
			Brand:      it.Brand,
			Unit:       it.Unit,
			CategoryID: it.CategoryID,
		})
		if errors.Is(err, domain.ErrConflict) {
			// Lost a race — re-read the winner.
			product, err = s.products.FindByName(ctx, name)
		}
		if err != nil {
			s.log.Warn("create product from transaction item", "name", name, "error", err)
			return nil
		}
	case err != nil:
		s.log.Warn("find product for transaction item", "name", name, "error", err)
		return nil
	}
	id := product.ID
	return &id
}

// BudgetService implements budget business rules. Budgets are open-ended
// envelopes: no period, open until manually closed.
type BudgetService struct {
	budgets    BudgetStore
	categories CategoryStore
}

// BudgetInput is the user-facing payload for create/update.
type BudgetInput struct {
	CategoryID  int64
	AmountCents int64
}

// List returns budgets, optionally filtered by lifecycle status
// ("" = all, "open", "closed").
func (s *BudgetService) List(ctx context.Context, status string) ([]domain.Budget, error) {
	budgets, err := s.budgets.List(ctx)
	if err != nil {
		return nil, err
	}
	if status == "" {
		return budgets, nil
	}
	lc := domain.BudgetLifecycle(status)
	if !lc.Valid() {
		return nil, validationError("status %q must be open or closed", status)
	}
	out := make([]domain.Budget, 0, len(budgets))
	for _, b := range budgets {
		if b.Status == lc {
			out = append(out, b)
		}
	}
	return out, nil
}

func (s *BudgetService) Create(ctx context.Context, in BudgetInput) (domain.Budget, error) {
	b, err := s.build(ctx, in)
	if err != nil {
		return domain.Budget{}, err
	}
	out, err := s.budgets.Create(ctx, b)
	if err != nil {
		return domain.Budget{}, err
	}
	return out, nil
}

// Update changes only a budget's amount (category is immutable).
func (s *BudgetService) Update(ctx context.Context, id int64, amountCents int64) (domain.Budget, error) {
	if err := validatePositiveCents(amountCents, "amount_cents"); err != nil {
		return domain.Budget{}, err
	}
	existing, err := s.budgets.GetByID(ctx, id)
	if err != nil {
		return domain.Budget{}, err
	}
	existing.AmountCents = amountCents
	return s.budgets.Update(ctx, existing)
}

// SetStatus marks an envelope finished (closed) or reopens it. Closing
// stamps closed_at; reopening clears it.
func (s *BudgetService) SetStatus(ctx context.Context, id int64, status domain.BudgetLifecycle) (domain.Budget, error) {
	if !status.Valid() {
		return domain.Budget{}, validationError("status %q must be open or closed", status)
	}
	existing, err := s.budgets.GetByID(ctx, id)
	if err != nil {
		return domain.Budget{}, err
	}
	existing.Status = status
	if status == domain.BudgetClosed {
		now := time.Now().UTC()
		existing.ClosedAt = &now
	} else {
		existing.ClosedAt = nil
	}
	return s.budgets.Update(ctx, existing)
}

func (s *BudgetService) Delete(ctx context.Context, id int64) error {
	return s.budgets.Delete(ctx, id)
}

func (s *BudgetService) build(ctx context.Context, in BudgetInput) (domain.Budget, error) {
	if in.CategoryID <= 0 {
		return domain.Budget{}, validationError("category_id must be a positive id")
	}
	if err := validatePositiveCents(in.AmountCents, "amount_cents"); err != nil {
		return domain.Budget{}, err
	}
	if _, err := s.categories.GetByID(ctx, in.CategoryID); err != nil {
		return domain.Budget{}, fmt.Errorf("validate category_id: %w", err)
	}
	return domain.Budget{
		CategoryID:  in.CategoryID,
		AmountCents: in.AmountCents,
		Status:      domain.BudgetOpen,
	}, nil
}

// SummaryService implements dashboard aggregates, converting every total
// into the user's base currency.
type SummaryService struct {
	summary    SummaryStore
	settings   *SettingsService
	rates      RateSource
	categories CategoryStore
}

// MonthSummary returns the dashboard aggregate for one calendar month
// ("YYYY-MM"); it maps the month onto its first/last day and delegates to
// RangeSummary.
func (s *SummaryService) MonthSummary(ctx context.Context, month string) (domain.Summary, error) {
	if err := validateMonth(month, "month"); err != nil {
		return domain.Summary{}, err
	}
	first, err := timeParseDate(month + "-01")
	if err != nil {
		return domain.Summary{}, validationError("month %q is not a real month", month)
	}
	last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	return s.RangeSummary(ctx, first.Format("2006-01-02"), last.Format("2006-01-02"))
}

// RangeSummary returns the dashboard aggregate for an inclusive date range
// ("YYYY-MM-DD"). Budget progress covers open envelopes plus any closed one
// with attributed spend inside the range.
func (s *SummaryService) RangeSummary(ctx context.Context, from, to string) (domain.Summary, error) {
	if err := validateDate(from, "from"); err != nil {
		return domain.Summary{}, err
	}
	if err := validateDate(to, "to"); err != nil {
		return domain.Summary{}, err
	}
	if to < from {
		return domain.Summary{}, validationError("to %q must not be before from %q", to, from)
	}
	base, err := s.settings.BaseCurrency(ctx)
	if err != nil {
		return domain.Summary{}, err
	}
	snap, err := s.rates.Snapshot(ctx)
	if err != nil {
		return domain.Summary{}, err
	}
	raw, err := s.summary.RawRangeSummary(ctx, from, to)
	if err != nil {
		return domain.Summary{}, err
	}
	conv := newConverter(base, snap)

	out := domain.Summary{
		From:               from,
		To:                 to,
		Currency:           base,
		ConversionWarnings: []string{},
	}
	for _, a := range raw.Income {
		out.IncomeCents += conv.add(a.Currency, a.Cents)
	}
	for _, a := range raw.Expense {
		out.ExpenseCents += conv.add(a.Currency, a.Cents)
	}
	out.NetCents = out.IncomeCents - out.ExpenseCents
	for _, a := range raw.Balances {
		out.TotalBalanceCents += conv.add(a.Currency, a.Cents)
	}

	// Daily expenses: merge per-currency rows by date, date order.
	daily := map[string]*domain.DayTotal{}
	for _, d := range raw.DailyExpenses {
		day, ok := daily[d.Date]
		if !ok {
			day = &domain.DayTotal{Date: d.Date}
			daily[d.Date] = day
		}
		day.ExpenseCents += conv.add(d.Currency, d.ExpenseCents)
	}
	out.DailyExpenses = make([]domain.DayTotal, 0, len(daily))
	for _, day := range daily {
		out.DailyExpenses = append(out.DailyExpenses, *day)
	}
	sort.Slice(out.DailyExpenses, func(i, j int) bool { return out.DailyExpenses[i].Date < out.DailyExpenses[j].Date })

	// Category spend: merge per-currency rows, resolve names, top 5.
	categorySpend := map[int64]int64{}
	for _, c := range raw.CategorySpend {
		categorySpend[c.CategoryID] += conv.add(c.Currency, c.TotalCents)
	}
	names := map[int64]string{}
	if len(categorySpend) > 0 {
		cats, err := s.categories.List(ctx)
		if err != nil {
			return domain.Summary{}, fmt.Errorf("summary category names: %w", err)
		}
		for _, c := range cats {
			names[c.ID] = c.Name
		}
	}
	out.TopCategories = make([]domain.CategoryTotal, 0, len(categorySpend))
	for cat, total := range categorySpend {
		out.TopCategories = append(out.TopCategories, domain.CategoryTotal{
			CategoryID:   cat,
			CategoryName: names[cat],
			TotalCents:   total,
		})
	}
	sort.Slice(out.TopCategories, func(i, j int) bool { return out.TopCategories[i].TotalCents > out.TopCategories[j].TotalCents })
	if len(out.TopCategories) > 5 {
		out.TopCategories = out.TopCategories[:5]
	}

	// Budget progress: in-range spend is transaction-category spend plus
	// bill-line spend attributed to the budget (per-line overrides included);
	// the envelope balance is lifetime spend against the amount. Open
	// envelopes are always reported; closed ones only when they had
	// attributed activity inside the range.
	billSpend := map[int64]int64{}
	for _, b := range raw.BillBudgetSpend {
		billSpend[b.BudgetID] += conv.add(b.Currency, b.Cents)
	}
	lifetimeBillSpend := map[int64]int64{}
	for _, b := range raw.LifetimeBillBudgetSpend {
		lifetimeBillSpend[b.BudgetID] += conv.add(b.Currency, b.Cents)
	}
	lifetimeCategorySpend := map[int64]int64{}
	for _, c := range raw.LifetimeCategorySpend {
		lifetimeCategorySpend[c.CategoryID] += conv.add(c.Currency, c.TotalCents)
	}
	out.Budgets = []domain.BudgetStatus{}
	for _, b := range raw.Budgets {
		st := domain.BudgetStatus{Budget: b}
		st.SpentCents = categorySpend[b.CategoryID] + billSpend[b.ID]
		st.LifetimeSpentCents = lifetimeCategorySpend[b.CategoryID] + lifetimeBillSpend[b.ID]
		st.RemainingCents = b.AmountCents - st.LifetimeSpentCents
		if b.Status == domain.BudgetOpen || st.SpentCents != 0 {
			out.Budgets = append(out.Budgets, st)
		}
	}

	out.ConversionWarnings = conv.warnings()
	return out, nil
}
