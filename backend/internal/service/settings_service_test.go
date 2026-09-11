package service

import (
	"context"
	"errors"
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
