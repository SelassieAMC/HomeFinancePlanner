package domain

import "testing"

func TestIsDepositArtifact(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"deposit charge", "PFAND 0,25", true},
		{"deposit bon", "PFAND-BON 0,15", true},
		{"lowercase", "Pfand", true},
		{"deposit combined with amount", "PFAND-MEHRWEG 0,15", true},
		{"english", "Bottle deposit 0.25", true},
		{"english bare", "DEPOSIT", true},
		{"return line", "LEERGUT 8", true},
		{"return words", "Leergutrückgabe", true}, // leergut is unambiguous anywhere in the name
		{"multiway bare", "MEHRWEG 0,15", true},
		{"oneway compound", "Einwegpfand 0,25", true}, // pfand matches anywhere in the name
		{"gratis marker", "GRATIS", true},
		{"gratis with article", "Gratis Ketchup", true},
		{"gratis inside compound", "Gratissauce", false}, // one word, no boundary
		{"real product einwegkamera", "Einwegkamera", false},
		{"real product", "Ritter Sport 100g", false},
		{"real product containing deposit word", "Deposit box 10 pcs", true}, // matches deposit
		{"empty", "", false},
	}
	for _, c := range cases {
		if got := IsDepositArtifact(c.input); got != c.want {
			t.Errorf("IsDepositArtifact(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}
