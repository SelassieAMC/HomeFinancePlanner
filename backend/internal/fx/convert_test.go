package fx

import "testing"

func TestConvertCents(t *testing.T) {
	tests := []struct {
		name  string
		cents int64
		rate  float64
		want  int64
	}{
		{"identity", 1234, 1, 1234},
		{"exact multiply", 200, 0.5, 100},
		{"half rounds away from zero", 5, 0.5, 3},
		{"negative half rounds away from zero", -5, 0.5, -3},
		{"negative amount", -2500, 0.92, -2300},
		{"zero", 0, 1.09, 0},
		{"tiny rate", 100, 0.001, 0},
		{"large rate", 1, 1090, 1090},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConvertCents(tt.cents, tt.rate); got != tt.want {
				t.Fatalf("ConvertCents(%d, %v) = %d, want %d", tt.cents, tt.rate, got, tt.want)
			}
		})
	}
}
