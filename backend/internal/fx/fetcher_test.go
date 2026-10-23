package fx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fetcherTestEnv patches both endpoints onto one local handler. The real
// network must never be touched by these tests: any path the handler does
// not recognize answers 500 (a dead fallback source), which exercises the
// "fallback failed, primary unaffected" behavior.
func fetcherTestEnv(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	origPrimary, origFallback := fetchURL, fallbackURL
	fetchURL, fallbackURL = srv.URL+"/primary", srv.URL+"/fallback"
	t.Cleanup(func() { fetchURL, fallbackURL = origPrimary, origFallback })
}

func fetcherTestEnvSplit(t *testing.T, primary, fallback http.HandlerFunc) {
	t.Helper()
	p := httptest.NewServer(primary)
	fb := httptest.NewServer(fallback)
	t.Cleanup(func() { p.Close(); fb.Close() })
	origPrimary, origFallback := fetchURL, fallbackURL
	fetchURL, fallbackURL = p.URL, fb.URL
	t.Cleanup(func() { fetchURL, fallbackURL = origPrimary, origFallback })
}

func TestFetchRatesParsesSnapshot(t *testing.T) {
	fetcherTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"amount": 1.0,
			"base": "EUR",
			"date": "2026-09-10",
			"rates": {"USD": 1.0934, "GBP": 0.8507, "PLN": 4.2811}
		}`))
	})

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
	fetcherTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	if _, err := New(time.Second).FetchRates(context.Background()); err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

func TestFetchRatesRejectsGarbage(t *testing.T) {
	fetcherTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json at all"))
	})

	if _, err := New(time.Second).FetchRates(context.Background()); err == nil {
		t.Fatal("expected error on garbage body, got nil")
	}
}

func TestFetchRatesRejectsEmptyTable(t *testing.T) {
	fetcherTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"amount":1.0,"base":"EUR","date":"2026-09-10","rates":{}}`))
	})

	if _, err := New(time.Second).FetchRates(context.Background()); err == nil {
		t.Fatal("expected error on empty rates table, got nil")
	}
}

// erAPIBody is the fallback wire shape with the given rates.
func erAPIBody(rates string) string {
	return `{"result":"success","base_code":"EUR","rates":` + rates + `}`
}

// TestFetchRatesFillsMissingFromFallback pins the COP fix: the ECB table
// (Frankfurter) does not publish the Latin American currencies — the fallback
// source fills exactly the missing codes and never overrides a primary rate.
func TestFetchRatesFillsMissingFromFallback(t *testing.T) {
	fetcherTestEnvSplit(t,
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"amount":1.0,"base":"EUR","date":"2026-09-10","rates":{"USD":1.0934,"GBP":0.8507}}`))
		},
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(erAPIBody(`{"COP":4100.5,"USD":1.12,"EUR":1}`)))
		})

	snap, err := New(2 * time.Second).FetchRates(context.Background())
	if err != nil {
		t.Fatalf("FetchRates: %v", err)
	}
	if snap.Rates["COP"] != 4100.5 {
		t.Errorf("Rates[COP] = %v, want filled from the fallback (4100.5)", snap.Rates["COP"])
	}
	// The primary's own values win on overlap.
	if snap.Rates["USD"] != 1.0934 {
		t.Errorf("Rates[USD] = %v, want the primary 1.0934, not the fallback", snap.Rates["USD"])
	}
	if rate, ok := snap.Rate("EUR", "COP"); !ok || rate != 4100.5 {
		t.Errorf("Rate(EUR,COP) = %v ok=%v, want 4100.5 true — the 1:1 degradation is gone", rate, ok)
	}
}

// TestFetchRatesFallbackServesWholeTable: the primary source down → the
// fallback is the snapshot (still pivot EUR, still usable).
func TestFetchRatesFallbackServesWholeTable(t *testing.T) {
	fetcherTestEnvSplit(t,
		func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "primary down", http.StatusInternalServerError)
		},
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(erAPIBody(`{"COP":4100,"USD":1.09}`)))
		})

	snap, err := New(2 * time.Second).FetchRates(context.Background())
	if err != nil {
		t.Fatalf("FetchRates: %v", err)
	}
	if snap.Pivot != "EUR" {
		t.Errorf("Pivot = %q, want EUR", snap.Pivot)
	}
	if snap.Rates["COP"] != 4100 {
		t.Errorf("Rates[COP] = %v, want the fallback table", snap.Rates["COP"])
	}
}

// TestFetchRatesFallbackFailureNotFatal: the fallback failing alone keeps the
// primary snapshot usable — a partial-but-honest table beats refusing the
// refresh.
func TestFetchRatesFallbackFailureNotFatal(t *testing.T) {
	fetcherTestEnvSplit(t,
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"amount":1.0,"base":"EUR","date":"2026-09-10","rates":{"USD":1.0934}}`))
		},
		func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "fallback down", http.StatusInternalServerError)
		})

	snap, err := New(2 * time.Second).FetchRates(context.Background())
	if err != nil {
		t.Fatalf("FetchRates: %v", err)
	}
	if snap.Rates["USD"] != 1.0934 {
		t.Errorf("Rates[USD] = %v, want the primary table", snap.Rates["USD"])
	}
}

// TestFetchRatesFallbackRejectsOtherPivot: a fallback that answers in
// another base currency is rejected — its rates are quoted against the wrong
// pivot and would poison every conversion.
func TestFetchRatesFallbackRejectsOtherPivot(t *testing.T) {
	fetcherTestEnvSplit(t,
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"amount":1.0,"base":"EUR","date":"2026-09-10","rates":{"USD":1.0934}}`))
		},
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"result":"success","base_code":"USD","rates":{"COP":4600}}`))
		})

	snap, err := New(2 * time.Second).FetchRates(context.Background())
	if err != nil {
		t.Fatalf("FetchRates: %v", err)
	}
	if _, ok := snap.Rates["COP"]; ok {
		t.Errorf("Rates[COP] taken from a USD-quoted response — must be rejected")
	}
}
