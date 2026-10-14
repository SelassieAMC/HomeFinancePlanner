import { useTranslation } from 'react-i18next';
import type { PeriodType } from '../../lib/date';
import { Button } from '../../components/ui';

const PERIOD_TYPES: { value: PeriodType }[] = [
  { value: 'day' },
  { value: 'week' },
  { value: 'month' },
  { value: 'year' },
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
  const { t } = useTranslation('dashboard');
  return (
    <div className="period-nav" role="group" aria-label={t('period.aria.period')}>
      <div className="segmented">
        {PERIOD_TYPES.map((p) => (
          <button
            key={p.value}
            type="button"
            className={type === p.value ? 'active' : ''}
            aria-pressed={type === p.value}
            onClick={() => onTypeChange(p.value)}
          >
            {t(`period.${p.value}`)}
          </button>
        ))}
      </div>
      <Button variant="ghost" onClick={onPrev} aria-label={t('period.aria.prev')}>
        ‹
      </Button>
      <span className="period-nav-label" aria-live="polite">
        {label}
      </span>
      <Button variant="ghost" onClick={onNext} disabled={nextDisabled} aria-label={t('period.aria.next')}>
        ›
      </Button>
    </div>
  );
}