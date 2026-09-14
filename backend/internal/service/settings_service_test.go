package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

func newSettingsServiceForTest(store *fakeSettingsStore) *SettingsService {
	return NewSettingsService(store, passthroughBox{}, nil)
}

func TestBaseCurrencyDefaultsWhenUnset(t *testing.T) {
	store := &fakeSettingsStore{data: map[string]string{}}
	svc := newSettingsServiceForTest(store)

	cur, err := svc.BaseCurrency(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cur != DefaultBaseCurrency {
		t.Errorf("BaseCurrency() = %q, want default %q", cur, DefaultBaseCurrency)
	}
}

func TestBaseCurrencyTreatsCorruptValueAsUnset(t *testing.T) {
	store := &fakeSettingsStore{data: map[string]string{settingsKeyBaseCurrency: "12"}}
	svc := newSettingsServiceForTest(store)

	cur, err := svc.BaseCurrency(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cur != DefaultBaseCurrency {
		t.Errorf("BaseCurrency() = %q, want default %q for corrupt stored value", cur, DefaultBaseCurrency)
	}
}

func TestSaveBaseCurrencyNormalizes(t *testing.T) {
	store := &fakeSettingsStore{data: map[string]string{}}
	svc := newSettingsServiceForTest(store)

	cur, err := svc.SaveBaseCurrency(context.Background(), " eur ")
	if err != nil {
		t.Fatal(err)
	}
	if cur != "EUR" {
		t.Errorf("SaveBaseCurrency() = %q, want EUR", cur)
	}
	if store.data[settingsKeyBaseCurrency] != "EUR" {
		t.Errorf("stored = %q, want EUR", store.data[settingsKeyBaseCurrency])
	}
	// Round-trips through BaseCurrency.
	got, err := svc.BaseCurrency(context.Background())
	if err != nil || got != "EUR" {
		t.Errorf("BaseCurrency() = %q err=%v, want EUR nil", got, err)
	}
}

func TestSaveBaseCurrencyRejectsInvalid(t *testing.T) {
	store := &fakeSettingsStore{data: map[string]string{}}
	svc := newSettingsServiceForTest(store)

	for _, bad := range []string{"", "EU", "EURO", "E1R", "€"} {
		if _, err := svc.SaveBaseCurrency(context.Background(), bad); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("SaveBaseCurrency(%q) error = %v, want domain.ErrValidation", bad, err)
		}
	}
	if _, ok := store.data[settingsKeyBaseCurrency]; ok {
		t.Error("invalid currency must not be persisted")
	}
}

// --- AI connector configuration ----------------------------------------------

// wrapBox simulates encryption so stored and re-encrypted keys are
// distinguishable (passthroughBox would make them identical).
type wrapBox struct{}

func (wrapBox) EncryptString(s string) (string, error) { return "enc:" + s, nil }
func (wrapBox) DecryptString(s string) (string, error) {
	return strings.TrimPrefix(s, "enc:"), nil
}

func newProviderTestService(store *fakeSettingsStore) *SettingsService {
	return NewSettingsService(store, wrapBox{}, nil)
}

func seedProviders(t *testing.T, store *fakeSettingsStore, providers []domain.AIProvider) {
	t.Helper()
	raw, err := json.Marshal(providers)
	if err != nil {
		t.Fatal(err)
	}
	store.data[settingsKeyAIProviders] = string(raw)
}

// storedProvidersFromStore decodes what a save persisted for assertions.
func storedProvidersFromStore(t *testing.T, store *fakeSettingsStore) []domain.AIProvider {
	t.Helper()
	raw, ok := store.data[settingsKeyAIProviders]
	if !ok {
		return nil
	}
	out, err := decodeStoredProviders(raw)
	if err != nil {
		t.Fatalf("decode persisted providers: %v", err)
	}
	return out
}

func TestSaveGeneratesIDsIndependentOfStored(t *testing.T) {
	store := &fakeSettingsStore{data: map[string]string{}}
	seedProviders(t, store, []domain.AIProvider{
		{ID: "provider-1", Type: domain.AIProviderOllama, Model: "m1"},
		{ID: "provider-3", Type: domain.AIProviderOllama, Model: "m3"},
	})
	svc := newSettingsServiceForTest(store)

	// Two new connectors with no IDs must get fresh, distinct ids that do not
	// merge into the stored provider-1/provider-3 configs.
	out, err := svc.SaveAIProviders(context.Background(), []ProviderInput{
		{Type: domain.AIProviderOllama, Model: "a"},
		{Type: domain.AIProviderOllama, Model: "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{out[0].ID, out[1].ID}
	if ids[0] == ids[1] {
		t.Errorf("both new connectors got id %q", ids[0])
	}
	for _, id := range ids {
		if id == "provider-1" || id == "provider-3" {
			t.Errorf("new connector reused stored id %q — configs would merge", id)
		}
	}
}

func TestSaveKeepsEachConnectorsKeyIndependent(t *testing.T) {
	store := &fakeSettingsStore{data: map[string]string{}}
	seedProviders(t, store, []domain.AIProvider{
		{ID: "provider-1", Type: domain.AIProviderOpenAI, APIKey: "enc:sk-old", Model: "gpt"},
	})
	svc := NewSettingsService(store, wrapBox{}, nil)

	// Save two connectors: provider-1 with an empty key (keep stored) and a
	// brand-new one with its own key.
	_, err := svc.SaveAIProviders(context.Background(), []ProviderInput{
		{ID: "provider-1", Type: domain.AIProviderOpenAI, Model: "gpt"},
		{Type: domain.AIProviderOpenAI, APIKey: "sk-new", Model: "gpt"},
	})
	if err != nil {
		t.Fatal(err)
	}

	stored := storedProvidersFromStore(t, store)
	if len(stored) != 2 {
		t.Fatalf("len(stored) = %d, want 2", len(stored))
	}
	var aKey, bKey string
	for _, p := range stored {
		if p.ID == "provider-1" {
			aKey = p.APIKey
		} else {
			bKey = p.APIKey
		}
	}
	if aKey != "enc:sk-old" {
		t.Errorf("provider-1 api key changed: %q, want stored key kept", aKey)
	}
	if bKey != "enc:sk-new" {
		t.Errorf("new connector api key = %q, want its own encrypted key", bKey)
	}
}

// A keyless new hosted connector must fail validation, not silently inherit
// another connector's stored key via a fabricated id.
func TestSaveRejectsKeylessNewConnectorWithoutInheritance(t *testing.T) {
	store := &fakeSettingsStore{data: map[string]string{}}
	seedProviders(t, store, []domain.AIProvider{
		{ID: "provider-1", Type: domain.AIProviderOpenAI, APIKey: "enc:sk-old", Model: "gpt"},
	})
	svc := newSettingsServiceForTest(store)

	_, err := svc.SaveAIProviders(context.Background(), []ProviderInput{
		{Type: domain.AIProviderOpenAI, Model: "gpt"}, // empty id, empty key
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("error = %v, want domain.ErrValidation", err)
	}
}

func TestSaveRejectsDuplicateIDs(t *testing.T) {
	store := &fakeSettingsStore{data: map[string]string{}}
	svc := newSettingsServiceForTest(store)

	_, err := svc.SaveAIProviders(context.Background(), []ProviderInput{
		{ID: "provider-2", Type: domain.AIProviderOllama, Model: "a"},
		{ID: "provider-2", Type: domain.AIProviderOllama, Model: "b"},
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Errorf("error = %v, want domain.ErrValidation for duplicate ids", err)
	}
}

func TestSaveNormalizesDefaultFlag(t *testing.T) {
	ctx := context.Background()

	t.Run("none marked marks the first", func(t *testing.T) {
		store := &fakeSettingsStore{data: map[string]string{}}
		svc := newSettingsServiceForTest(store)
		out, err := svc.SaveAIProviders(ctx, []ProviderInput{
			{Type: domain.AIProviderOllama, Model: "a"},
			{Type: domain.AIProviderOllama, Model: "b"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !out[0].IsDefault || out[1].IsDefault {
			t.Errorf("defaults = %v/%v, want true/false", out[0].IsDefault, out[1].IsDefault)
		}
	})

	t.Run("several marked keeps the first", func(t *testing.T) {
		store := &fakeSettingsStore{data: map[string]string{}}
		svc := newSettingsServiceForTest(store)
		out, err := svc.SaveAIProviders(ctx, []ProviderInput{
			{Type: domain.AIProviderOllama, Model: "a", IsDefault: true},
			{Type: domain.AIProviderOllama, Model: "b", IsDefault: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !out[0].IsDefault || out[1].IsDefault {
			t.Errorf("defaults = %v/%v, want first marked to win", out[0].IsDefault, out[1].IsDefault)
		}
	})

	t.Run("single connector is always default", func(t *testing.T) {
		store := &fakeSettingsStore{data: map[string]string{}}
		svc := newSettingsServiceForTest(store)
		out, err := svc.SaveAIProviders(ctx, []ProviderInput{
			{Type: domain.AIProviderOllama, Model: "a"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !out[0].IsDefault {
			t.Error("single connector must be flagged default")
		}
	})

	t.Run("removing the default promotes a remaining one", func(t *testing.T) {
		store := &fakeSettingsStore{data: map[string]string{}}
		seedProviders(t, store, []domain.AIProvider{
			{ID: "provider-1", Type: domain.AIProviderOllama, Model: "a", IsDefault: true},
			{ID: "provider-2", Type: domain.AIProviderOllama, Model: "b"},
		})
		svc := newSettingsServiceForTest(store)
		out, err := svc.SaveAIProviders(ctx, []ProviderInput{
			{ID: "provider-2", Type: domain.AIProviderOllama, Model: "b"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || !out[0].IsDefault {
			t.Errorf("remaining = %+v, want provider-2 flagged default", out)
		}
	})
}

func TestListAIProvidersNormalizesLegacyStored(t *testing.T) {
	store := &fakeSettingsStore{data: map[string]string{}}
	// Legacy JSON saved before is_default existed.
	store.data[settingsKeyAIProviders] =
		`[{"id":"provider-1","type":"ollama","model":"a"},{"id":"provider-2","type":"ollama","model":"b"}]`
	svc := newSettingsServiceForTest(store)

	out, err := svc.ListAIProviders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !out[0].IsDefault || out[1].IsDefault {
		t.Errorf("legacy list defaults = %v/%v, want first flagged on read", out[0].IsDefault, out[1].IsDefault)
	}
}

func TestDefaultProvider(t *testing.T) {
	ctx := context.Background()

	t.Run("honors the flagged connector", func(t *testing.T) {
		store := &fakeSettingsStore{data: map[string]string{}}
		seedProviders(t, store, []domain.AIProvider{
			{ID: "provider-1", Type: domain.AIProviderOllama, Model: "a"},
			{ID: "provider-2", Type: domain.AIProviderOllama, Model: "b", IsDefault: true},
		})
		svc := newSettingsServiceForTest(store)
		p, err := svc.DefaultProvider(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if p.ID != "provider-2" {
			t.Errorf("DefaultProvider() = %q, want provider-2", p.ID)
		}
	})

	t.Run("falls back to the first for legacy lists", func(t *testing.T) {
		store := &fakeSettingsStore{data: map[string]string{}}
		seedProviders(t, store, []domain.AIProvider{
			{ID: "provider-1", Type: domain.AIProviderOllama, Model: "a"},
			{ID: "provider-2", Type: domain.AIProviderOllama, Model: "b"},
		})
		svc := newSettingsServiceForTest(store)
		p, err := svc.DefaultProvider(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if p.ID != "provider-1" {
			t.Errorf("DefaultProvider() = %q, want provider-1 fallback", p.ID)
		}
	})

	t.Run("not found when none configured", func(t *testing.T) {
		store := &fakeSettingsStore{data: map[string]string{}}
		svc := newSettingsServiceForTest(store)
		if _, err := svc.DefaultProvider(ctx); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("error = %v, want domain.ErrNotFound", err)
		}
	})
}
