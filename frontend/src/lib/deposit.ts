/**
 * Deposit bottle/crate RETURN lines ("Leergut", "Pfandrückgabe", "Pfandbon",
 * "Bottle return") are money back: their amounts may go negative and reduce
 * the bill total. Deposit CHARGES ("Pfand", "Mehrweg", "Einweg") are money
 * spent — they stay positive, so they intentionally do not match.
 *
 * Client-side mirror of the backend's domain.IsDepositReturn predicate —
 * keep the patterns in sync when either side changes.
 */
const DEPOSIT_RETURN_PATTERN =
  /(leergut|rückgabe|pfand-?bon|\b(bottle return|deposit return|deposit refund)\b)/i;

export function isDepositReturn(name: string): boolean {
  return DEPOSIT_RETURN_PATTERN.test(name);
}

/**
 * Deposit/coupon ARTIFACT lines ("Pfand", "Rabatt", "Gutschein", "Coupon",
 * "Gratis") are money-marking lines, not products: they never join the
 * product catalogue, and their amounts may go negative (money back) however
 * their category was filed.
 *
 * Client-side mirror of the backend's domain.IsDepositArtifact predicate —
 * keep the patterns in sync when either side changes.
 */
const DEPOSIT_ARTIFACT_PATTERN =
  /(pfand|leergut|pfandbon|rabatt|gutschein|\b(mehrweg|einweg|deposit|bottle deposit|gratis|coupon|voucher|discount)\b)/i;

export function isDepositArtifact(name: string): boolean {
  return DEPOSIT_ARTIFACT_PATTERN.test(name);
}