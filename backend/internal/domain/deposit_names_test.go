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
		// Coupon/marketing lines: money-marking artifacts, never products.
		{"lidl plus coupon", "Lidl Plus Rabatt", true},
		{"german voucher", "GUTSCHEIN -2,00", true},
		{"percentage rebate", "10% Rabatt", true},
		{"english coupon", "Coupon 1.00", true},
		{"english voucher", "Voucher -5.00", true},
		{"english discount", "Discount 0.50", true},
		{"rabatt inside compound", "Rabattaktion 2,00", true}, // rabatt is unambiguous anywhere in the name
	}
	for _, c := range cases {
		if got := IsDepositArtifact(c.input); got != c.want {
			t.Errorf("IsDepositArtifact(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

// IsDepositReturn is the money-back subset of the deposit artifacts: deposit
// RETURNS (negative amounts), not the charges — "PFAND 0,25" on the way in is
// money spent and stays positive.
func TestIsDepositReturn(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"bottle return", "LEERGUT 8", true},
		{"crate return", "Leergutrückgabe", true},
		{"german receipt label", "Pfandrückgabe -16 x 0,25", true},
		{"german receipt label glued", "Pfand-Rückgabe 0,15", true},
		{"bare return word", "Rückgabe", true},
		{"return bon", "PFAND-BON 3,50", true},
		{"english", "Bottle return 1.50", true},
		{"english refund", "Deposit refund 12.75", true},
		{"deposit charge is not a return", "PFAND 0,25", false},
		{"multiway charge", "MEHRWEG 0,15", false},
		{"oneway compound", "Einwegpfand 1,50", false},
		{"real product", "Ritter Sport 100g", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		if got := IsDepositReturn(c.input); got != c.want {
			t.Errorf("IsDepositReturn(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}
