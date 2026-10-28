package service

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// BagService implements allocation-envelope business rules. A bag is a
// virtual partition of ONE account's balance: its balance is derived per read
// as Σ allocations − tagged spend (spend attribution is live from the tagged
// transactions/bills, never copied into the ledger), so later edits of a
// spend always reflect. Allocations are plan-level rows: they write no money
// movement and stay out of every aggregate by construction — the same
// discipline transfers follow. The account-level invariant Σ bags ≤
// account.balance is warned, never enforced: balances are user-maintained
// values and salaries arrive mid-month, after funding.
type BagService struct {
	bags     BagStore
	accounts AccountStore
	// fx supplies the rate snapshot for converting tagged spend recorded in a
	// foreign currency (bills) into the bag's account currency. nil disables
	// conversion (tests): foreign-currency spend then counts 1:1 with a
	// warning — the same degradation the summary services accept.
	fx *FXService
	// schedules powers the delete guard: an active (non-archived) schedule
	// funding or draining the bag refuses its deletion — the auto-applied
	// movements would resurrect allocations into a gone ledger. nil skips the
	// guard (tests).
	schedules ScheduledActionStore
	log       *slog.Logger
}

// BagInput is the user-facing payload for create/update.
type BagInput struct {
	Name        string
	AccountID   int64
	TargetCents *int64 // optional; positive when set
}

// RebalanceInput moves leftover from one bag to another INside one account.
// Cross-account moves are refused — move money with a real transfer first,
// then rebalance. One intent = one API call; the UI may suggest combos, the
// API stays honest.
type RebalanceInput struct {
	FromBagID   int64
	ToBagID     int64
	AmountCents int64 // positive magnitude; the legs carry the sign
	Note        string
	Date        string
}

// Overview returns the whole bags view: every account with its live balance,
// the money still unbagged, and its bags with computed balances. One service
// read — per-account unbagged numbers need the whole picture.
func (s *BagService) Overview(ctx context.Context) (domain.BagOverview, error) {
	accounts, err := s.accounts.List(ctx)
	if err != nil {
		return domain.BagOverview{}, fmt.Errorf("list accounts: %w", err)
	}
	bags, err := s.bags.ListBags(ctx, domain.BagFilters{})
	if err != nil {
		return domain.BagOverview{}, err
	}
	allocs, spend, err := s.bagFacts(ctx, "")
	if err != nil {
		return domain.BagOverview{}, err
	}
	snap := domain.RateSnapshot{}
	if s.fx != nil {
		if snap, err = s.fx.Snapshot(ctx); err != nil {
			return domain.BagOverview{}, err
		}
	}

	byAccount := make(map[int64]*domain.BagAccountView, len(accounts))
	order := make([]int64, 0, len(accounts))
	for i := range accounts {
		acc := &accounts[i]
		byAccount[acc.ID] = &domain.BagAccountView{
			AccountID:           acc.ID,
			AccountName:         acc.Name,
			Currency:            acc.Currency,
			AccountBalanceCents: acc.BalanceCents,
			UnbaggedCents:       acc.BalanceCents,
			Bags:                []domain.BagStatus{},
		}
		order = append(order, acc.ID)
	}

	warnings := map[string]bool{}
	for _, g := range bags {
		view, ok := byAccount[g.AccountID]
		if !ok {
			continue // bags cascade with their account — defensively skip
		}
		conv := newConverter(view.Currency, snap)
		spent := int64(0)
		for _, gp := range spend[g.ID] {
			spent += conv.add(gp.Currency, gp.Cents)
		}
		for _, w := range conv.warnings() {
			warnings[w] = true
		}
		view.Bags = append(view.Bags, domain.BagStatus{
			Bag:              g,
			AccountName:      view.AccountName,
			TaggedSpendCents: spent,
			BalanceCents:     allocs[g.ID] - spent,
		})
	}
	for _, v := range byAccount {
		for _, st := range v.Bags {
			// A closed bag's leftover counts toward "bagged" only while it
			// stays open; unbagged is the ready-to-assign number of THIS
			// account's open reservations.
			if st.Status == domain.BagOpen {
				v.UnbaggedCents -= st.BalanceCents
			}
		}
		v.Oversubscribed = v.UnbaggedCents < 0
	}

	out := domain.BagOverview{Accounts: []domain.BagAccountView{}, ConversionWarnings: []string{}}
	for _, id := range order {
		out.Accounts = append(out.Accounts, *byAccount[id])
	}
	for w := range warnings {
		out.ConversionWarnings = append(out.ConversionWarnings, w)
	}
	return out, nil
}

// Get returns one bag with its computed balance.
func (s *BagService) Get(ctx context.Context, id int64) (domain.BagStatus, error) {
	b, err := s.bags.GetBagByID(ctx, id)
	if err != nil {
		return domain.BagStatus{}, err
	}
	return s.withBalance(ctx, b)
}

// Create opens a new bag for one account. The bag's currency is the
// account's currency (implied, never stored on the bag row).
func (s *BagService) Create(ctx context.Context, in BagInput) (domain.BagStatus, error) {
	b, err := s.build(ctx, domain.Bag{}, in)
	if err != nil {
		return domain.BagStatus{}, err
	}
	b.Status = domain.BagOpen
	created, err := s.bags.CreateBag(ctx, b)
	if err != nil {
		return domain.BagStatus{}, err
	}
	return s.withBalance(ctx, created)
}

// Update renames a bag or changes its target. Re-pointing a bag at another
// account is refused: a reserve does not wander between accounts (move the
// money with a real transfer, then create the bag where it belongs) — its
// existing allocation ledger would silently change currency meaning.
func (s *BagService) Update(ctx context.Context, id int64, in BagInput) (domain.BagStatus, error) {
	existing, err := s.bags.GetBagByID(ctx, id)
	if err != nil {
		return domain.BagStatus{}, err
	}
	b, err := s.build(ctx, existing, in)
	if err != nil {
		return domain.BagStatus{}, err
	}
	b.ID = id
	b.Status = existing.Status
	b.ClosedAt = existing.ClosedAt
	updated, err := s.bags.UpdateBag(ctx, b)
	if err != nil {
		return domain.BagStatus{}, err
	}
	return s.withBalance(ctx, updated)
}

// SetStatus closes a bag or reopens a closed one. Closing no longer demands
// a zero balance (decision 2026-10-28): the leftover never strands. One
// balancing allocation zeroes the bag at the close moment — a withdraw of the
// remaining balance when positive (the leftover returns to the account's
// unbagged money), a fund of the deficit when it dipped negative — dated at
// the close date with note "Closed: <name>", kept forever in the ledger like
// any other allocation. Reopening keeps reading; the balancing row is
// ordinary history.
func (s *BagService) SetStatus(ctx context.Context, id int64, status domain.BagLifecycle) (domain.BagStatus, error) {
	if !status.Valid() {
		return domain.BagStatus{}, validationError("status %q must be open or closed", status)
	}
	existing, err := s.bags.GetBagByID(ctx, id)
	if err != nil {
		return domain.BagStatus{}, err
	}
	if status == domain.BagClosed && existing.Status == domain.BagOpen {
		st, err := s.withBalance(ctx, existing)
		if err != nil {
			return domain.BagStatus{}, err
		}
		if st.BalanceCents != 0 {
			bal := domain.BagAllocationInput{
				Kind:        domain.BagWithdraw,
				AmountCents: st.BalanceCents,
				Note:        "Closed: " + existing.Name,
				Date:        time.Now().Format("2006-01-02"),
			}
			if st.BalanceCents < 0 {
				bal.Kind = domain.BagFund
				bal.AmountCents = -st.BalanceCents
			}
			alloc, err := s.buildAllocation(ctx, existing, bal)
			if err != nil {
				return domain.BagStatus{}, err
			}
			if _, err := s.bags.CreateAllocation(ctx, alloc); err != nil {
				return domain.BagStatus{}, fmt.Errorf("close balancing allocation: %w", err)
			}
		}
	}
	// The close stamp lives here (the service owns the lifecycle transition);
	// reopen clears it.
	if status == domain.BagClosed {
		now := time.Now().UTC()
		existing.ClosedAt = &now
	} else {
		existing.ClosedAt = nil
	}
	existing.Status = status
	b, err := s.bags.UpdateBag(ctx, existing)
	if err != nil {
		return domain.BagStatus{}, err
	}
	return s.withBalance(ctx, b)
}

// Delete removes a bag outright — no history is kept (decision 2026-10-13):
// allocations cascade, tagged spend falls back to untagged via SET NULL and
// reads as plain expense. The scheduler guard bites since 0037: an active
// schedule funding or draining the bag would otherwise keep auto-applying
// allocations into a ledger that just vanished (archive the schedules first).
func (s *BagService) Delete(ctx context.Context, id int64) error {
	if s.schedules != nil {
		targets, err := s.schedules.ActiveActionsTargetingBag(ctx, id)
		if err != nil {
			return err
		}
		if len(targets) > 0 {
			names := make([]string, 0, len(targets))
			for _, t := range targets {
				names = append(names, fmt.Sprintf("%q", t.Name))
			}
			return conflictError(
				"active schedule(s) %s move money into or out of this bag — archive them before deleting it",
				strings.Join(names, ", "))
		}
	}
	return s.bags.DeleteBag(ctx, id)
}

// Allocate records a manual ledger entry: fund (money reserved), withdraw
// (money released back to unbagged) or adjust (an explicit correction of a
// drifted balance). Rebalances must go through Rebalance — they are two rows
// on two bags, not one. Closed bags admit the withdraw rule only after being
// reopened: all ledger writes refuse on closed bags.
func (s *BagService) Allocate(ctx context.Context, bagID int64, in domain.BagAllocationInput) (domain.BagAllocation, error) {
	bag, err := s.bags.GetBagByID(ctx, bagID)
	if err != nil {
		return domain.BagAllocation{}, err
	}
	if bag.Status == domain.BagClosed {
		return domain.BagAllocation{}, validationError("bag %q is closed — reopen it before touching its reserve", bag.Name)
	}
	alloc, err := s.buildAllocation(ctx, bag, in)
	if err != nil {
		return domain.BagAllocation{}, err
	}
	return s.bags.CreateAllocation(ctx, alloc)
}

// AllocationHistory lists one bag's ledger rows, newest first.
func (s *BagService) AllocationHistory(ctx context.Context, bagID int64) ([]domain.BagAllocation, error) {
	if _, err := s.bags.GetBagByID(ctx, bagID); err != nil {
		return nil, err
	}
	return s.bags.ListAllocations(ctx, bagID)
}

// Rebalance moves an amount between two bags of the SAME account in one
// action: both ledger rows (source −, destination +) are written atomically
// and linked. Refuses when the move would push the source bag negative — a
// bag that "spent more than it holds" is a data smell; fix the tagging or
// withdraw explicitly instead.
func (s *BagService) Rebalance(ctx context.Context, in RebalanceInput) (domain.BagAllocation, domain.BagAllocation, error) {
	if in.AmountCents <= 0 {
		return domain.BagAllocation{}, domain.BagAllocation{}, validationError("amount_cents must be a positive magnitude")
	}
	if err := validateDate(in.Date, "date"); err != nil {
		return domain.BagAllocation{}, domain.BagAllocation{}, err
	}
	if len(in.Note) > 200 {
		return domain.BagAllocation{}, domain.BagAllocation{}, validationError("note must be at most 200 characters")
	}
	from, err := s.bags.GetBagByID(ctx, in.FromBagID)
	if err != nil {
		return domain.BagAllocation{}, domain.BagAllocation{}, fmt.Errorf("validate from_bag_id: %w", err)
	}
	to, err := s.bags.GetBagByID(ctx, in.ToBagID)
	if err != nil {
		return domain.BagAllocation{}, domain.BagAllocation{}, fmt.Errorf("validate to_bag_id: %w", err)
	}
	if from.ID == to.ID {
		return domain.BagAllocation{}, domain.BagAllocation{}, validationError("a bag cannot rebalance onto itself")
	}
	if from.AccountID != to.AccountID {
		return domain.BagAllocation{}, domain.BagAllocation{}, validationError(
			"bags %q and %q live on different accounts — move the money with a transfer, then rebalance",
			from.Name, to.Name)
	}
	for _, b := range []domain.Bag{from, to} {
		if b.Status != domain.BagOpen {
			return domain.BagAllocation{}, domain.BagAllocation{}, validationError("bag %q is closed and accepts no movement", b.Name)
		}
	}
	src, err := s.withBalance(ctx, from)
	if err != nil {
		return domain.BagAllocation{}, domain.BagAllocation{}, err
	}
	if src.BalanceCents < in.AmountCents {
		return domain.BagAllocation{}, domain.BagAllocation{}, validationError(
			"bag %q holds %d — rebalancing %d would push it negative", from.Name, src.BalanceCents, in.AmountCents)
	}

	date := in.Date
	note := strings.TrimSpace(in.Note)
	neg, pos, err := s.bags.CreateAllocationPair(ctx,
		domain.BagAllocation{BagID: from.ID, Kind: string(domain.BagRebalance), AmountCents: -in.AmountCents, Note: note, Date: date},
		domain.BagAllocation{BagID: to.ID, Kind: string(domain.BagRebalance), AmountCents: in.AmountCents, Note: note, Date: date},
	)
	if err != nil {
		return domain.BagAllocation{}, domain.BagAllocation{}, err
	}
	return neg, pos, nil
}

// YearClose previews the year-end close-out: every still-open bag's balance
// as it would stand after all allocations and tagged spend dated through the
// year's December 31, plus the shortfall of target bags ("warm expenses").
// The actual leftover reassignment is posted by the client as regular
// rebalances/withdrawals dated in close-out — one API, no bespoke state.
func (s *BagService) YearClose(ctx context.Context, year string) (domain.YearClosePreview, error) {
	year = strings.TrimSpace(year)
	if !yearRegex.MatchString(year) {
		return domain.YearClosePreview{}, validationError("year %q must be a four-digit year", year)
	}
	until := year + "-12-31"
	allocs, spend, err := s.bagFacts(ctx, until)
	if err != nil {
		return domain.YearClosePreview{}, err
	}
	bags, err := s.bags.ListBags(ctx, domain.BagFilters{Status: domain.BagOpen})
	if err != nil {
		return domain.YearClosePreview{}, err
	}
	accounts, err := s.accounts.List(ctx)
	if err != nil {
		return domain.YearClosePreview{}, fmt.Errorf("list accounts: %w", err)
	}
	snap := domain.RateSnapshot{}
	if s.fx != nil {
		if snap, err = s.fx.Snapshot(ctx); err != nil {
			return domain.YearClosePreview{}, err
		}
	}

	accName := make(map[int64]domain.Account, len(accounts))
	for _, a := range accounts {
		accName[a.ID] = a
	}
	out := domain.YearClosePreview{Year: year, Bags: []domain.YearCloseBagRow{}, ConversionWarnings: []string{}}
	warnings := map[string]bool{}
	for _, g := range bags {
		acc, ok := accName[g.AccountID]
		if !ok {
			continue
		}
		conv := newConverter(acc.Currency, snap)
		spent := int64(0)
		for _, gp := range spend[g.ID] {
			spent += conv.add(gp.Currency, gp.Cents)
		}
		for _, w := range conv.warnings() {
			warnings[w] = true
		}
		balance := allocs[g.ID] - spent
		target := int64(0)
		if g.TargetCents != nil {
			target = *g.TargetCents
		}
		row := domain.YearCloseBagRow{
			BagID:          g.ID,
			BagName:        g.Name,
			AccountID:      acc.ID,
			AccountName:    acc.Name,
			Currency:       acc.Currency,
			BalanceCents:   balance,
			TargetCents:    target,
			ShortfallCents: 0,
		}
		if target > 0 && balance < target {
			row.ShortfallCents = target - balance
		}
		out.Bags = append(out.Bags, row)
	}
	for w := range warnings {
		out.ConversionWarnings = append(out.ConversionWarnings, w)
	}
	return out, nil
}

// yearRegex validates a bare four-digit year for the close preview.
var yearRegex = regexp.MustCompile(`^\d{4}$`)

// build validates the payload over the existing row (zero for create).
func (s *BagService) build(ctx context.Context, existing domain.Bag, in BagInput) (domain.Bag, error) {
	if err := validateRequiredString(strings.TrimSpace(in.Name), "name", 100); err != nil {
		return domain.Bag{}, err
	}
	if in.TargetCents != nil {
		if err := validatePositiveCents(*in.TargetCents, "target_cents"); err != nil {
			return domain.Bag{}, err
		}
	}
	if in.AccountID <= 0 {
		return domain.Bag{}, validationError("account_id must be a positive id — a bag partitions one account")
	}
	acc, err := s.accounts.GetByID(ctx, in.AccountID)
	if err != nil {
		return domain.Bag{}, fmt.Errorf("validate account_id: %w", err)
	}
	if existing.ID > 0 && existing.AccountID != acc.ID {
		return domain.Bag{}, validationError(
			"bag %q belongs to account #%d — a reserve does not move between accounts",
			existing.Name, existing.AccountID)
	}
	return domain.Bag{
		Name:        strings.TrimSpace(in.Name),
		AccountID:   acc.ID,
		TargetCents: in.TargetCents,
	}, nil
}

// buildAllocation validates one manual ledger entry for a bag.
func (s *BagService) buildAllocation(ctx context.Context, bag domain.Bag, in domain.BagAllocationInput) (domain.BagAllocation, error) {
	kind := in.Kind
	if !kind.Valid() {
		return domain.BagAllocation{}, validationError("kind %q must be fund, withdraw or adjust (rebalance moves two bags)", kind)
	}
	if err := validateDate(in.Date, "date"); err != nil {
		return domain.BagAllocation{}, err
	}
	if len(in.Note) > 200 {
		return domain.BagAllocation{}, errNoteTooLong
	}
	amount := in.AmountCents
	switch kind {
	case domain.BagFund, domain.BagWithdraw:
		// The kind carries the sign: fund reserves into the bag, withdraw
		// releases from it — the client sends the positive magnitude only.
		if in.AmountCents <= 0 {
			return domain.BagAllocation{}, validationError("amount_cents must be a positive magnitude (the kind carries the sign)")
		}
		if kind == domain.BagWithdraw {
			amount = -in.AmountCents
		}
	default: // adjust — an explicit correction, any sign
		if in.AmountCents == 0 {
			return domain.BagAllocation{}, validationError("an adjustment must not be zero")
		}
	}
	return domain.BagAllocation{
		BagID:       bag.ID,
		Kind:        string(kind),
		AmountCents: amount,
		Note:        strings.TrimSpace(in.Note),
		Date:        in.Date,
	}, nil
}

var errNoteTooLong = validationError("note must be at most 200 characters")

// withBalance computes one bag's live balance from the ledger and the tagged
// spend, converted into the bag's account currency.
func (s *BagService) withBalance(ctx context.Context, b domain.Bag) (domain.BagStatus, error) {
	acc, err := s.accounts.GetByID(ctx, b.AccountID)
	if err != nil {
		return domain.BagStatus{}, fmt.Errorf("load bag account %d: %w", b.AccountID, err)
	}
	allocs, spend, err := s.bagFacts(ctx, "")
	if err != nil {
		return domain.BagStatus{}, err
	}
	snap := domain.RateSnapshot{}
	if s.fx != nil {
		if snap, err = s.fx.Snapshot(ctx); err != nil {
			return domain.BagStatus{}, err
		}
	}
	conv := newConverter(acc.Currency, snap)
	spent := int64(0)
	for _, gp := range spend[b.ID] {
		spent += conv.add(gp.Currency, gp.Cents)
	}
	if ws := conv.warnings(); len(ws) > 0 && s.log != nil {
		s.log.WarnContext(ctx, "bag tagged spend converted 1:1 (no rate)", "bag", b.Name, "currencies", strings.Join(ws, ","))
	}
	return domain.BagStatus{
		Bag:              b,
		AccountName:      acc.Name,
		TaggedSpendCents: spent,
		BalanceCents:     allocs[b.ID] - spent,
	}, nil
}

// bagFacts loads the two halves of every bag balance: the signed allocation
// totals and the per-currency tagged spend groups, both optionally bounded
// by a date ("YYYY-MM-DD"; empty = all-time).
func (s *BagService) bagFacts(ctx context.Context, until string) (map[int64]int64, map[int64][]domain.BagCurrencySpend, error) {
	allocs, err := s.bags.AllocationTotals(ctx, until)
	if err != nil {
		return nil, nil, err
	}
	spendRows, err := s.bags.TaggedSpend(ctx, until)
	if err != nil {
		return nil, nil, err
	}
	spend := map[int64][]domain.BagCurrencySpend{}
	for _, row := range spendRows {
		spend[row.BagID] = append(spend[row.BagID], row)
	}
	return allocs, spend, nil
}
