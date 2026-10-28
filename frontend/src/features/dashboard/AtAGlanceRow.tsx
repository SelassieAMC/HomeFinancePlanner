import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import type { AsyncState } from '../../hooks/useAsync';
import { useAsync } from '../../hooks/useAsync';
import i18n from '../../i18n';
import { intlLocale } from '../../i18n/core';
import { accountsApi } from '../../api/accounts';
import { crossRate, convertCents, fxApi } from '../../api/fx';
import { formatCents, formatSignedCents } from '../../lib/money';
import type { Account, Glance } from '../../types/domain';
import { Card, ErrorMessage } from '../../components/ui';

function monthLabel(month: string): string {
  return new Date(`${month}-01T00:00:00`).toLocaleDateString(intlLocale(i18n.language), {
    month: 'long',
  });
}

/**
 * Step-1 "at a glance" row: current financial standing, independent of the
 * period the dashboard below is navigated to. The fetch happens in
 * DashboardPage so its conversion warnings join the page-wide banner.
 *
 * The net-worth tile keeps a compact strip by default: the per-account
 * balances it rolls up live behind an in-tile expansion, fetched only on
 * first open, with the accounts page for management.
 */
export function AtAGlanceRow({ glance }: { glance: AsyncState<Glance> }) {
  const { t } = useTranslation('dashboard');
  const d = glance.data;
  const cur = d?.currency ?? '';

  // Accounts expand inside the net-worth tile — lazy, only when first opened.
  // The rate snapshot joins the fetch so every row can be centralized into
  // the base currency: raw foreign-currency balances (a peso account in the
  // millions) don't add up against the total.
  const [accountsOpen, setAccountsOpen] = useState(false);
  const accounts = useAsync(async () => (accountsOpen ? await accountsApi.list() : []), [accountsOpen]);
  const fx = useAsync(async () => (accountsOpen ? await fxApi.rates() : null), [accountsOpen]);

  // Rows read as contributions to the net-worth figure: the converted amount
  // (base currency) is primary, the account's own currency trails muted.
  // A currency the rate table doesn't cover degrades to 1:1 with a flag —
  // the same convention as every other conversion in the app.
  const accountRows = (accounts.data ?? [])
    .map((a: Account) => {
      const same = a.currency === cur;
      const rate = same ? 1 : crossRate(fx.data, a.currency, cur);
      const converted = same ? a.balance_cents : rate != null ? convertCents(a.balance_cents, rate) : a.balance_cents;
      return {
        account: a,
        converted,
        convertedLabel: same ? undefined : formatCents(a.balance_cents, a.currency),
        noRate: !same && rate == null,
      };
    })
    .sort((a, b) => b.converted - a.converted || a.account.name.localeCompare(b.account.name));

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
  // Phase 4 assets/liabilities split: the strip sections by sign. Liabilities
  // are negative balances (credit debt), reported as a positive magnitude.
  const debt = d.liabilities_cents ?? 0;
  return (
    <div className="stat-grid glance">
      <Card className="stat-tile glance-net-tile">
        <button
          type="button"
          className="glance-net-toggle"
          aria-expanded={accountsOpen}
          onClick={() => setAccountsOpen((o) => !o)}
        >
          <span className="stat-label">{t('glance.netWorth')}</span>
          <span className="glance-net-value">
            <span className="stat-value">{formatCents(d.net_worth_cents, cur)}</span>
            <svg
              className={`glance-chev${accountsOpen ? ' open' : ''}`}
              width="10"
              height="7"
              viewBox="0 0 10 7"
              aria-hidden="true"
            >
              <path d="M1 1.5L5 5.5L9 1.5" stroke="currentColor" strokeWidth="1.5" fill="none" />
            </svg>
          </span>
          <span className="stat-label">
            {debt > 0
              ? t('glance.debtIncl', { liabilities: formatCents(debt, cur) })
              : t('glance.today')}
          </span>
        </button>
        {accountsOpen && (
          <div className="glance-accounts">
            {accounts.error && <ErrorMessage message={t('glance.accountsError')} />}
            {accounts.loading && <span className="stat-label">…</span>}
            {!accounts.loading && !accounts.error && accountRows.length === 0 && (
              <span className="stat-label">{t('glance.accountsEmpty')}</span>
            )}
            {!accounts.loading && !accounts.error && accountRows.length > 0 && (
              <>
                {debt > 0 && (
                  <span className="stat-label glance-split-head">{t('glance.assets')}</span>
                )}
                {accountRows
                  .filter((row) => row.converted >= 0)
                  .map((row) => (
                    <div className="glance-account-row" key={row.account.id}>
                      <span className="glance-account-name">{row.account.name}</span>
                      {row.convertedLabel && (
                        <span className="glance-account-native">{row.convertedLabel}</span>
                      )}
                      {row.noRate && (
                        <span className="stat-label glance-account-norate" title={t('glance.noRate')}>
                          ≈
                        </span>
                      )}
                      <span className="glance-account-amount">{formatCents(row.converted, cur)}</span>
                    </div>
                  ))}
                {debt > 0 && (
                  <span className="stat-label glance-split-head">{t('glance.liabilities')}</span>
                )}
                {debt > 0 &&
                  accountRows
                    .filter((row) => row.converted < 0)
                    .map((row) => (
                      <div className="glance-account-row glance-liability-row" key={row.account.id}>
                        <span className="glance-account-name">{row.account.name}</span>
                        {row.convertedLabel && (
                          <span className="glance-account-native">{row.convertedLabel}</span>
                        )}
                        {row.noRate && (
                          <span className="stat-label glance-account-norate" title={t('glance.noRate')}>
                            ≈
                          </span>
                        )}
                        <span className="glance-account-amount">{formatCents(row.converted, cur)}</span>
                      </div>
                    ))}
              </>
            )}
            <Link to="/accounts" className="glance-accounts-link">
              {t('glance.manageAccounts')}
            </Link>
          </div>
        )}
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