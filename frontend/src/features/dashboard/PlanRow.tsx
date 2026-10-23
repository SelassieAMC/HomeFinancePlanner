import type { ReactNode } from 'react';

/**
 * One compact planning row — the shared line-per-entity layout the dashboard's
 * planning bodies (budgets, goals, bags) render instead of a stacked card per
 * entity. Reuses the budget-card visual language (dot, bar, tabular amounts)
 * at a fraction of the height: name line, then a hairline bar with the
 * trailing status. Details that only sometimes apply (pace warning, in-month
 * spend) ride along as a hint line so quiet rows stay one bar tall.
 */
export interface PlanRowProps {
  /** Dot + fill variant, matching the .budget-dot / .progress-fill variants. */
  tone: 'normal' | 'ok' | 'over' | 'closed';
  /** Display name (category, bag, goal). */
  name: string;
  /** Optional emoji/short prefix rendered before the name (category icon). */
  icon?: ReactNode;
  /** Small muted context on the name line (account name, cadence tag). */
  context?: string;
  /** The current amount, emphasized. */
  primary: string;
  /** Optional comparison amount after the slash (cap / target). */
  target?: string;
  /** Bar fill percent (already clamped). null = no bar (e.g. bags without target). */
  pct: number | null;
  /** Trailing status beside the bar; falls onto the name line when there is no bar. */
  status?: string;
  /** Status colour. */
  statusTone?: 'normal' | 'ok' | 'over';
  /** Rare extra hint line under the bar (pace warning, in-month spend). */
  hint?: string;
}

const dotClass: Record<PlanRowProps['tone'], string> = {
  normal: 'budget-dot',
  ok: 'budget-dot ok',
  over: 'budget-dot over',
  closed: 'budget-dot closed',
};

const fillClass: Record<PlanRowProps['tone'], string> = {
  normal: 'progress-fill',
  ok: 'progress-fill done',
  over: 'progress-fill over',
  closed: 'progress-fill',
};

export function PlanRow({
  tone,
  name,
  icon,
  context,
  primary,
  target,
  pct,
  status,
  statusTone = 'normal',
  hint,
}: PlanRowProps) {
  return (
    <div className="plan-row">
      <div className="plan-line">
        <span className={dotClass[tone]} aria-hidden="true" />
        {icon && (
          <span className="plan-icon" aria-hidden="true">
            {icon}
          </span>
        )}
        <span className="plan-name">{name}</span>
        {context && <span className="stat-label plan-context">{context}</span>}
        {pct === null && status && (
          <span className={`plan-status plan-status-inline ${statusTone === 'over' ? 'over' : statusTone === 'ok' ? 'ok' : ''}`}>
            {status}
          </span>
        )}
        <span className="budget-amounts">
          <strong>{primary}</strong>
          {target && <span>{' / '}{target}</span>}
        </span>
      </div>
      {pct !== null && (
        <div className="plan-bar-row">
          <div className="progress-track">
            <div
              className={fillClass[tone]}
              style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
            />
          </div>
          {status && (
            <span
              className={`plan-status ${statusTone === 'over' ? 'over' : statusTone === 'ok' ? 'ok' : ''}`}
            >
              {status}
            </span>
          )}
        </div>
      )}
      {hint && <div className="hint-inline">{hint}</div>}
    </div>
  );
}

/**
 * Account group header for the bags rows: the account's name, its own balance
 * and the unbagged remainder once per account — not repeated per bag — with
 * the oversubscription flag where it belongs.
 */
export function PlanAccountHead({
  name,
  balance,
  unbagged,
  warn,
}: {
  name: string;
  balance: string;
  unbagged?: string;
  warn?: string;
}) {
  return (
    <div className="plan-account-head">
      <span className="plan-account-name">{name}</span>
      <span className="stat-label plan-account-figures">
        {balance}
        {unbagged && <span className="plan-unbagged">{unbagged}</span>}
      </span>
      {warn && <span className="plan-flag">{warn}</span>}
    </div>
  );
}