package fx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchRatesParsesSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"amount": 1.0,
			"base": "EUR",
			"date": "2026-09-10",
			"rates": {"USD": 1.0934, "GBP": 0.8507, "PLN": 4.2811}
		}`))
	}))
	defer srv.Close()
	orig := fetchURL
	fetchURL = srv.URL
	t.Cleanup(func() { fetchURL = orig })

	f := New(2 * time.Second)
	snap, err := f.FetchRates(context.Background())
	if err != nil {
		t.Fatalf("FetchRates: %v", err)
	}
	if snap.Pivot != "EUR" {
		t.Errorf("Pivot = %q, want EUR", snap.Pivot)
	}
	if snap.Date != "2026-09-10" {
		t.Errorf("Date = %q, want 2026-09-10", snap.Date)
	}
	if snap.Rates["USD"] != 1.0934 {
		t.Errorf("Rates[USD] = %v, want 1.0934", snap.Rates["USD"])
	}
	if rate, ok := snap.Rate("EUR", "USD"); !ok || rate != 1.0934 {
		t.Errorf("Rate(EUR,USD) = %v ok=%v, want 1.0934 true", rate, ok)
	}
	if rate, ok := snap.Rate("USD", "EUR"); !ok || rate < 0.9145 || rate > 0.9146 {
		t.Errorf("Rate(USD,EUR) = %v ok=%v, want ~0.9146 true", rate, ok)
	}
	if snap.FetchedAt.IsZero() {
		t.Error("FetchedAt not set")
	}
}

func TestFetchRatesRejectsBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	orig := fetchURL
	fetchURL = srv.URL
	t.Cleanup(func() { fetchURL = orig })

	if _, err := New(time.Second).FetchRates(context.Background()); err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

func TestFetchRatesRejectsGarbage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json at all"))
	}))
	defer srv.Close()
	orig := fetchURL
	fetchURL = srv.URL
	t.Cleanup(func() { fetchURL = orig })

	if _, err := New(time.Second).FetchRates(context.Background()); err == nil {
		t.Fatal("expected error on garbage body, got nil")
	}
}

func TestFetchRatesRejectsEmptyTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"amount":1.0,"base":"EUR","date":"2026-09-10","rates":{}}`))
	}))
	defer srv.Close()
	orig := fetchURL
	fetchURL = srv.URL
	t.Cleanup(func() { fetchURL = orig })

	if _, err := New(time.Second).FetchRates(context.Background()); err == nil {
		t.Fatal("expected error on empty rates table, got nil")
	}
}
