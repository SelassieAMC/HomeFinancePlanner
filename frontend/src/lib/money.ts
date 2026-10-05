// Money helpers. All amounts cross the API as integer cents; formatting to
// currency strings happens only here, for display. The currency is a
// REQUIRED argument — callers must state which currency an amount is in:
// per-entity pages use the entity's native currency, aggregated views the
// base currency carried on the API response.

export function formatCents(cents: number, currency: string): string {
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency,
  }).format(cents / 100);
}

export function formatSignedCents(cents: number, currency: string): string {
  const sign = cents > 0 ? '+' : '';
  return sign + formatCents(cents, currency);
}

/**
 * parseDecimalInput parses an amount typed into a decimal field, accepting
 * BOTH decimal separators: mobile numeric keypads only ever show the locale's
 * separator key (a comma on German phones), and a pasted value may carry a
 * thousands group separator, a currency code/symbol or a unit suffix. Rules:
 * - the numeric core (optional minus + digits, dots, commas, apostrophes) is
 *   extracted first — "CHF 1,5" → "1,5", "1,5 kg" → "1,5";
 * - when both "." and "," appear, the LAST one is the decimal separator and
 *   the other is a group separator ("1.234,56" and "1,234.56" → 1234.56);
 * - a single "," is a decimal separator ("12,50" → 12.5) while several turn
 *   into group separators ("1,234,567" → 1234567);
 * - "." always stays a decimal separator (the previous behavior).
 * Returns NaN when nothing numeric remains ("", "abc").
 */
export function parseDecimalInput(input: string): number {
  const m = input.match(/-?\s*(?:[0-9][0-9.,']*|[.,][0-9]+)/);
  if (!m) return NaN;
  let s = m[0].replace(/'/g, '');
  const lastDot = s.lastIndexOf('.');
  const lastComma = s.lastIndexOf(',');
  if (lastDot >= 0 && lastComma >= 0) {
    // Last separator wins as the decimal point; drop the other kind.
    const [decimalSep, groupSep] = lastDot > lastComma ? ['.', ','] : [',', '.'];
    s = s.split(groupSep).join('');
    if (decimalSep === ',') s = s.split(',').join('.');
  } else if (lastComma >= 0 && s.split(',').length > 2) {
    // Several commas without a dot: group separators. (A single comma — the
    // decimal case — falls through to the replace below.)
    s = s.split(',').join('');
  } else {
    s = s.split(',').join('.');
  }
  return Number(s);
}

export function dollarsToCents(input: string): number {
  const parsed = parseDecimalInput(input);
  if (Number.isNaN(parsed)) return NaN;
  return Math.round(parsed * 100);
}

/** Current month as YYYY-MM (local time). */
export function currentMonth(): string {
  const now = new Date();
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`;
}