// Period helpers for the dashboard. All dates are "YYYY-MM-DD" strings in
// LOCAL time; the backend treats them as opaque (already validated) values.
// Weeks are Monday–Sunday (ISO 8601).

export type PeriodType = 'day' | 'week' | 'month' | 'year';

export interface Period {
  type: PeriodType;
  /** Inclusive first day, YYYY-MM-DD. */
  start: string;
  /** Inclusive last day, YYYY-MM-DD. */
  end: string;
}

function toISO(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(
    d.getDate(),
  ).padStart(2, '0')}`;
}

function fromISO(iso: string): Date {
  const parts = iso.split('-').map(Number);
  return new Date(parts[0] ?? 2000, (parts[1] ?? 1) - 1, parts[2] ?? 1);
}

/** First and last day of a "YYYY-MM" month, local time. */
export function monthBounds(month: string): { from: string; to: string } {
  const parts = month.split('-').map(Number);
  const y = parts[0] ?? 2000;
  const m = (parts[1] ?? 1) - 1;
  return {
    from: toISO(new Date(y, m, 1)),
    to: toISO(new Date(y, m + 1, 0)),
  };
}

/** The month (YYYY-MM) containing the given date. */
export function monthOf(iso: string): string {
  return iso.slice(0, 7);
}

/** Shift a YYYY-MM month by whole months. */
export function shiftMonth(month: string, delta: number): string {
  const parts = month.split('-').map(Number);
  const d = new Date(parts[0] ?? 2000, (parts[1] ?? 1) - 1 + delta, 1);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`;
}

/** Inclusive range covering the last `monthsBack` calendar months plus the
 *  month containing `anchorISO` itself (e.g. anchor Sep 14, 6 → Apr 1–Sep 30).
 *  Anchored on the period end so past periods stay self-consistent. */
export function monthWindow(anchorISO: string, monthsBack: number): { from: string; to: string } {
  const end = monthOf(anchorISO);
  return { from: monthBounds(shiftMonth(end, -monthsBack)).from, to: monthBounds(end).to };
}

/** Today as YYYY-MM-DD in local time. */
export function todayISO(): string {
  return toISO(new Date());
}

/** The period of the given type containing the anchor date. */
export function periodOf(type: PeriodType, anchorISO: string): Period {
  const anchor = fromISO(anchorISO);
  switch (type) {
    case 'day':
      return { type, start: anchorISO, end: anchorISO };
    case 'week': {
      // Step back to Monday: (getDay()+6)%7 is 0 on Monday … 6 on Sunday.
      const monday = new Date(anchor);
      monday.setDate(anchor.getDate() - ((anchor.getDay() + 6) % 7));
      const sunday = new Date(monday);
      sunday.setDate(monday.getDate() + 6);
      return { type, start: toISO(monday), end: toISO(sunday) };
    }
    case 'month': {
      const first = new Date(anchor.getFullYear(), anchor.getMonth(), 1);
      const last = new Date(anchor.getFullYear(), anchor.getMonth() + 1, 0);
      return { type, start: toISO(first), end: toISO(last) };
    }
    case 'year': {
      const first = new Date(anchor.getFullYear(), 0, 1);
      const last = new Date(anchor.getFullYear(), 11, 31);
      return { type, start: toISO(first), end: toISO(last) };
    }
  }
}

/** Move a period forward (delta > 0) or backward (delta < 0) by whole units
 *  of its type, based on its own start date. */
export function shiftPeriod(period: Period, delta: number): Period {
  const base = fromISO(period.start);
  const shifted = new Date(base);
  switch (period.type) {
    case 'day':
      shifted.setDate(base.getDate() + delta);
      break;
    case 'week':
      shifted.setDate(base.getDate() + 7 * delta);
      break;
    case 'month':
      shifted.setMonth(base.getMonth() + delta);
      break;
    case 'year':
      shifted.setFullYear(base.getFullYear() + delta);
      break;
  }
  return periodOf(period.type, toISO(shifted));
}

/** True when the period contains today — the "cannot go into the future"
 *  boundary for the next-arrow. */
export function isCurrentPeriod(period: Period): boolean {
  const today = todayISO();
  return period.start <= today && today <= period.end;
}

/** Human label for the period header, e.g. "Sep 11, 2026",
 *  "Sep 8 – 14, 2026", "September 2026", "2026". */
export function periodLabel(period: Period): string {
  const start = fromISO(period.start);
  const end = fromISO(period.end);
  const sameYear = start.getFullYear() === end.getFullYear();
  switch (period.type) {
    case 'day':
      return start.toLocaleDateString('en-US', {
        year: sameYear ? 'numeric' : undefined,
        month: 'short',
        day: 'numeric',
      });
    case 'week': {
      const from = start.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
      const to = end.toLocaleDateString('en-US', {
        year: sameYear ? 'numeric' : undefined,
        month: 'short',
        day: 'numeric',
      });
      return `${from} – ${to}`;
    }
    case 'month':
      return start.toLocaleDateString('en-US', {
        year: 'numeric',
        month: 'long',
      });
    case 'year':
      return String(start.getFullYear());
  }
}

