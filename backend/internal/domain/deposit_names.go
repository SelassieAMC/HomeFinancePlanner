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
	`(?i)(pfand|leergut|pfandbon|rabatt|gutschein|\b(mehrweg|einweg|deposit|bottle deposit|gratis|coupon|voucher|discount)\b)`)

// IsDepositArtifact reports whether a receipt-line (or product) name denotes a
// deposit/refund/free artifact instead of a purchasable product. Deposit
// charges and returns stay in the bill (they carry real, often negative,
// money under "Deposit & Returns") but must never become catalogue products
// or enter the naming memory. The same rule drives bill confirm, manual
// transactions, and the one-off cleanups.
//
// Coupon/marketing lines ("Lidl Plus Rabatt", "Coupon", "Gutschein") match
// too: they keep their (often negative, often unit-price-less) money in the
// bill but are not products — one once find-or-created itself into the
// catalogue and polluted purchase history / per-category spending with it.
func IsDepositArtifact(name string) bool {
	return depositArtifactPattern.MatchString(name)
}

// depositReturnPattern is narrower than the artifact pattern: it matches only
// the money-BACK deposit lines ("LEERGUT", "Pfandrückgabe", "Pfandbon",
// "Bottle return", "Deposit refund") — the subset whose amounts must be
// negative (or zero) because they reduce the bill total. Deposit charges
// ("PFAND 0,25", "MEHRWEG", "EINWEG") match IsDepositArtifact but not this —
// they are money spent and stay positive. "Rückgabe" alone is safe to match,
// a returned article always refunds money as well.
var depositReturnPattern = regexp.MustCompile(
	`(?i)(leergut|rückgabe|pfand-?bon|\b(bottle return|deposit return|deposit refund)\b)`)

// IsDepositReturn reports whether a line name denotes the money-back side of
// deposits — a bottle/crate return rather than a deposit charge. Returns
// reduce the bill total: negative prices are allowed for them and the AI read
// must not leave their amounts positive. "Leergut" used to be the only match;
// "Pfandrückgabe" (the label most German receipts print for the return lines)
// was missed, so the guard chains never classified it as money back.
func IsDepositReturn(name string) bool {
	return depositReturnPattern.MatchString(name)
}
