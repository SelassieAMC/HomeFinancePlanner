package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"home-finance-planner/backend/internal/domain"
	"home-finance-planner/backend/internal/extractor"
)

// uvRow is one candidate the fake store holds: the row's name/unit as stored,
// the magnitude of its linked product (when any), and whether the row has
// already been resolved (the repository-side unit_value IS NULL guard).
type uvRow struct {
	id           int64
	name, unit   string
	productValue *float64
	productUnit  string
	resolved     bool
	value        float64
}

// fakeUnitValueStore implements all three repo interfaces plus the settings
// store over one in-memory row space — unit_value IS NULL semantics included.
type fakeUnitValueStore struct {
	mu     sync.Mutex
	tables map[string]map[int64]*uvRow
	marks  map[string]string
	err    error // write/list errors
}

func newFakeStore(tables ...map[int64]*uvRow) *fakeUnitValueStore {
	s := &fakeUnitValueStore{tables: map[string]map[int64]*uvRow{}, marks: map[string]string{}}
	names := []string{"products", "bill_items", "transaction_items"}
	for i, t := range tables {
		s.tables[names[i]] = t
	}
	for _, n := range names {
		if s.tables[n] == nil {
			s.tables[n] = map[int64]*uvRow{}
		}
	}
	return s
}

func row(id int64, name, unit string) *uvRow { return &uvRow{id: id, name: name, unit: unit} }

func (r *uvRow) linked(value float64, unit string) *uvRow {
	r.productValue = &value
	r.productUnit = unit
	return r
}

func (s *fakeUnitValueStore) list(table string, limit int) ([]domain.UnitValueRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	ids := make([]int64, 0, len(s.tables[table]))
	for id, r := range s.tables[table] {
		if !r.resolved {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]domain.UnitValueRow, 0, len(ids))
	for _, id := range ids {
		r := s.tables[table][id]
		out = append(out, domain.UnitValueRow{ID: id, Name: r.name, Unit: r.unit,
			ProductUnitValue: r.productValue, ProductUnit: r.productUnit})
	}
	return out, nil
}

func (s *fakeUnitValueStore) write(table string, fixes []domain.UnitValueFix) error {
	if len(fixes) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	for _, f := range fixes {
		r, ok := s.tables[table][f.ID]
		if !ok {
			return fmt.Errorf("%s %d: no such row", table, f.ID)
		}
		if r.resolved { // unit_value IS NULL guard — already decided, keep it
			continue
		}
		r.resolved = true
		r.value = f.UnitValue
		if f.Unit != nil && r.unit == "" {
			r.unit = *f.Unit
		}
	}
	return nil
}

// list/write forwarders: the three repo interfaces.
func (s *fakeUnitValueStore) ProductsMissingUnitValue(ctx context.Context, limit int) ([]domain.UnitValueRow, error) {
	return s.list("products", limit)
}
func (s *fakeUnitValueStore) BackfillProductUnitValues(ctx context.Context, fixes []domain.UnitValueFix) error {
	return s.write("products", fixes)
}
func (s *fakeUnitValueStore) BillItemsMissingUnitValue(ctx context.Context, limit int) ([]domain.UnitValueRow, error) {
	return s.list("bill_items", limit)
}
func (s *fakeUnitValueStore) BackfillBillItemUnitValues(ctx context.Context, fixes []domain.UnitValueFix) error {
	return s.write("bill_items", fixes)
}
func (s *fakeUnitValueStore) TransactionItemsMissingUnitValue(ctx context.Context, limit int) ([]domain.UnitValueRow, error) {
	return s.list("transaction_items", limit)
}
func (s *fakeUnitValueStore) BackfillTransactionItemUnitValues(ctx context.Context, fixes []domain.UnitValueFix) error {
	return s.write("transaction_items", fixes)
}

func (s *fakeUnitValueStore) Put(ctx context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.marks[key] = value
	return nil
}

func (s *fakeUnitValueStore) marked(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.marks[key]
	return ok
}

// fakeProviders resolves the default connector; it either answers with a fixed
// provider or fails (no connector configured for the purpose).
type fakeProviders struct {
	err  error
	call int
}

func (f *fakeProviders) DefaultBillProvider(ctx context.Context) (domain.AIProvider, error) {
	f.call++
	if f.err != nil {
		return domain.AIProvider{}, f.err
	}
	return domain.AIProvider{ID: "fake", Type: domain.AIProviderOllama, Model: "fake-vision"}, nil
}

type fakePrompts struct {
	prompt string
}

func (f *fakePrompts) ResolvePrompt(ctx context.Context, key string) (string, error) {
	if f.prompt != "" {
		return f.prompt, nil
	}
	return "resolve the printed sizes", nil
}

type fakeAI struct {
	mu      sync.Mutex
	answers map[string]extractor.UnitValueAnswer
	calls   []string
	err     error
}

func (f *fakeAI) UnitValues(ctx context.Context, provider domain.AIProvider, prompt string) ([]extractor.UnitValueAnswer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, prompt)
	if f.err != nil {
		return nil, f.err
	}
	out := make([]extractor.UnitValueAnswer, 0, len(f.answers))
	for _, a := range f.answers {
		out = append(out, a)
	}
	return out, nil
}

func newTestWorker(store *fakeUnitValueStore, providers *fakeProviders, ai *fakeAI) *UnitValueBackfillWorker {
	return NewUnitValueBackfillWorker(store, store, store, store, providers, &fakePrompts{}, ai, 50*time.Millisecond,
		slog.New(slog.DiscardHandler))
}

func f64(v float64) *float64 { return &v }

// TestWorkParsesCopiesAndAIFills covers the full happy path: product names both
// parseable and dimension-mismatched, line copies from resolved products, and
// the AI filling the rows the parser cannot.
func TestWorkParsesCopiesAndAIFills(t *testing.T) {
	store := newFakeStore(
		map[int64]*uvRow{
			// unit adopted from the parse
			1: row(1, "Water 500ml", ""),
			// parsed "l" vs stored "ml" — same dimension, unit kept
			2: row(2, "Cola 1,5l", "ml"),
			// unparseable — the AI pass resolves it
			3: row(3, "Alpine Cheese", "g"),
		},
		map[int64]*uvRow{
			// copy from product 1, unit adopted from the product
			10: row(10, "Water 500ml", "").linked(500, "ml"),
			// copy from product 2, stored unit kept (same dimension)
			11: row(11, "Cola 1,5l", "ml").linked(1.5, "l"),
			// unparseable line name, no product link — AI resolves it
			12: row(12, "Sliced Bread", ""),
		},
		map[int64]*uvRow{
			// transaction line copying product 2's magnitude
			20: row(20, "Cola 1,5l", "").linked(1.5, "l"),
		},
	)
	ai := &fakeAI{answers: map[string]extractor.UnitValueAnswer{
		"alpine cheese": {Name: "Alpine Cheese", Unit: "g", UnitValue: 250},
		"sliced bread":  {Name: "Sliced Bread", Unit: "g", UnitValue: 750},
	}}
	w := newTestWorker(store, &fakeProviders{}, ai)

	if err := w.Work(context.Background(), &river.Job[UnitValueBackfillArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	checks := []struct {
		table string
		id    int64
		want  float64
		unit  string
	}{
		{"products", 1, 500, "ml"}, // unit adopted
		{"products", 2, 1.5, "ml"}, // unit kept (dimension agreed)
		{"products", 3, 250, "g"},
		{"bill_items", 10, 500, "ml"}, // product 1's magnitude + its unit
		{"bill_items", 11, 1.5, "ml"},
		{"bill_items", 12, 750, "g"},
		{"transaction_items", 20, 1.5, "l"},
	}
	for _, c := range checks {
		r := store.tables[c.table][c.id]
		if !r.resolved || r.value != c.want {
			t.Errorf("%s %d: resolved=%v value=%v, want %v", c.table, c.id, r.resolved, r.value, c.want)
		}
		if r.unit != c.unit {
			t.Errorf("%s %d: unit = %q, want %q", c.table, c.id, r.unit, c.unit)
		}
	}
	if !store.marked(UnitValueCompletionKey) {
		t.Error("completion marker not written")
	}
	// The AI only ever sees the two unparseable rows (product 3, line 12), one
	// batch per table; 11/20 were resolved by copying and the transaction pass
	// has no candidates at all.
	if got := len(ai.calls); got != 2 {
		t.Errorf("AI calls = %d, want 2 (products batch, then the bill-items batch)", got)
	}
	for _, p := range ai.calls {
		if !strings.Contains(p, "never guess") { // prompt hint text present
			t.Errorf("prompt lost its instructions:\n%s", p)
		}
	}
}

// TestWorkWithoutAIConfigured verifies the actionable no-connector error and
// that the completion marker is NOT written (boot must keep the row missing).
func TestWorkWithoutAIConfigured(t *testing.T) {
	store := newFakeStore(
		map[int64]*uvRow{1: row(1, "Loaf of Bread", "")},
		map[int64]*uvRow{},
		map[int64]*uvRow{},
	)
	w := newTestWorker(store, &fakeProviders{err: fmt.Errorf("no connector flagged default_for_bills")}, &fakeAI{})

	err := w.Work(context.Background(), &river.Job[UnitValueBackfillArgs]{})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "default_for_bills") {
		t.Errorf("error not actionable: %v", err)
	}
	if store.marked(UnitValueCompletionKey) {
		t.Error("marker written despite unresolved rows")
	}
	if r := store.tables["products"][1]; r.resolved {
		t.Error("row resolved without AI")
	}
}

// TestWorkAIFailureBubbles: a failing AI call returns an error (River retries
// the job) and never writes the marker.
func TestWorkAIFailureBubbles(t *testing.T) {
	store := newFakeStore(
		map[int64]*uvRow{1: row(1, "Mysterious Loaf", "")},
		map[int64]*uvRow{},
		map[int64]*uvRow{},
	)
	w := newTestWorker(store, &fakeProviders{}, &fakeAI{err: fmt.Errorf("model down")})

	if err := w.Work(context.Background(), &river.Job[UnitValueBackfillArgs]{}); err == nil {
		t.Fatal("want error, got nil")
	}
	if store.marked(UnitValueCompletionKey) {
		t.Error("marker written despite AI failure")
	}
}

// TestWorkUnresolvableNamesEndOnePass: names the AI cannot answer are marked
// asked and the pass ends (a second scan with the same name returns no new
// work) instead of looping until backfillPassMax.
func TestWorkUnresolvableNamesEndOnePass(t *testing.T) {
	store := newFakeStore(
		map[int64]*uvRow{1: row(1, "Handmade Jam", "")},
		map[int64]*uvRow{2: row(2, "Mystery Cheese", "")},
		map[int64]*uvRow{},
	)
	ai := &fakeAI{} // answers nothing
	w := newTestWorker(store, &fakeProviders{}, ai)

	if err := w.Work(context.Background(), &river.Job[UnitValueBackfillArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	// one batch per table: the unresolvable rows are asked once each
	if got := len(ai.calls); got != 2 {
		t.Errorf("AI calls = %d, want 2 (asked-set must end the passes)", got)
	}
	if store.tables["products"][1].resolved || store.tables["bill_items"][2].resolved {
		t.Error("unresolvable rows must stay unresolved")
	}
	if !store.marked(UnitValueCompletionKey) {
		t.Error("completion marker must still be written — boot would otherwise re-enqueue forever")
	}
}

// TestWorkDimensionRefusalLeavesRowForAI: a parse whose unit contradicts the
// stored one ("6 stk" on a row decided as grams) is not applied by the parser
// — and the repeating the same contradiction, the AI's pcs answer is refused
// too, so the row stays unknown (magnitudes are never guessed against a
// decided unit) while the pass still terminates via the asked-set.
func TestWorkDimensionRefusalLeavesRowForAI(t *testing.T) {
	store := newFakeStore(
		map[int64]*uvRow{1: row(1, "Eggs 6 stk fresh", "g")},
		map[int64]*uvRow{},
		map[int64]*uvRow{},
	)
	ai := &fakeAI{answers: map[string]extractor.UnitValueAnswer{
		"eggs 6 stk fresh": {Name: "Eggs 6 stk fresh", Unit: "pcs", UnitValue: 6},
	}}
	w := newTestWorker(store, &fakeProviders{}, ai)

	if err := w.Work(context.Background(), &river.Job[UnitValueBackfillArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	r := store.tables["products"][1]
	if r.resolved {
		t.Errorf("row resolved as %v — a pcs answer must never overwrite a decided unit g", r.value)
	}
	if got := len(ai.calls); got != 1 {
		t.Errorf("AI calls = %d, want 1 (asked-set ended the pass)", got)
	}
	if !store.marked(UnitValueCompletionKey) {
		t.Error("completion marker must still be written")
	}
}
