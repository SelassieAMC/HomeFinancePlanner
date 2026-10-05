package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// Fakes for the engine's narrow interfaces (the unit-value worker's test
// style, insight.go: consumer-side contracts).
type fakeInsightStore struct {
	created []domain.ProductInsight
	recent  bool
}

func (f *fakeInsightStore) Create(_ context.Context, ins domain.ProductInsight) (domain.ProductInsight, error) {
	ins.ID = int64(len(f.created) + 1)
	f.created = append(f.created, ins)
	return ins, nil
}

func (f *fakeInsightStore) RecentlyInsighted(_ context.Context, _ domain.ProductInsightKind, _ int64, _ time.Time) (bool, error) {
	return f.recent, nil
}

type fakeInsightPurchases struct{ lines []domain.InsightLine }

func (f *fakeInsightPurchases) BillLines(_ context.Context, _ int64) ([]domain.InsightLine, error) {
	return f.lines, nil
}

func (f *fakeInsightPurchases) TransactionLines(_ context.Context, _ int64) ([]domain.InsightLine, error) {
	return f.lines, nil
}

type fakeInsightHistory struct {
	product, family []domain.ProductHistoryEntry
}

func (f *fakeInsightHistory) ProductPurchaseHistory(_ context.Context, _ int64, limit int) ([]domain.ProductHistoryEntry, error) {
	out := f.product
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeInsightHistory) FamilyPurchaseHistory(_ context.Context, _ string, limit int) ([]domain.ProductHistoryEntry, error) {
	return f.family[:min(limit, len(f.family))], nil
}

type fakeInsightProducts struct{ p domain.Product }

func (f *fakeInsightProducts) GetByID(_ context.Context, _ int64) (domain.Product, error) {
	return f.p, nil
}

type fakeInsightProvider struct {
	p   domain.AIProvider
	err bool
}

func (f *fakeInsightProvider) DefaultBillProvider(_ context.Context) (domain.AIProvider, error) {
	if f.err {
		return domain.AIProvider{}, fmt.Errorf("no connector flagged default_for_bills")
	}
	return f.p, nil
}

type fakeInsightPrompts struct{ text string }

func (f *fakeInsightPrompts) ResolvePrompt(_ context.Context, _ string) (string, error) {
	return f.text, nil
}

type fakeInsightAI struct{ reply string }

func (f *fakeInsightAI) CompleteText(_ context.Context, _ domain.AIProvider, prompt string) (string, error) {
	return f.reply + " [" + prompt[:12] + "]", nil
}

// fakeLines sets the purchase lines the engine reads during a test.
func fakeLines(lines ...domain.InsightLine) *fakeInsightPurchases {
	fakeLinesStore = &fakeInsightPurchases{lines: lines}
	return fakeLinesStore
}

// fakeInsightBillLines points analyze at the fake purchase store's lines.
func fakeInsightBillLines(_ context.Context, _ int64) ([]domain.InsightLine, error) {
	return fakeLinesStore.lines, nil
}

// fakeLinesStore remembers the lines of the last fakeLines() call.
var fakeLinesStore *fakeInsightPurchases

// Without a configured connector the finding is still persisted — in its
// deterministic auto wording, source "auto".
func TestEngineWithoutAIKeepsAutoWording(t *testing.T) {
	store := &fakeInsightStore{}
	engine := newInsightEngine(store,
		&fakeInsightHistory{product: []domain.ProductHistoryEntry{hist("2026-09-01", "g", 200, 200)}},
		fakeLines(line("g", 150, 200)),
		&fakeInsightProducts{p: product()},
		&fakeInsightProvider{err: true}, // no connector configured
		&fakeInsightPrompts{text: "prompt"},
		nil, // the AI is never asked without a provider
		10, 0, slog.New(slog.DiscardHandler))

	if err := engine.analyze(context.Background(), fakeInsightBillLines, "b", 1, "test bill"); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected one saved insight, got %d", len(store.created))
	}
	ins := store.created[0]
	if ins.Source != "auto" || ins.Message == "" || ins.Kind != domain.ProductInsightShrinkflation {
		t.Fatalf("unexpected insight %+v", ins)
	}
	if ins.ProductID == nil || *ins.ProductID != 1 || ins.Currency != "EUR" {
		t.Fatalf("facts missing: %+v", ins)
	}
}

// With a connector, the AI answer replaces the auto wording (source "ai").
func TestEnginePhrasesWithAI(t *testing.T) {
	store := &fakeInsightStore{}
	engine := newInsightEngine(store,
		&fakeInsightHistory{product: []domain.ProductHistoryEntry{hist("2026-09-01", "g", 200, 200)}},
		fakeLines(line("g", 150, 200)),
		&fakeInsightProducts{p: product()},
		&fakeInsightProvider{p: domain.AIProvider{Type: domain.AIProviderOllama, Model: "x"}},
		&fakeInsightPrompts{text: "prompt"},
		&fakeInsightAI{reply: "  Your chips shrunk!  "},
		10, 0, slog.New(slog.DiscardHandler))

	if err := engine.analyze(context.Background(), fakeInsightBillLines, "b", 1, "test bill"); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected one saved insight, got %d", len(store.created))
	}
	ins := store.created[0]
	if ins.Source != "ai" {
		t.Fatalf("expected source ai, got %q", ins.Source)
	}
	if !strings.HasPrefix(ins.Message, "Your chips shrunk!") {
		t.Fatalf("expected the AI wording, got %q", ins.Message)
	}
	if ins.Data.ChangePct < 30 || ins.Data.ChangePct > 40 {
		t.Fatalf("expected the ~33%% change in the facts, got %.1f", ins.Data.ChangePct)
	}
}

// Cooldown suppression: a kind+product pair nudged recently is not repeated.
func TestEngineCooldownSuppressesRepeat(t *testing.T) {
	store := &fakeInsightStore{recent: true}
	engine := newInsightEngine(store,
		&fakeInsightHistory{product: []domain.ProductHistoryEntry{hist("2026-09-01", "g", 200, 200)}},
		fakeLines(line("g", 150, 200)),
		&fakeInsightProducts{p: product()},
		&fakeInsightProvider{err: true},
		&fakeInsightPrompts{text: "prompt"},
		nil,
		10, 0, slog.New(slog.DiscardHandler))

	if err := engine.analyze(context.Background(), fakeInsightBillLines, "b", 1, "test bill"); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if len(store.created) != 0 {
		t.Fatalf("cooldown must suppress the finding, got %+v", store.created)
	}
}

// An all-deposit or sizeless bill is a no-op (nothing analyzable, no error).
func TestEngineSkipsUnanalyzableLines(t *testing.T) {
	store := &fakeInsightStore{}
	uv := 0.0
	engine := newInsightEngine(store,
		&fakeInsightHistory{},
		fakeLines(
			domain.InsightLine{ProductID: 1, Name: "Leergut", Unit: "pcs", UnitValue: &uv, UnitPriceCents: -150, Currency: "EUR", Date: "2026-10-05"},
			domain.InsightLine{ProductID: 1, Name: "Something", Currency: "EUR", Date: "2026-10-05"}, // no size
			domain.InsightLine{ProductID: 0, Name: "Unlinked", Unit: "g", UnitValue: &uv, UnitPriceCents: 100, Currency: "EUR", Date: "2026-10-05"},
		),
		&fakeInsightProducts{p: product()},
		&fakeInsightProvider{err: true},
		&fakeInsightPrompts{text: "prompt"},
		nil,
		10, 0, slog.New(slog.DiscardHandler))

	if err := engine.analyze(context.Background(), fakeInsightBillLines, "b", 1, "test bill"); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if len(store.created) != 0 {
		t.Fatalf("unanalyzable lines must not produce insights, got %+v", store.created)
	}
}
