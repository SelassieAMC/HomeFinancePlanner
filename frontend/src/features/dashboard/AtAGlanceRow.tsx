import { useTranslation } from 'react-i18next';
import type { AsyncState } from '../../hooks/useAsync';
import type { Glance } from '../../types/domain';
import i18n from '../../i18n';
import { intlLocale } from '../../i18n/core';
import { formatCents, formatSignedCents } from '../../lib/money';
import { Card } from '../../components/ui';

function monthLabel(month: string): string {
  return new Date(`${month}-01T00:00:00`).toLocaleDateString(intlLocale(i18n.language), {
    month: 'long',
  });
}

/**
 * Step-1 "at a glance" row: current financial standing, independent of the
 * period the dashboard below is navigated to. The fetch happens in
 * DashboardPage so its conversion warnings join the page-wide banner.
 */
export function AtAGlanceRow({ glance }: { glance: AsyncState<Glance> }) {
  const { t } = useTranslation('dashboard');
  const d = glance.data;
  const cur = d?.currency ?? '';

  if (glance.error) {
    return <div className="bill-warning">{t('glance.error', { message: glance.error.message })}</div>;
  }
  if (glance.loading || !d) {
    return (
      <div className="stat-grid glance">
        {[t('glance.loading.netWorth'), t('glance.loading.cashFlow'), t('glance.loading.burnRate'), t('glance.loading.receipts')].map((label) => (
          <Card className="stat-tile" key={label}>
            <span className="stat-label">{label}</span>
            <span className="stat-value">…</span>
          </Card>
        ))}
      </div>
    );
  }

  const netTone = d.net_cents >= 0 ? 'stat-positive' : 'stat-negative';
  return (
    <div className="stat-grid glance">
      <Card className="stat-tile">
        <span className="stat-label">{t('glance.netWorth')}</span>
        <span className="stat-value">{formatCents(d.net_worth_cents, cur)}</span>
        <span className="stat-label">{t('glance.today')}</span>
      </Card>
      <Card className="stat-tile">
        <span className="stat-label">{t('glance.cashFlowTitle', { month: monthLabel(d.month) })}</span>
        <span className={`stat-value ${netTone}`}>{formatSignedCents(d.net_cents, cur)}</span>
        <span className="stat-label">
          {t('glance.inOut', { income: formatCents(d.income_cents, cur), expense: formatCents(d.expense_cents, cur) })}
        </span>
      </Card>
      <Card className="stat-tile">
        <span className="stat-label">{t('glance.burnTitle')}</span>
        <span className="stat-value">{formatCents(d.burn_rate_cents, cur)}</span>
        <span className="stat-label">{t('glance.burnHint')}</span>
      </Card>
      <Card className="stat-tile">
        <span className="stat-label">{t('glance.receiptsTitle')}</span>
        <span className="stat-value">{d.pending_receipts}</span>
        <span className="stat-label">
          {d.pending_receipts === 0
            ? t('glance.nothingWaiting')
            : t('glance.awaitingReview', { amount: formatCents(d.pending_receipt_cents, cur) })}
        </span>
      </Card>
    </div>
  );
}
