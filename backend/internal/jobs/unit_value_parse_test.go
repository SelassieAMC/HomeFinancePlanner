package jobs

import (
	"math"
	"testing"
	"time"

	"github.com/riverqueue/river/rivertype"
)

func TestParsePrintedSize(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantValue float64
		wantUnit  string
		wantOK    bool
	}{
		// plain sizes
		{"grams", "Yoghurt Natural 500g", 500, "g", true},
		{"gramm", "Mehl Type 405 1.000 Gramm", 1000, "g", true},
		{"upper gram", "Pasta Pennette 500 GR", 500, "g", true},
		{"grams space", "Chips Paprika 175 g", 175, "g", true},
		{"kilogram", "Wheat Flour 1 kg", 1, "kg", true},
		{"milliliters", "Sunflower Oil 500ml", 500, "ml", true},
		{"litre comma decimal", "Cola Zero 1,5l", 1.5, "l", true},
		{"liter dot decimal", "Milk 1.5 L", 1.5, "l", true},
		{"liter long word", "Water Still 1 liter", 1, "l", true},
		{"litre abbreviation", "Juice Orange 1 ltr", 1, "l", true},
		{"cl scales to ml", "Cola Dose 33 cl", 330, "ml", true},
		{"dl scales to ml", "Cream 2 dl", 200, "ml", true},
		{"two-pack keeps item size", "Chocolate Bar 2 x 500g", 500, "g", true},
		{"fat qualifier skipped", "Milch 3,5% Fett 1l", 1, "l", true},
		{"german thousands mass", "Zucker Fein 1.000 g", 1000, "g", true},
		{"german thousands kg", "Rice 12.500 kg", 12500, "kg", true},
		{"volume dot three digits stays decimal", "Milk 1.500 l", 1.5, "l", true},
		{"pcs", "Eier Freiland 6 Stk.", 6, "pcs", true},
		{"piece", "Yogurt Cup 4 pieces", 4, "pcs", true},
		{"stueck", "Bun Laugen 5 Stück", 5, "pcs", true},
		{"ct counted as pcs", "Nuggets 20 ct", 20, "pcs", true},
		{"zero-prefixed decimal", "Soda 0,5l", 0.5, "l", true},
		{"zero-prefixed dot decimal", "Soda 0.5 l", 0.5, "l", true},
		// non-sizes: no match or non-unit suffix
		{"no digit", "Butter Mild", 0, "", false},
		{"no unit", "Type 405 Flour", 0, "", false},
		{"price tail", "Total 3,99 EUR", 0, "", false},
		{"percent not a unit", "Cola 3,5% 6pack", 0, "", false},
		{"gb not grams", "Card 32 Gb", 0, "", false},
		{"granulat not grams", "Soup Granulat 40", 0, "", false},
		{"negative size rejected", "Deposit Credit -0,25l Refund", 0, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, unit, ok := ParsePrintedSize(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (value=%v unit=%q)", ok, tt.wantOK, value, unit)
			}
			if !tt.wantOK {
				return
			}
			if math.Abs(value-tt.wantValue) > 1e-9 {
				t.Errorf("value = %v, want %v", value, tt.wantValue)
			}
			if unit != tt.wantUnit {
				t.Errorf("unit = %q, want %q", unit, tt.wantUnit)
			}
		})
	}
}

func TestCanonicalUnit(t *testing.T) {
	tests := []struct{ in, want string }{
		{"kg", "kg"}, {"KG", "kg"}, {"kilogramm", "kg"}, {"kgs", "kg"},
		{"g", "g"}, {"GR", "g"}, {"grams", "g"}, {"gramm", "g"},
		{"l", "l"}, {"lt", "l"}, {"ltr", "l"}, {"liter", "l"}, {"litre", "l"},
		{"ml", "ml"}, {"milliliter", "ml"}, {"Millilitre", "ml"},
		{"pcs", "pcs"}, {"pc", "pcs"}, {"stk", "pcs"}, {"STÜCK", "pcs"}, {"stck", "pcs"}, {"ea", "pcs"},
		{"", ""}, {"bars", ""}, {"pieces of 8", ""},
	}
	for _, tt := range tests {
		if got := canonicalUnit(tt.in); got != tt.want {
			t.Errorf("canonicalUnit(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLLMRetryPolicy(t *testing.T) {
	p := newRetryPolicy(2 * time.Minute) // floor 4m
	base := time.Now()
	cases := []struct {
		attempt int
		wantMin time.Duration
	}{
		{1, 4 * time.Minute},   // first retry: the full AI timeout ×2
		{2, 8 * time.Minute},   // 2^(2-1)
		{3, 16 * time.Minute},  // 2^(3-1)
		{7, 256 * time.Minute}, // 4m × 64 (attempts multiply up to ×2^7)
		{9, 512 * time.Minute}, // 4m × 128 — the multiplier saturates here
		{30, 512 * time.Minute},
	}
	for _, c := range cases {
		got := p.NextRetry(&rivertype.JobRow{Attempt: c.attempt})
		if diff := got.Sub(base); diff < c.wantMin {
			t.Errorf("attempt %d: backoff %v < wanted %v", c.attempt, diff, c.wantMin)
		}
		// never more than 24h + 1s over the base (timing slack)
		if maxDiff := 24*time.Hour + time.Second; got.Sub(base) > maxDiff {
			t.Errorf("attempt %d: backoff %v exceeds cap", c.attempt, got.Sub(base))
		}
	}
	if got := newRetryPolicy(time.Second).NextRetry(&rivertype.JobRow{Attempt: 1}); got.Sub(base) < 30*time.Second {
		t.Errorf("floor must not drop below 30s, got %v", got.Sub(base))
	}
	// a big floor hits the 24h cap: 12h × 4 at attempt 3 overflows it
	if got := newRetryPolicy(6 * time.Hour).NextRetry(&rivertype.JobRow{Attempt: 3}); got.Sub(base) > (24*time.Hour + time.Second) {
		t.Errorf("cap breached: %v", got.Sub(base))
	}
}
