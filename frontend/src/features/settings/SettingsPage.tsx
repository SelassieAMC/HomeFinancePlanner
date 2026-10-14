import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { useAsync } from '../../hooks/useAsync';
import { usePolling } from '../../hooks/usePolling';
import {
  settingsApi,
  type AIProviderInput,
  type ConnectionTestResult,
} from '../../api/settings';
import { productsApi } from '../../api/products';
import type { AIProvider, AIProviderType, NormalizationJob } from '../../types/domain';
import { COMMON_CURRENCIES } from '../../lib/currencies';
import { Button, Card, Tabs, Spinner, ErrorMessage, EmptyState, ItemPanel, ItemPanels } from '../../components/ui';
import { useAuth } from '../auth/AuthContext';
import { ProfileTab } from './ProfileTab';
import { UsersTab } from './UsersTab';

const providerTypes: { value: AIProviderType; labelKey: string }[] = [
  { value: 'ollama', labelKey: 'connectors.typeOllama' },
  {
    value: 'ollama_web_search',
    labelKey: 'connectors.typeOllamaWebSearch',
  },
  { value: 'openai', labelKey: 'connectors.typeOpenai' },
  { value: 'gemini', labelKey: 'connectors.typeGemini' },
  { value: 'anthropic', labelKey: 'connectors.typeAnthropic' },
  {
    value: 'openai_compatible',
    labelKey: 'connectors.typeOpenaiCompatible',
  },
];

// Translated labels resolved through the record (labels are call-time keys,
// never translated at module load).
const providerTypeLabels = Object.fromEntries(
  providerTypes.map((p) => [p.value, p.labelKey]),
) as Record<AIProviderType, string>;

const modelPlaceholder: Record<AIProviderType, string> = {
  ollama: 'llama3.2-vision',
  ollama_web_search: 'qwen3',
  openai: 'gpt-4o-mini',
  gemini: 'gemini-2.5-flash',
  anthropic: 'claude-haiku-4-5',
  openai_compatible: 'meta-llama/llama-3.2-11b-vision-instruct',
};

const baseUrlPlaceholder: Record<AIProviderType, string> = {
  ollama: 'http://localhost:11434',
  ollama_web_search: 'http://localhost:11434 (https://ollama.com = cloud)',
  openai: 'https://api.openai.com/v1',
  gemini: 'https://generativelanguage.googleapis.com/v1beta',
  anthropic: 'https://api.anthropic.com/v1',
  openai_compatible: 'https://openrouter.ai/api/v1',
};

type DraftKey = number; // local-only editing key

interface DraftProvider {
  key: DraftKey;
  id?: string; // present only once saved server-side
  type: AIProviderType;
  base_url: string;
  api_key: string; // what the user has typed this session (never echoed back)
  stored_key_mask: string; // masked key from the server, e.g. ••••abcd
  model: string;
  default_for_bills: boolean;
  default_for_search: boolean;
}

function toDraft(p: AIProvider, key: DraftKey): DraftProvider {
  return {
    key,
    id: p.id,
    type: p.type,
    base_url: p.base_url ?? '',
    api_key: '',
    stored_key_mask: p.api_key ?? '',
    model: p.model,
    default_for_bills: p.default_for_bills ?? false,
    default_for_search: p.default_for_search ?? false,
  };
}

// The page's sections are independent settings domains; tabs keep each one
// readable (own profile, currency/general, AI connector drafts, the
// normalization job, and — admins only — the household user management).
type SettingsTab = 'profile' | 'general' | 'connectors' | 'normalization' | 'users';

export function SettingsPage() {
  const { t } = useTranslation('settings');
  const { user } = useAuth();
  const [tab, setTab] = useState<SettingsTab>('profile');
  const saved = useAsync(() => settingsApi.listAIProviders(), []);

  // --- Display currency ------------------------------------------------------
  const baseCurrency = useAsync(() => settingsApi.getBaseCurrency(), []);
  const [currency, setCurrency] = useState('');
  const [currencySaving, setCurrencySaving] = useState(false);
  const [currencyError, setCurrencyError] = useState<string | null>(null);
  const [currencySaved, setCurrencySaved] = useState(false);
  useEffect(() => {
    if (baseCurrency.data && currency === '') {
      setCurrency(baseCurrency.data.currency);
    }
  }, [baseCurrency.data, currency]);

  async function handleSaveCurrency() {
    setCurrencySaving(true);
    setCurrencyError(null);
    setCurrencySaved(false);
    try {
      const result = await settingsApi.saveBaseCurrency(currency);
      setCurrency(result.currency);
      setCurrencySaved(true);
    } catch (err) {
      setCurrencyError(err instanceof Error ? err.message : t('currency.saveError'));
    } finally {
      setCurrencySaving(false);
    }
  }

  const [drafts, setDrafts] = useState<DraftProvider[] | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState<string | null>(null);
  const [testResults, setTestResults] = useState<Record<string, ConnectionTestResult>>({});

  // Drafts mirror the saved list until the first edit, then edits live here.
  const current: DraftProvider[] =
    drafts ??
    (saved.data ?? []).map((p, i) => toDraft(p, i + 1));

  // Editing keys must be unique against whatever is on screen right now —
  // derived at mutation time, never from a stale counter (a new connector's
  // edits used to collide with the first configured one).
  function nextDraftKey(): DraftKey {
    return current.reduce((m, d) => Math.max(m, d.key), 0) + 1;
  }

  function update(key: DraftKey, patch: Partial<DraftProvider>) {
    setDrafts(current.map((d) => (d.key === key ? { ...d, ...patch } : d)));
  }

  function addProvider() {
    setDrafts([
      ...current,
      {
        key: nextDraftKey(),
        type: 'ollama',
        base_url: '',
        api_key: '',
        stored_key_mask: '',
        model: '',
        default_for_bills: false,
        default_for_search: false,
      },
    ]);
  }

  function removeProvider(key: DraftKey) {
    setDrafts(current.filter((d) => d.key !== key));
  }

  // Purpose defaults are exclusive per purpose: marking one connector sets
  // that purpose's flag on it alone and clears it from the others (the other
  // purpose's flags stay untouched). The save response re-derives the drafts
  // with the authoritative flags.
  function setDefaultFor(key: DraftKey, purpose: 'bills' | 'search') {
    const field = purpose === 'bills' ? 'default_for_bills' : 'default_for_search';
    setDrafts(
      current.map((d) => ({
        ...d,
        [field]: d.key === key ? true : false,
      })),
    );
  }

  async function handleSave() {
    setSaving(true);
    setSaveError(null);
    try {
      const payload: AIProviderInput[] = current.map((d) => ({
        id: d.id,
        type: d.type,
        base_url: d.base_url.trim() || undefined,
        // Empty api_key means "keep the stored one" server-side.
        api_key: d.api_key.trim() || undefined,
        model: d.model.trim(),
        default_for_bills: d.default_for_bills,
        default_for_search: d.default_for_search,
      }));
      const savedList = await settingsApi.saveAIProviders(payload);
      setTestResults({});
      setDrafts(savedList.map((p, i) => toDraft(p, i + 1)));
      saved.reload();
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : t('connectors.saveError'));
    } finally {
      setSaving(false);
    }
  }

  async function handleTest(d: DraftProvider) {
    if (!d.id) return;
    setTesting(d.id);
    try {
      const result = await settingsApi.testAIProvider(d.id);
      setTestResults((prev) => ({ ...prev, [d.id as string]: result }));
    } catch (err) {
      setTestResults((prev) => ({
        ...prev,
        [d.id as string]: {
          ok: false,
          error: err instanceof Error ? err.message : t('connectors.testError'),
        },
      }));
    } finally {
      setTesting(null);
    }
  }

  // --- Product normalization backfill job -----------------------------------
  // The job row is fetched once on load; while a run is in progress the
  // status is polled until it leaves "running".
  const initialJob = useAsync(() => productsApi.normalizationStatus(), []);
  const [jobRunning, setJobRunning] = useState(false);
  const [jobStarting, setJobStarting] = useState(false);
  const [jobError, setJobError] = useState<string | null>(null);
  const jobPoll = usePolling(() => productsApi.normalizationStatus(), {
    intervalMs: 2000,
    enabled: jobRunning,
  });
  const job: NormalizationJob | null = jobPoll.data ?? initialJob.data ?? null;

  // A job found already running (page opened mid-run) resumes polling.
  useEffect(() => {
    if (initialJob.data && !jobRunning) {
      setJobRunning(initialJob.data.status === 'running');
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialJob.data]);

  // The poll flips itself off once the job leaves "running".
  useEffect(() => {
    if (jobPoll.data && jobPoll.data.status !== 'running') setJobRunning(false);
  }, [jobPoll.data]);

  async function handleRunNormalization() {
    setJobStarting(true);
    setJobError(null);
    try {
      await productsApi.runNormalization();
      setJobRunning(true);
    } catch (err) {
      setJobError(err instanceof Error ? err.message : t('normalization.startError'));
    } finally {
      setJobStarting(false);
    }
  }

  if (saved.loading) return <Spinner />;
  if (saved.error) return <ErrorMessage message={saved.error.message} />;

  return (
    <div className="page">
      <h2 className="page-title">{t('page.title')}</h2>

      <Tabs
        ariaLabel={t('page.sectionsAria')}
        tabs={[
          { key: 'profile', label: t('tabs.profile') },
          { key: 'general', label: t('page.tabGeneral') },
          { key: 'connectors', label: t('page.tabConnectors') },
          { key: 'normalization', label: t('page.tabNormalization') },
          ...(user?.is_admin
            ? [{ key: 'users' as const, label: t('tabs.users') }]
            : []),
        ]}
        active={tab}
        onChange={setTab}
      />

      {tab === 'profile' && <ProfileTab key="profile" />}

      {tab === 'users' && <UsersTab key="users" />}

      {tab === 'general' && (
        <Card title={t('currency.title')}>
        <form
          className="form-row"
          onSubmit={(e) => {
            e.preventDefault();
            handleSaveCurrency();
          }}
        >
          <select
            value={currency}
            onChange={(e) => {
              setCurrency(e.target.value);
              setCurrencySaved(false);
            }}
            aria-label={t('currency.selectAria')}
            disabled={baseCurrency.loading || currencySaving}
          >
            {COMMON_CURRENCIES.map((c) => (
              <option key={c.code} value={c.code}>
                {c.code} — {c.label}
              </option>
            ))}
          </select>
          <Button type="submit" disabled={currencySaving || baseCurrency.loading}>
            {currencySaving ? t('currency.saving') : t('currency.save')}
          </Button>
        </form>
        <p className="hint-text">{t('currency.hint')}</p>
        {currencyError && <ErrorMessage message={currencyError} />}
        {currencySaved && <div className="hint-banner">{t('currency.saved')}</div>}
        </Card>
      )}

      {tab === 'connectors' && (
        <>

      <p className="hint-text">
        {t('connectors.hint')}{' '}
        <Link to="/settings/prompts">{t('connectors.managePrompts')}</Link>{' '}
        {t('connectors.promptLinkSuffix')}
      </p>

      {current.length === 0 ? (
        <EmptyState message={t('connectors.empty')} />
      ) : (
        <ItemPanels>
          {current.map((d) => (
            <ItemPanel
              key={d.id ?? d.key}
              title={
                providerTypeLabels[d.type]
                  ? t(providerTypeLabels[d.type])
                  : d.type
              }
              subtitle={
                <>
                  {d.model || t('connectors.modelNotSet')}
                  {d.default_for_bills && (
                    <span className="default-badge"> {t('connectors.badgeBills')}</span>
                  )}
                  {d.default_for_search && (
                    <span className="default-badge"> {t('connectors.badgeSearch')}</span>
                  )}
                </>
              }
              value={
                testResults[d.id as string]?.ok
                  ? '✓'
                  : d.id
                    ? undefined
                    : t('connectors.newBadge')
              }
            >
              <div className="form-grid">
              <label>
                {t('connectors.typeLabel')}
                <select
                  value={d.type}
                  onChange={(e) => update(d.key, { type: e.target.value as AIProviderType })}
                >
                  {providerTypes.map((pt) => (
                    <option key={pt.value} value={pt.value}>
                      {t(pt.labelKey)}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                {t('connectors.baseUrlLabel')}{' '}
                <span className="hint-inline">{t('connectors.baseUrlBlank')}</span>
                <input
                  value={d.base_url}
                  placeholder={baseUrlPlaceholder[d.type]}
                  onChange={(e) => update(d.key, { base_url: e.target.value })}
                />
              </label>
              <label>
                {t('connectors.apikeyLabel')}{' '}
                {d.type === 'ollama_web_search' && (
                  <span className="hint-inline">
                    {t('connectors.webSearchKeyHint')}
                  </span>
                )}
                {d.stored_key_mask && (
                  <span className="hint-inline">
                    {t('connectors.storedKey', { mask: d.stored_key_mask })}
                  </span>
                )}
                <input
                  type="password"
                  value={d.api_key}
                  placeholder={
                    d.stored_key_mask
                      ? t('connectors.keyPlaceholderKeep')
                      : t('connectors.keyPlaceholder')
                  }
                  autoComplete="off"
                  onChange={(e) => update(d.key, { api_key: e.target.value })}
                />
              </label>
              <label>
                {t('connectors.modelLabel')}
                <input
                  value={d.model}
                  placeholder={modelPlaceholder[d.type]}
                  onChange={(e) => update(d.key, { model: e.target.value })}
                />
              </label>
              <label className="default-radio">
                {t('connectors.defaultBills')}
                <input
                  type="checkbox"
                  checked={d.default_for_bills}
                  onChange={() => setDefaultFor(d.key, 'bills')}
                />
              </label>
              <label className="default-radio">
                {t('connectors.defaultSearch')}
                <input
                  type="checkbox"
                  checked={d.default_for_search}
                  onChange={() => setDefaultFor(d.key, 'search')}
                />
              </label>
            </div>

            <div className="camera-row">
              {d.id ? (
                <Button
                  variant="secondary"
                  onClick={() => handleTest(d)}
                  disabled={testing !== null}
                >
                  {testing === d.id ? t('connectors.testing') : t('connectors.testConnection')}
                </Button>
              ) : (
                <span className="hint-inline">{t('connectors.saveFirstTest')}</span>
              )}
              <Button variant="danger" onClick={() => removeProvider(d.key)}>
                {t('connectors.remove')}
              </Button>
            </div>

            {(() => {
              if (!d.id) return null;
              const result = testResults[d.id];
              if (!result) return null;
              return result.ok ? (
                <div className="hint-banner">{t('connectors.testOk')}</div>
              ) : (
                <ErrorMessage message={result.error ?? t('connectors.testFailed')} />
              );
            })()}
            </ItemPanel>
          ))}
        </ItemPanels>
      )}

      {saveError && <ErrorMessage message={saveError} />}

      <div className="camera-row">
        <Button variant="secondary" onClick={addProvider} disabled={saving}>
          {t('connectors.add')}
        </Button>
        <Button onClick={handleSave} disabled={saving}>
          {saving ? t('connectors.saving') : t('connectors.saveAll')}
        </Button>
      </div>
        </>
      )}

      {tab === 'normalization' && (
        <>

      <Card title={t('normalization.title')}>
        <p className="hint-text">{t('normalization.hint')}</p>
        {job?.status === 'running' ? (
          <p className="hint-text">
            {t('normalization.running', {
              processed: job.processed_names,
              total: job.total_names,
              mapped: job.mapped_names,
            })}
          </p>
        ) : job?.status === 'done' ? (
          <p className="hint-text">
            {t('normalization.done', { mapped: job.mapped_names, total: job.total_names })}
          </p>
        ) : job?.status === 'failed' ? (
          <ErrorMessage message={job.error || t('normalization.lastFailed')} />
        ) : (
          <p className="hint-text">{t('normalization.notRun')}</p>
        )}
        {jobError && <ErrorMessage message={jobError} />}
        <div className="camera-row">
          <Button
            onClick={handleRunNormalization}
            disabled={jobStarting || jobRunning || job?.status === 'running'}
          >
            {jobRunning || job?.status === 'running'
              ? t('normalization.analyzing')
              : job?.status === 'done'
                ? t('normalization.runAgain')
                : t('normalization.runNow')}
          </Button>
        </div>
      </Card>
        </>
      )}
    </div>
  );
}