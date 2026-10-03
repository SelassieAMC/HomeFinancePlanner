package extractor

import (
	"context"
	"encoding/json"
	"strings"

	"home-finance-planner/backend/internal/domain"
)

// UnitValueAnswer is one name → size answer of the unit-value prompt: the
// printed size magnitude the model could derive for a product ("unit_value"
// 500 with unit "ml" for a 500ml bottle). UnitValue ≤ 0 (or a non-whitelisted
// unit) means "no size derivable" and the caller leaves the row unknown.
type UnitValueAnswer struct {
	Name      string  `json:"name"`
	Unit      string  `json:"unit"`
	UnitValue float64 `json:"unit_value"`
}

// unitValueUnits is the canonical measure whitelist the rest of the system
// computes prices against (same list as the analytics price query).
var unitValueUnits = map[string]bool{"kg": true, "g": true, "l": true, "ml": true, "pcs": true}

// UnitValues runs the prompt-only unit-value call and parses the
// {"items":[…]} answer (service-facing wrapper). Answers whose name is
// empty, magnitude is not positive, or unit is outside the canonical
// whitelist are dropped — the caller keeps them unknown.
func (e *Extractor) UnitValues(ctx context.Context, provider domain.AIProvider, prompt string) ([]UnitValueAnswer, error) {
	raw, err := e.CompleteText(ctx, provider, prompt)
	if err != nil {
		return nil, err
	}
	return ParseUnitValueJSON(raw)
}

// ParseUnitValueJSON extracts the {"items":[…]} object from a model response
// and returns the usable name → size answers. Entries are matched back to the
// prompt inputs case-insensitively by the caller; unusable or missing entries
// leave those names unknown (never a guessed value).
func ParseUnitValueJSON(raw string) ([]UnitValueAnswer, error) {
	var wire struct {
		Items []UnitValueAnswer `json:"items"`
	}
	if err := decodeBestObject(raw, &wire, func(w *struct {
		Items []UnitValueAnswer `json:"items"`
	}) bool {
		return len(w.Items) > 0
	}); err != nil {
		return nil, err
	}
	out := []UnitValueAnswer{}
	for _, it := range wire.Items {
		name := strings.TrimSpace(it.Name)
		unit := strings.ToLower(strings.TrimSpace(it.Unit))
		if name == "" || it.UnitValue <= 0 || !unitValueUnits[unit] {
			continue
		}
		out = append(out, UnitValueAnswer{Name: name, Unit: unit, UnitValue: it.UnitValue})
	}
	return out, nil
}

// formatUnitValueHints is the size/measure context appended to the prompt of
// every unit-value call: what the caller can process, and what the answer
// must look like. Kept here so the prompt text and the parser agree.
const formatUnitValueHints = `- "unit_value" is the printed size magnitude as a NUMBER ONLY — 500 for a
  500ml bottle, 1.5 for a 1.500g flour, 0.5 for a "0,5l" pack — the number
  without its unit text.
- "unit" is the measure the magnitude pairs with, exactly one of: kg, g, l,
  ml, pcs. Convert printed units to the canonical one ("0,5 l" → unit "l",
  unit_value 0.5; "500 ml" → unit "ml", unit_value 500).
- If the product name tells you nothing about a size, return unit_value 0
  and unit "" — never guess.`

// FormatUnitValuePrompt assembles the JSON-array payload line for one
// unit-value batch: the prompt text plus the names to resolve (echoed
// verbatim so answers are matched back).
func FormatUnitValuePrompt(prompt string, names []string) string {
	// Names are plain strings — marshal cannot fail. The error path keeps the
	// bare prompt only as belt-and-braces.
	encoded, err := json.Marshal(names)
	if err != nil {
		return prompt
	}
	return prompt + "\nProduct names:\n" + string(encoded) + "\n" + formatUnitValueHints
}
