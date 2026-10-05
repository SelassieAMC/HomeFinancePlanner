package domain

import (
	"regexp"
)

// depositArtifactPattern matches the non-purchase lines German receipts (and
// their English translations) print around the shopping: bottle/crate deposit
// charges ("PFAND 0,25", "PFAND-BON", "Bottle deposit"), deposit returns
// ("Leergut", "MEHRWEG-PFAND", "EINWEGPFAND"), and free-item markers
// ("GRATIS"). "Pfand" and "Leergut" are unambiguous German deposit words — no
// product name carries them — so they match anywhere (glued compounds like
// "Einwegpfand" too); the ambiguous tokens need word boundaries so
// "Einwegkamera" and "Gratissauce" stay products while "MEHRWEG 0,15" and a
// bare "GRATIS" do not.
var depositArtifactPattern = regexp.MustCompile(
	`(?i)(pfand|leergut|pfandbon|\b(mehrweg|einweg|deposit|bottle deposit|gratis)\b)`)

// IsDepositArtifact reports whether a receipt-line (or product) name denotes a
// deposit/refund/free artifact instead of a purchasable product. Deposit
// charges and returns stay in the bill (they carry real, often negative,
// money under "Deposit & Returns") but must never become catalogue products
// or enter the naming memory. The same rule drives bill confirm, manual
// transactions, and the one-off cleanups.
func IsDepositArtifact(name string) bool {
	return depositArtifactPattern.MatchString(name)
}
