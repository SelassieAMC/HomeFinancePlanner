import { useEffect, useState } from 'react';
import { useAsync, type AsyncState } from '../../hooks/useAsync';
import { goalsApi } from '../../api/goals';
import { bagsApi } from '../../api/bags';
import {
  Card,
  EmptyState,
  ErrorMessage,
  Spinner,
  Tabs,
  type TabDef,
} from '../../components/ui';
import type { Summary } from '../../types/domain';
import { budgetRowsFor, BudgetsBody } from './BudgetProgressCard';
import { GoalsBody } from './GoalsCard';
import { BagsBody, bagsHaveContent } from './BagsCard';

// Budgets, goals and bags are sibling planning layers — one dashboard card
// with tabs instead of three stacked cards. The card is only visible when at
// least one layer has something to show (each is hidden-when-empty on its
// own); a layer without content shows guidance inside its tab so the user
// learns where to create it.

type PlanningSection = 'budgets' | 'goals' | 'bags';

const TABS: TabDef<PlanningSection>[] = [
  { key: 'budgets', label: 'Budgets' },
  { key: 'goals', label: 'Goals' },
  { key: 'bags', label: 'Bags' },
];

export function PlanningCard({ summary }: { summary: AsyncState<Summary> }) {
  const goals = useAsync(() => goalsApi.list(), []);
  const bags = useAsync(() => bagsApi.overview(), []);

  const [active, setActive] = useState<PlanningSection>('budgets');

  // Section presence: `false` only once that section's fetch finished empty
  // (nothing yet = still loading, never "absent"); budgets come from the
  // month summary the stat tiles already share.
  const budgetsReady = !summary.loading && !!summary.data;
  const goalsReady = !goals.loading;
  const bagsReady = !bags.loading;
  const hasBudgets = budgetsReady && budgetRowsFor(summary.data as Summary).length > 0;
  const hasGoals = goalsReady && (goals.data ?? []).length > 0;
  const hasBags = bagsReady && bagsHaveContent(bags.data ?? null);

  // Never leave the active tab on a section that is *known* empty while
  // another section has content — the first loaded section wins. Mid-load,
  // the loading spinner keeps the tab stable.
  useEffect(() => {
    const present: Record<PlanningSection, boolean> = {
      budgets: hasBudgets,
      goals: hasGoals,
      bags: hasBags,
    };
    const known: Record<PlanningSection, boolean> = {
      budgets: budgetsReady,
      goals: goalsReady,
      bags: bagsReady,
    };
    if (present[active] !== false) return;
    const first = (['budgets', 'goals', 'bags'] as PlanningSection[]).find(
      (s) => present[s] && known[s],
    );
    if (first) setActive(first);
  }, [active, hasBudgets, hasGoals, hasBags, budgetsReady, goalsReady, bagsReady]);

  const everythingEmpty =
    budgetsReady && goalsReady && bagsReady && !hasBudgets && !hasGoals && !hasBags;
  // While loads are in flight, show the card skeleton; once everything is
  // known and no layer qualifies, the card disappears entirely (the previous
  // hide-when-empty behavior).
  if (everythingEmpty) return null;

  const monthTag = summary.data
    ? new Date(`${summary.data.from.slice(0, 7)}-01T00:00:00`).toLocaleDateString('en-US', {
        month: 'short',
        year: 'numeric',
      })
    : '';

  return (
    <Card title="Planning" className="budgets-card planning-card">
      <Tabs
        tabs={TABS}
        active={active}
        onChange={setActive}
        ariaLabel="Planning: budgets, goals and bags"
      />
      <div className="planning-pane">
        {active === 'budgets' &&
          (summary.loading ? (
            <Spinner label="Loading budgets…" />
          ) : summary.error || !summary.data ? (
            <ErrorMessage message={summary.error?.message ?? 'Summary unavailable.'} />
          ) : hasBudgets ? (
            <BudgetsBody summary={summary} />
          ) : (
            <EmptyState message={`No budget activity in ${monthTag} — add caps on the Planning page.`} />
          ))}
        {active === 'goals' &&
          (goals.loading ? (
            <Spinner label="Loading goals…" />
          ) : goals.error ? (
            <ErrorMessage message={goals.error.message} />
          ) : hasGoals ? (
            <GoalsBody goals={goals} />
          ) : (
            <EmptyState message="No goals yet — add a savings target on the Planning page." />
          ))}
        {active === 'bags' &&
          (bags.loading ? (
            <Spinner label="Loading bags…" />
          ) : bags.error ? (
            <ErrorMessage message={bags.error.message} />
          ) : hasBags ? (
            <BagsBody overview={bags} />
          ) : (
            <EmptyState message="No bags yet — add a reserve on the Planning page." />
          ))}
      </div>
    </Card>
  );
}