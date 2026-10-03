package extractor

import (
	"strings"
	"testing"
)

func TestParseUnitValueJSON(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []UnitValueAnswer
		wantErr bool
	}{
		{
			name: "plain items object",
			raw:  `{"items":[{"name":"Water 500ml","unit":"ml","unit_value":500},{"name":"Cola 1.5l","unit":"l","unit_value":1.5}]}`,
			want: []UnitValueAnswer{
				{Name: "Water 500ml", Unit: "ml", UnitValue: 500},
				{Name: "Cola 1.5l", Unit: "l", UnitValue: 1.5},
			},
		},
		{
			name: "object wrapped in prose and fences",
			raw:  "Here are the sizes:\n```json\n{\"items\":[{\"name\":\"Milk\",\"unit\":\"g\",\"unit_value\":1000}]}\n```",
			want: []UnitValueAnswer{{Name: "Milk", Unit: "g", UnitValue: 1000}},
		},
		{
			name: "units normalized and case-folded",
			raw:  `{"items":[{"name":"Bread","unit":" ML ","unit_value":250}]}`,
			want: []UnitValueAnswer{{Name: "Bread", Unit: "ml", UnitValue: 250}},
		},
		{
			name: "unusable entries dropped, never guessed",
			raw: `{"items":[
				{"name":"","unit":"g","unit_value":100},
				{"name":"Unknown Thing","unit":"","unit_value":0},
				{"name":"Weird Can","unit":"bars","unit_value":4},
				{"name":"Negative Size","unit":"g","unit_value":-5},
				{"name":"Water 500ml","unit":"ml","unit_value":500}]}`,
			want: []UnitValueAnswer{{Name: "Water 500ml", Unit: "ml", UnitValue: 500}},
		},
		{
			name:    "no parseable object",
			raw:     "I cannot help with that.",
			wantErr: true,
		},
		{
			// a decodable but payload-less object is the models' warm-up fallback:
			// no error, no answers
			name: "object without items",
			raw:  `{"answers":[]}`,
			want: []UnitValueAnswer{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseUnitValueJSON(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d answers, want %d: %+v", len(got), len(tt.want), got)
			}
			for i, a := range tt.want {
				if got[i] != a {
					t.Errorf("answer %d: got %+v, want %+v", i, got[i], a)
				}
			}
		})
	}
}

func TestFormatUnitValuePrompt(t *testing.T) {
	got := FormatUnitValuePrompt("Resolve sizes.", []string{"Water 500ml", "Cheese \"Alpine\""})
	for _, want := range []string{
		"Resolve sizes.",
		`["Water 500ml","Cheese \"Alpine\""]`,
		"unit_value",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q:\n%s", want, got)
		}
	}
}
