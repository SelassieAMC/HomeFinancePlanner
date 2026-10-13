import { useSearchParams } from 'react-router-dom';
import { Tabs, type TabDef } from '../../components/ui';
import { BudgetsPage } from '../budgets/BudgetsPage';
import { GoalsPage } from '../goals/GoalsPage';
import { BagsPage } from '../bags/BagsPage';
import { SchedulesPage } from '../schedules/SchedulesPage';

// One view for the four planning layers the terminology splits apart:
// budget = spending cap, goal = savings target on a real account, bag =
// reserve inside one account, schedule = planned money movements. The sidebar
// carries this page as a single "Planning" entry; the previous /budgets
// /goals /bags routes redirect here.
export type PlanningTab = 'budgets' | 'goals' | 'bags' | 'schedules';

const TABS: TabDef<PlanningTab>[] = [
  { key: 'budgets', label: 'Budgets' },
  { key: 'goals', label: 'Goals' },
  { key: 'bags', label: 'Bags' },
  { key: 'schedules', label: 'Scheduled' },
];

export function PlanningPage() {
  const [params, setParams] = useSearchParams();
  const raw = params.get('tab');
  const tab: PlanningTab = raw === 'goals' || raw === 'bags' || raw === 'schedules' ? raw : 'budgets';

  function select(next: PlanningTab) {
    // The default tab stays the clean URL (?tab= only when not budgets).
    setParams(next === 'budgets' ? {} : { tab: next });
    window.scrollTo({ top: 0 });
  }

  return (
    <div className="page">
      <h2 className="page-title">Planning</h2>
      <Tabs tabs={TABS} active={tab} onChange={select} ariaLabel="Planning sections" />
      {tab === 'budgets' && <BudgetsPage embedded />}
      {tab === 'goals' && <GoalsPage embedded />}
      {tab === 'bags' && <BagsPage embedded />}
      {tab === 'schedules' && <SchedulesPage embedded />}
    </div>
  );
}