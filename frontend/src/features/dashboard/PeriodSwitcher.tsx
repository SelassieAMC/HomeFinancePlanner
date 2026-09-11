import type { PeriodType } from '../../lib/date';
import { Button } from '../../components/ui';

const PERIOD_TYPES: { value: PeriodType; label: string }[] = [
  { value: 'day', label: 'Day' },
  { value: 'week', label: 'Week' },
  { value: 'month', label: 'Month' },
  { value: 'year', label: 'Year' },
];

interface PeriodSwitcherProps {
  type: PeriodType;
  label: string;
  nextDisabled: boolean;
  onTypeChange: (type: PeriodType) => void;
  onPrev: () => void;
  onNext: () => void;
}

/** Dashboard period controls: Day/Week/Month/Year segmented control plus
 *  prev/next navigation. Next is disabled on the period containing today —
 *  the dashboard never looks into the future. */
export function PeriodSwitcher({
  type,
  label,
  nextDisabled,
  onTypeChange,
  onPrev,
  onNext,
}: PeriodSwitcherProps) {
  return (
    <div className="period-nav" role="group" aria-label="Dashboard period">
      <div className="segmented">
        {PERIOD_TYPES.map((t) => (
          <button
            key={t.value}
            type="button"
            className={type === t.value ? 'active' : ''}
            aria-pressed={type === t.value}
            onClick={() => onTypeChange(t.value)}
          >
            {t.label}
          </button>
        ))}
      </div>
      <Button variant="ghost" onClick={onPrev} aria-label="Previous period">
        ‹
      </Button>
      <span className="period-nav-label" aria-live="polite">
        {label}
      </span>
      <Button variant="ghost" onClick={onNext} disabled={nextDisabled} aria-label="Next period">
        ›
      </Button>
    </div>
  );
}