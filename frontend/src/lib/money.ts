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

export function dollarsToCents(input: string): number {
  const trimmed = input.trim().replace(/[$,]/g, '');
  if (trimmed === '' || Number.isNaN(Number(trimmed))) return NaN;
  return Math.round(Number(trimmed) * 100);
}

/** Current month as YYYY-MM (local time). */
export function currentMonth(): string {
  const now = new Date();
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`;
}