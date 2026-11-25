import { useTranslation } from 'react-i18next';
import { monthOf, shiftMonth, todayISO } from '../../lib/date';
import { IconButton, IconChevronLeft, IconChevronRight } from './IconButton';

/**
 * Native month input flanked by prev/next step buttons — the quick month
 * navigation the dashboard's period controls offer, but for a plain YYYY-MM
 * filter field. The native input stays (jump to any month); the arrows shift
 * one calendar month without opening the picker.
 */
export function MonthPicker({
  value,
  onChange,
  ariaLabel,
}: {
  value: string;
  onChange: (month: string) => void;
  ariaLabel?: string;
}) {
  const { t } = useTranslation('common');
  // A cleared native month input returns ''; stepping from nothing should
  // still behave (anchored on the current month) rather than produce NaN-NaN.
  const base = /^\d{4}-\d{2}$/.test(value) ? value : monthOf(todayISO());
  return (
    <span className="month-picker">
      <IconButton
        icon={<IconChevronLeft />}
        label={t('month.prev')}
        onClick={() => onChange(shiftMonth(base, -1))}
      />
      <input
        type="month"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        aria-label={ariaLabel}
      />
      <IconButton
        icon={<IconChevronRight />}
        label={t('month.next')}
        onClick={() => onChange(shiftMonth(base, 1))}
      />
    </span>
  );
}
