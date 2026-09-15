import { MEASURE_OPTIONS, normalizeUnit } from '../../lib/units';

interface UnitSelectProps {
  /** Raw unit value; mapped onto the fixed vocabulary via normalizeUnit. */
  value: string | undefined;
  onChange: (unit: string) => void;
  ariaLabel: string;
  /** Label of the empty choice (defaults to "—"). */
  emptyLabel?: string;
}

/**
 * Shared measure dropdown. Offers the fixed measure vocabulary used on bills;
 * a stored value outside the vocabulary is kept as an extra option so it is
 * never silently lost.
 */
export function UnitSelect({ value, onChange, ariaLabel, emptyLabel = '—' }: UnitSelectProps) {
  const current = normalizeUnit(value);
  const extra =
    current && !MEASURE_OPTIONS.some((m) => m.value === current)
      ? [{ value: current, label: current }]
      : [];
  return (
    <select value={current} aria-label={ariaLabel} onChange={(e) => onChange(e.target.value)}>
      <option value="">{emptyLabel}</option>
      {[...MEASURE_OPTIONS, ...extra].map((m) => (
        <option key={m.value} value={m.value}>
          {m.label}
        </option>
      ))}
    </select>
  );
}