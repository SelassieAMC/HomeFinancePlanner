package fx

import "math"

// ConvertCents applies a conversion rate to an integer-cent amount.
// Rounding: half away from zero, applied ONCE after the multiplication —
// the same convention as the extractor's decimal→cents conversion and the
// bill line-total rounding.
func ConvertCents(cents int64, rate float64) int64 {
	return int64(math.Round(float64(cents) * rate))
}
