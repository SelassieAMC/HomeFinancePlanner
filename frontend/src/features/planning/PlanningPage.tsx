import { useSearchParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
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

const TAB_KEYS: readonly PlanningTab[] = ['budgets', 'goals', 'bags', 'schedules'];

export function PlanningPage() {
  const { t } = useTranslation('planning');
  const [params, setParams] = useSearchParams();
  const raw = params.get('tab');
  const tab: PlanningTab = raw === 'goals' || raw === 'bags' || raw === 'schedules' ? raw : 'budgets';

  function select(next: PlanningTab) {
    // The default tab stays the clean URL (?tab= only when not budgets).
    setParams(next === 'budgets' ? {} : { tab: next });
    window.scrollTo({ top: 0 });
  }

  const tabs: TabDef<PlanningTab>[] = TAB_KEYS.map((key) => ({
    key,
    label: t(`tabs.${key}`),
  }));

  return (
    <div className="page">
      <h2 className="page-title">{t('title')}</h2>
      <Tabs tabs={tabs} active={tab} onChange={select} ariaLabel={t('tabs_aria')} />
      {tab === 'budgets' && <BudgetsPage embedded />}
      {tab === 'goals' && <GoalsPage embedded />}
      {tab === 'bags' && <BagsPage embedded />}
      {tab === 'schedules' && <SchedulesPage embedded />}
    </div>
  );
}