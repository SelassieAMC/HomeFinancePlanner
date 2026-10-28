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
	"home-finance-planner/backend/internal/fx"
)

// AccountService implements account business rules.
type AccountService struct{ accounts AccountStore }

// AccountInput is the user-facing payload for create/update. The money field
// is the OPENING balance (the pre-ledger seed): the current balance is derived
// from the transaction ledger and moves with every saved row — adjust it
// through Reconcile when it drifts from the bank.
type AccountInput struct {
	Name                string
	Type                domain.AccountType
	Currency            string
	OpeningBalanceCents int64  // may be negative (credit accounts start owed)
	CardLastDigits      string // optional; last digits for bill matching
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

// Reconcile adjusts the opening balance so the derived balance matches the
// number the bank reports: opening += (stated − derived). The ledger's facts
// stay untouched — a reconciliation is a correction of the pre-ledger seed
// money, not a fabricated transaction. Stated may be negative (credit
// accounts).
func (s *AccountService) Reconcile(ctx context.Context, id int64, statedCents int64) (domain.Account, error) {
	a, err := s.accounts.GetByID(ctx, id)
	if err != nil {
		return domain.Account{}, err
	}
	a.OpeningBalanceCents += statedCents - a.BalanceCents
	return s.accounts.Update(ctx, a)
}

// Ledger returns the account register (Phase 2): the Account — its derived
// balance rides along for the header — plus one window of the transaction
// thread, newest first, every row carrying the balance as it stood after it.
// View-only; limit/offset page it without disturbing the per-row fold.
func (s *AccountService) Ledger(ctx context.Context, id int64, limit, offset int) (domain.AccountLedger, error) {
	acc, err := s.accounts.GetByID(ctx, id)
	if err != nil {
		return domain.AccountLedger{}, fmt.Errorf("load account %d: %w", id, err)
	}
	entries, err := s.accounts.Ledger(ctx, id, limit, offset)
	if err != nil {
		return domain.AccountLedger{}, err
	}
	return domain.AccountLedger{Account: acc, Entries: entries}, nil
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
	cardDigits := nonDigitsAccount.ReplaceAllString(strings.TrimSpace(in.CardLastDigits), "")
	if cardDigits != "" && len(cardDigits) != 4 {
		return domain.Account{}, validationError("card_last_digits %q must be exactly 4 digits", in.CardLastDigits)
	}
	return domain.Account{
		Name:                strings.TrimSpace(in.Name),
		Type:                in.Type,
		Currency:            currency,
		OpeningBalanceCents: in.OpeningBalanceCents,
		CardLastDigits:      cardDigits,
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
	// bags validates expense tagging into allocation envelopes. nil disables
	// the layer (tests).
	bags BagStore
	// fx supplies the exchange-rate snapshot for the receiving leg of a
	// TRANSFER between accounts of different currencies (recorded once, at
	// save time, so the balance math is stable). nil — or a snapshot without
	// both currencies — degrades to 1:1, the same accepted degradation the
	// summary services use.
	fx *FXService
	// mappings is the product-name normalization memory: a typed name that is
	// already mapped fills its category from the mapping, and an unmapped
	// name records an identity mapping so the next entry normalizes without
	// asking. nil disables the layer (tests).
	mappings ProductMappingStore
	// insights enqueues the deferred price-per-unit analysis of a saved
	// purchase. nil disables the layer (tests).
	insights PurchaseAnalyzer
	log      *slog.Logger
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
	// CounterAccountID is the destination account of a transfer (the input's
	// AccountID is the source). Must be nil for income/expense and a distinct
	// account for transfers.
	CounterAccountID *int64
	// FeeCents is an optional transfer fee the bank/intermediary takes, in the
	// SOURCE account's currency: the sending side loses amount + fee. Transfer
	// only; must be ≥ 0.
	FeeCents *int64
	// FxRate is a user-overridden exchange rate for a cross-currency transfer
	// (units of the destination currency per 1 of the source) — the public
	// reference rate is the default, but banks don't always follow it. Transfer
	// only; must be > 0; refused when both accounts share a currency.
	FxRate *float64
	// BagID tags an expense as spent out of a reserve. Refused on income and
	// transfer; expense-only, and the bag must belong to the transaction's
	// own account and stay open.
	BagID *int64
	// Items are the article lines of a manually entered purchase; empty for a
	// plain amount-only transaction. Only expenses may carry them.
	Items []TransactionItemInput
}

// TransactionItemInput is one item line of the user-facing payload.
type TransactionItemInput struct {
	Name           string
	Brand          string
	Unit           string
	UnitValue      *float64 // printed size magnitude ("500" for "500ml"); nil = unknown
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
		created, err := s.transactions.CreateWithItems(ctx, t, t.Items)
		if err == nil {
			s.enqueueInsight(ctx, created.ID)
			s.enqueueCategoryCheck(ctx, created)
		}
		return created, err
	}
	created, err := s.transactions.Create(ctx, t)
	if err == nil {
		s.enqueueCategoryCheck(ctx, created)
	}
	return created, err
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
		updated, err := s.transactions.UpdateWithItems(ctx, t, t.Items)
		if err == nil {
			s.enqueueInsight(ctx, id)
			s.enqueueCategoryCheck(ctx, updated)
		}
		return updated, err
	}
	updated, err := s.transactions.Update(ctx, t)
	if err == nil {
		s.enqueueCategoryCheck(ctx, updated)
	}
	return updated, err
}

// enqueueInsight fires the deferred price-per-unit analysis of the saved
// purchase (only the WithItems paths carry linked product lines). The save
// never waits for it: a nil analyzer or an enqueue error is logged and
// dropped — the analysis is a nudge in the background, not part of the
// transaction.
func (s *TransactionService) enqueueInsight(ctx context.Context, id int64) {
	if s.insights == nil {
		return
	}
	if err := s.insights.AnalyzeTransaction(ctx, id); err != nil {
		s.log.Warn("enqueue purchase analysis", "transaction_id", id, "error", err)
	}
}

// enqueueCategoryCheck fires the deferred category-anomaly job for an expense
// transaction: a category's month-to-date spend evaluated against the
// preceding months. Income and transfers move no category spend, so they
// never qualify; the worker re-checks anyway (the save decides what it saved,
// the worker decides what it evaluates).
func (s *TransactionService) enqueueCategoryCheck(ctx context.Context, t domain.Transaction) {
	if s.insights == nil || t.Kind != domain.TransactionExpense {
		return
	}
	if err := s.insights.AnalyzeTransactionCategories(ctx, t.ID); err != nil {
		s.log.Warn("enqueue category anomaly check", "transaction_id", t.ID, "error", err)
	}
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
	if s.log == nil {
		s.log = slog.Default()
	}
	if err := validatePositiveCents(in.AmountCents, "amount_cents"); err != nil {
		return domain.Transaction{}, err
	}
	if !in.Kind.Valid() {
		return domain.Transaction{}, validationError("kind %q must be income, expense or transfer", in.Kind)
	}
	if len(in.Items) > 0 && in.Kind != domain.TransactionExpense {
		return domain.Transaction{}, validationError("items are only supported on expense transactions")
	}
	if in.Kind == domain.TransactionTransfer && (in.CategoryID != nil || in.StoreName != "") {
		return domain.Transaction{}, validationError("transfers carry no category or store")
	}
	// A store is where goods were bought; an income (salary, refund, interest)
	// arrives from a source that belongs in the description, not in the store
	// catalogue.
	if in.Kind == domain.TransactionIncome && in.StoreName != "" {
		return domain.Transaction{}, validationError("income carries no store — name the paying source in the description")
	}
	if in.Kind == domain.TransactionTransfer {
		if in.CounterAccountID == nil {
			return domain.Transaction{}, validationError("a transfer needs a destination account (counter_account_id)")
		}
	} else if in.CounterAccountID != nil {
		return domain.Transaction{}, validationError("counter_account_id is only valid on transfers")
	}
	if in.FeeCents != nil {
		if in.Kind != domain.TransactionTransfer {
			return domain.Transaction{}, validationError("fee_cents is only valid on transfers")
		}
		if *in.FeeCents < 0 {
			return domain.Transaction{}, validationError("fee_cents must not be negative")
		}
	}
	if in.FxRate != nil {
		if in.Kind != domain.TransactionTransfer {
			return domain.Transaction{}, validationError("fx_rate is only valid on transfers")
		}
		if *in.FxRate <= 0 {
			return domain.Transaction{}, validationError("fx_rate must be positive when given")
		}
	}
	// Bag tagging is a spending concept: income cannot spend a reserve and a
	// transfer moves money, it does not spend it.
	if in.BagID != nil && in.Kind != domain.TransactionExpense {
		return domain.Transaction{}, validationError("bag_id is only valid on expenses — income and transfers write no reserve")
	}
	if len(in.Items) > maxTransactionItems {
		return domain.Transaction{}, validationError("at most %d items are supported", maxTransactionItems)
	}
	if err := validateDate(in.Date, "date"); err != nil {
		return domain.Transaction{}, err
	}
	// A transaction records money that already moved: a future date would be a
	// fabricated fact (balances are stored values, and no real-world movement
	// can have happened yet). Planned movements belong on scheduled actions.
	if in.Date > time.Now().Format("2006-01-02") {
		return domain.Transaction{}, validationError("date %s cannot be in the future — transactions record money that already moved, planned movements belong on scheduled actions", in.Date)
	}
	if len(in.Description) > 500 {
		return domain.Transaction{}, validationError("description must be at most 500 characters")
	}
	// Referential checks: a transaction must point at real rows. An open
	// account defaults to the primary (first listed) account — with no
	// accounts yet the request stays an error.
	acc := domain.Account{}
	var err error
	if in.AccountID > 0 {
		acc, err = s.accounts.GetByID(ctx, in.AccountID)
		if err != nil {
			return domain.Transaction{}, fmt.Errorf("validate account_id: %w", err)
		}
	} else {
		list, lerr := s.accounts.List(ctx)
		if lerr != nil {
			return domain.Transaction{}, fmt.Errorf("list accounts: %w", lerr)
		}
		if len(list) == 0 {
			return domain.Transaction{}, validationError("account_id must be a positive id")
		}
		acc = list[0]
	}
	if in.CategoryID != nil {
		if _, err := s.categories.GetByID(ctx, *in.CategoryID); err != nil {
			return domain.Transaction{}, fmt.Errorf("validate category_id: %w", err)
		}
	}

	// Bag tagging: the bag must exist, be open, and belong to the account the
	// transaction is recorded on — a bag partitions exactly one account.
	var bagID *int64
	if in.BagID != nil {
		bag, err := s.validateBag(ctx, *in.BagID)
		if err != nil {
			return domain.Transaction{}, err
		}
		if bag.AccountID != acc.ID {
			return domain.Transaction{}, validationError(
				"bag %q lives on another account — tag expenses on %q too",
				bag.Name, acc.Name)
		}
		bagID = in.BagID
	}

	// Transfer destination: a real account distinct from the source. The
	// amount is recorded in the source account's currency (like every manual
	// row); a cross-currency transfer also records the receiving leg — what
	// the destination account gains — from the FX snapshot at save time (or a
	// user-overridden rate, banks don't always follow the reference), so the
	// derived balances on both sides stay honest. Same currency (or no rate
	// known and nothing overridden) folds 1:1. An optional fee the bank takes
	// is recorded in the source currency and deducted on the sending side.
	var counterID *int64
	var counterAmount *int64
	var counterCurrency string
	var feeCents *int64
	var fxRate *float64
	if in.Kind == domain.TransactionTransfer {
		counter, err := s.accounts.GetByID(ctx, *in.CounterAccountID)
		if err != nil {
			return domain.Transaction{}, fmt.Errorf("validate counter_account_id: %w", err)
		}
		if counter.ID == acc.ID {
			return domain.Transaction{}, validationError("source and destination accounts must differ")
		}
		counterID = in.CounterAccountID
		feeCents = in.FeeCents
		if counter.Currency != acc.Currency {
			// Rate priority: the user's override wins, the FX snapshot follows.
			rate, ok := 0.0, false
			if in.FxRate != nil {
				rate, ok = *in.FxRate, true
			} else if s.fx != nil {
				if snap, serr := s.fx.Snapshot(ctx); serr == nil {
					rate, ok = snap.Rate(acc.Currency, counter.Currency)
				}
			}
			if ok {
				converted := fx.ConvertCents(in.AmountCents, rate)
				counterAmount = &converted
				counterCurrency = counter.Currency
				fxRate = &rate
			} else {
				s.log.WarnContext(ctx, "transfer conversion degraded to 1:1 — no rate for currency pair",
					"from", acc.Currency, "to", counter.Currency)
			}
		} else if in.FxRate != nil {
			return domain.Transaction{}, validationError(
				"fx_rate is only valid on transfers between accounts of different currencies")
		}
	}

	// Item lines, mirroring buildBill's per-line rules minus budget
	// attribution: deposit returns ("Leergut") and lines filed under a
	// category with allows_negative (the seeded "Deposit & Returns" / Pfand
	// family) are money back — their unit price and line total may be
	// negative and reduce the items total.
	items := make([]domain.TransactionItem, 0, len(in.Items))
	// recordableCats tracks the per-line category the normalization memory may
	// record (explicit or mapped) — the transaction-level fallback below is a
	// display default, never a memory decision.
	recordableCats := make([]*int64, 0, len(in.Items))
	itemsTotal := int64(0)
	for _, it := range in.Items {
		name := strings.TrimSpace(it.Name)
		if name == "" {
			return domain.Transaction{}, validationError("every item needs a name")
		}
		if it.Quantity <= 0 {
			return domain.Transaction{}, validationError("quantity for %q must be positive", name)
		}
		// Normalization memory: a typed name that is already mapped fills its
		// category when the user left it open. The typed name itself stays
		// untouched — it is the raw text the product catalogue keys on.
		// Deposit lines are never looked up (they are not products).
		categoryID := it.CategoryID
		recordable := it.CategoryID
		isReturn := domain.IsDepositReturn(name)
		if s.mappings != nil && !isReturn && !domain.IsDepositArtifact(name) {
			if m, err := s.mappings.FindByRawName(ctx, name); err == nil {
				if categoryID == nil && m.CategoryID != nil {
					categoryID = m.CategoryID
					recordable = m.CategoryID
				}
			} else if !errors.Is(err, domain.ErrNotFound) {
				s.log.Warn("lookup product mapping", "raw", name, "error", err)
			}
		}
		// Low-friction default: a line the user left open files under the
		// transaction's main category.
		if categoryID == nil && in.CategoryID != nil {
			categoryID = in.CategoryID
		}
		recordableCats = append(recordableCats, recordable)
		// The negative rule needs the line's category, so it is resolved
		// before the price checks — like buildBill. Deposit/coupon artifact
		// lines ("Pfand", "Rabatt", "Gutschein", …) may carry money-back
		// amounts regardless of the filed category.
		allowsNegative := isReturn || domain.IsDepositArtifact(name)
		if categoryID != nil {
			cat, err := s.categories.GetByID(ctx, *categoryID)
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
			UnitValue:      sanitizeUnitValue(it.UnitValue),
			CategoryID:     categoryID,
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
	// Money-back lines (Leergut / negative prices) and deposit charges or
	// free-item markers (domain.IsDepositArtifact) stay unlinked, like bill
	// returns, so purchase stats and store prices stay about real purchases.
	for i := range items {
		if items[i].UnitPriceCents < 0 || domain.IsDepositReturn(items[i].Name) ||
			domain.IsDepositArtifact(items[i].Name) {
			continue
		}
		pid, product := s.resolveTransactionProduct(ctx, items[i])
		items[i].ProductID = pid
		// Low-friction entry: fields the user left blank inherit the linked
		// product's descriptors (its purchase history knows the size), without
		// touching what was typed explicitly — an entered-but-unsanitary value
		// stays the "unknown" the sanitize wrote.
		if product != nil {
			if strings.TrimSpace(in.Items[i].Unit) == "" && product.Unit != "" {
				items[i].Unit = product.Unit
			}
			if in.Items[i].UnitValue == nil && product.UnitValue != nil {
				items[i].UnitValue = product.UnitValue
			}
		}
		// Record the typed name into the normalization memory (identity
		// mapping, source 'manual'): a name that matches nothing today is its
		// own standard form until a scan or the analysis job decides
		// otherwise. ErrConflict means it is already mapped — fine.
		if s.mappings != nil {
			if _, err := s.mappings.Create(ctx, domain.ProductNameMapping{
				RawName:      items[i].Name,
				StandardName: items[i].Name,
				CategoryID:   recordableCats[i],
				ProductID:    items[i].ProductID,
				Source:       domain.MappingSourceManual,
			}); err != nil && !errors.Is(err, domain.ErrConflict) {
				s.log.Warn("record manual product mapping", "raw", items[i].Name, "error", err)
			}
		}
	}

	return domain.Transaction{
		AccountID:          acc.ID,
		CategoryID:         in.CategoryID,
		Kind:               in.Kind,
		AmountCents:        in.AmountCents,
		Currency:           acc.Currency, // manual rows are always in the account's currency
		Description:        strings.TrimSpace(in.Description),
		Date:               in.Date,
		StoreID:            storeID,
		CounterAccountID:   counterID,
		CounterAmountCents: counterAmount,
		CounterCurrency:    counterCurrency,
		FeeCents:           feeCents,
		FxRate:             fxRate,
		BagID:              bagID,
		ItemsTotalCents:    itemsTotal,
		Items:              items,
	}, nil
}

// validateBag loads a bag for tagging and refuses closed ones (their reserve
// no longer accepts spend, like closed budgets).
func (s *TransactionService) validateBag(ctx context.Context, id int64) (domain.Bag, error) {
	if s.bags == nil {
		return domain.Bag{}, validationError("bag_id tagging is unavailable")
	}
	bag, err := s.bags.GetBagByID(ctx, id)
	if err != nil {
		return domain.Bag{}, fmt.Errorf("validate bag_id: %w", err)
	}
	if bag.Status == domain.BagClosed {
		return domain.Bag{}, validationError("bag %q is closed and no longer accepts spend", bag.Name)
	}
	return bag, nil
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
// product, resolved mapping-first (the normalization memory's product_id,
// which earlier purchase lines decided) and otherwise find-or-created
// case-insensitively from the line name (CategoryID seeds a new product).
// Failures are non-fatal (logged, link left nil) — a catalogue problem must
// not block recording the purchase; the next save retries. The matched
// product (nil when unlinked) comes back so the line can inherit its
// unit/unit_value.
func (s *TransactionService) resolveTransactionProduct(ctx context.Context, it domain.TransactionItem) (*int64, *domain.Product) {
	if s.products == nil {
		return nil, nil
	}
	name := strings.TrimSpace(it.Name)
	if name == "" {
		return nil, nil
	}
	// Mapping-first: a raw text the normalization memory links to a product
	// joins that product's history, even when the typed name never matches a
	// catalogue name (e.g. "Whole Milk" mapping to "Whole Milk 3.8%").
	if p := mappingProduct(ctx, s.mappings, s.products, s.log, name); p != nil {
		learnProductUnitValue(ctx, s.products, s.log, p, it.UnitValue)
		id := p.ID
		return &id, p
	}
	product, err := s.products.FindByName(ctx, name)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		product, err = s.products.Create(ctx, domain.Product{
			Name:       name,
			Brand:      it.Brand,
			Unit:       it.Unit,
			UnitValue:  it.UnitValue,
			CategoryID: it.CategoryID,
		})
		if errors.Is(err, domain.ErrConflict) {
			// Lost a race — re-read the winner.
			product, err = s.products.FindByName(ctx, name)
		}
		if err != nil {
			s.log.Warn("create product from transaction item", "name", name, "error", err)
			return nil, nil
		}
	case err != nil:
		s.log.Warn("find product for transaction item", "name", name, "error", err)
		return nil, nil
	default:
		// Existing product: fill its missing size magnitude from the line,
		// never overwriting a decided value (same rule as the bill flow).
		learnProductUnitValue(ctx, s.products, s.log, &product, it.UnitValue)
	}
	linkProductMapping(ctx, s.mappings, s.log, name, product.ID)
	id := product.ID
	linked := product
	return &id, &linked
}

// SummaryService implements dashboard aggregates, converting every total
// into the user's base currency.
type SummaryService struct {
	summary    SummaryStore
	settings   *SettingsService
	rates      RateSource
	categories CategoryStore
	// budgets is optional (nil in unit tests): when set, Range/Month summary
	// first run the monthly-budget rollover so a stale open monthly row
	// never lingers past its own month.
	budgets *BudgetService
	// schedules is optional (nil in unit tests): when set, Range/Month
	// summary runs the scheduler's lazy catch-up (materialize runs, auto-apply
	// virtual ones) so the dashboard's "Coming up" feed never lags behind a
	// dashboard that never visits the schedules page.
	schedules *ScheduledActionService
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
	// Rollover targets the real current month, not the queried range: viewing
	// a past period must not leave a stale open monthly row behind.
	if s.budgets != nil {
		if err := s.budgets.EnsureMonthlyCurrent(ctx, currentMonth()); err != nil {
			return domain.Summary{}, fmt.Errorf("budget month rollover: %w", err)
		}
	}
	// The scheduler's lazy catch-up rides the same read (materialize runs,
	// auto-apply the virtual ones) — the dashboard always sees an honest
	// "Coming up" feed even without visiting the schedules page.
	if s.schedules != nil {
		if err := s.schedules.EnsureUpcoming(ctx, time.Now().Format("2006-01-02")); err != nil {
			return domain.Summary{}, fmt.Errorf("schedule catch-up: %w", err)
		}
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
	// the balance is lifetime spend against the amount. For MONTHLY rows the
	// transaction side is bounded to the row's own month (later months belong
	// to successor rows) while explicit bill-line links stay all-time — the
	// link is the attribution, regardless of the bill's date. Open budgets
	// are always reported; closed ones only when they had attributed activity
	// inside the range.
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
	// Per-category per-month transaction spend for the monthly rows.
	categoryMonthSpend := map[int64]map[string]int64{}
	for _, c := range raw.CategoryMonthSpend {
		m, ok := categoryMonthSpend[c.CategoryID]
		if !ok {
			m = map[string]int64{}
			categoryMonthSpend[c.CategoryID] = m
		}
		m[c.Month] += conv.add(c.Currency, c.TotalCents)
	}
	out.Budgets = []domain.BudgetStatus{}
	for _, b := range raw.Budgets {
		st := domain.BudgetStatus{Budget: b}
		if b.Cadence == domain.BudgetMonthly && b.Month != "" {
			monthFrom := b.Month + "-01"
			monthTo := monthEnd(b.Month)
			switch {
			case to < monthFrom || from > monthTo:
				// Disjoint: nothing of this month falls inside the range.
				st.SpentCents = 0
			case from >= monthFrom && to <= monthTo:
				// Range inside the month: exact in-range attribution.
				st.SpentCents = categorySpend[b.CategoryID] + billSpend[b.ID]
			default:
				// Range spans the month (or beyond): the month bucket is the
				// best attribution — exact when the range covers the month,
				// approximate for a partial overlap (e.g. a week crossing a
				// month boundary).
				st.SpentCents = categoryMonthSpend[b.CategoryID][b.Month] + billSpend[b.ID]
			}
			st.LifetimeSpentCents = categoryMonthSpend[b.CategoryID][b.Month] + lifetimeBillSpend[b.ID]
		} else {
			st.SpentCents = categorySpend[b.CategoryID] + billSpend[b.ID]
			st.LifetimeSpentCents = lifetimeCategorySpend[b.CategoryID] + lifetimeBillSpend[b.ID]
		}
		st.RemainingCents = b.AmountCents - st.LifetimeSpentCents
		// Envelope rollover (0043): a carrying monthly row's remaining is the
		// available money — the cap plus the inherited running carry (deficits
		// negative, MoneyWiz running balance) minus the month spend.
		if b.CarryOver && b.Cadence == domain.BudgetMonthly {
			st.RemainingCents += b.CarriedCents
		}
		if b.Status == domain.BudgetOpen || st.SpentCents != 0 {
			out.Budgets = append(out.Budgets, st)
		}
	}

	out.ConversionWarnings = conv.warnings()
	return out, nil
}

// Glance returns the period-independent "at a glance" dashboard row for the
// given day ("YYYY-MM-DD", supplied by the handler so the service stays
// deterministic): cash flow for today's calendar month, net worth across all
// accounts, the average daily expense over the trailing 30 days (today
// included — an under-filled day pulls the average down slightly), and the
// pending-receipt backlog (scanned bills awaiting review — money already
// spent, not unpaid obligations).
func (s *SummaryService) Glance(ctx context.Context, today string) (domain.Glance, error) {
	if err := validateDate(today, "today"); err != nil {
		return domain.Glance{}, err
	}
	t, err := timeParseDate(today)
	if err != nil {
		return domain.Glance{}, validationError("today %q is not a real date", today)
	}
	monthFrom := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthTo := monthFrom.AddDate(0, 1, -1)
	burnFrom := t.AddDate(0, 0, -29)

	base, err := s.settings.BaseCurrency(ctx)
	if err != nil {
		return domain.Glance{}, err
	}
	snap, err := s.rates.Snapshot(ctx)
	if err != nil {
		return domain.Glance{}, err
	}
	raw, err := s.summary.RawGlance(ctx,
		monthFrom.Format("2006-01-02"), monthTo.Format("2006-01-02"),
		burnFrom.Format("2006-01-02"), today)
	if err != nil {
		return domain.Glance{}, err
	}
	conv := newConverter(base, snap)

	out := domain.Glance{
		Currency:           base,
		Month:              monthFrom.Format("2006-01"),
		ConversionWarnings: []string{},
		PendingReceipts:    raw.PendingReceipts,
	}
	for _, a := range raw.MonthIncome {
		out.IncomeCents += conv.add(a.Currency, a.Cents)
	}
	for _, a := range raw.MonthExpense {
		out.ExpenseCents += conv.add(a.Currency, a.Cents)
	}
	out.NetCents = out.IncomeCents - out.ExpenseCents
	for _, a := range raw.Balances {
		out.NetWorthCents += conv.add(a.Currency, a.Cents)
	}
	var expense30 int64
	for _, a := range raw.Expense30Days {
		expense30 += conv.add(a.Currency, a.Cents)
	}
	out.BurnRateCents = expense30 / 30
	for _, a := range raw.PendingTotals {
		out.PendingReceiptCents += conv.add(a.Currency, a.Cents)
	}
	out.ConversionWarnings = conv.warnings()
	return out, nil
}
