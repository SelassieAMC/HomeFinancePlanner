import { useState } from 'react';
import { useAsync } from '../../hooks/useAsync';
import {
  settingsApi,
  type AIProviderInput,
  type ConnectionTestResult,
} from '../../api/settings';
import type { AIProvider, AIProviderType } from '../../types/domain';
import { Button, Spinner, ErrorMessage, EmptyState, ItemPanel, ItemPanels } from '../../components/ui';

const providerTypes: { value: AIProviderType; label: string }[] = [
  { value: 'ollama', label: 'Ollama (local, no key)' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'gemini', label: 'Google Gemini' },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai_compatible', label: 'OpenAI-compatible (OpenRouter, Groq, …)' },
];

const modelPlaceholder: Record<AIProviderType, string> = {
  ollama: 'llama3.2-vision',
  openai: 'gpt-4o-mini',
  gemini: 'gemini-2.5-flash',
  anthropic: 'claude-haiku-4-5',
  openai_compatible: 'meta-llama/llama-3.2-11b-vision-instruct',
};

const baseUrlPlaceholder: Record<AIProviderType, string> = {
  ollama: 'http://localhost:11434',
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
  };
}

export function SettingsPage() {
  const saved = useAsync(() => settingsApi.listAIProviders(), []);
  const [drafts, setDrafts] = useState<DraftProvider[] | null>(null);
  const [nextKey, setNextKey] = useState<DraftKey>(1);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState<string | null>(null);
  const [testResults, setTestResults] = useState<Record<string, ConnectionTestResult>>({});

  // Drafts mirror the saved list until the first edit, then edits live here.
  const current: DraftProvider[] =
    drafts ??
    (saved.data ?? []).map((p, i) => toDraft(p, i + 1));

  function update(key: DraftKey, patch: Partial<DraftProvider>) {
    setDrafts(current.map((d) => (d.key === key ? { ...d, ...patch } : d)));
  }

  function addProvider() {
    setDrafts([
      ...current,
      {
        key: nextKey,
        type: 'ollama',
        base_url: '',
        api_key: '',
        stored_key_mask: '',
        model: '',
      },
    ]);
    setNextKey(nextKey + 1);
  }

  function removeProvider(key: DraftKey) {
    setDrafts(current.filter((d) => d.key !== key));
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
      }));
      const savedList = await settingsApi.saveAIProviders(payload);
      setTestResults({});
      setDrafts(savedList.map((p, i) => toDraft(p, i + 1)));
      setNextKey(savedList.length + 1);
      saved.reload();
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Failed to save settings.');
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
          error: err instanceof Error ? err.message : 'Test failed.',
        },
      }));
    } finally {
      setTesting(null);
    }
  }

  if (saved.loading) return <Spinner />;
  if (saved.error) return <ErrorMessage message={saved.error.message} />;

  return (
    <div className="page">
      <h2 className="page-title">Settings — AI connectors</h2>

      <p className="hint-text">
        These connectors read receipt photos during bill scanning. Keys are
        encrypted server-side and never returned in full — the masked value
        (••••abcd) only shows the last four characters. Leave the key field
        empty to keep the stored key.
      </p>

      {current.length === 0 ? (
        <EmptyState message="No connectors configured yet. Add one to start scanning bills." />
      ) : (
        <ItemPanels>
          {current.map((d) => (
            <ItemPanel
              key={d.key}
              icon="🔌"
              title={providerTypes.find((t) => t.value === d.type)?.label ?? d.type}
              subtitle={d.model || 'model not set'}
              value={testResults[d.id as string]?.ok ? '✓' : d.id ? undefined : 'new'}
            >
              <div className="form-grid">
              <label>
                Type
                <select
                  value={d.type}
                  onChange={(e) => update(d.key, { type: e.target.value as AIProviderType })}
                >
                  {providerTypes.map((t) => (
                    <option key={t.value} value={t.value}>
                      {t.label}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                Base URL <span className="hint-inline">(blank = default)</span>
                <input
                  value={d.base_url}
                  placeholder={baseUrlPlaceholder[d.type]}
                  onChange={(e) => update(d.key, { base_url: e.target.value })}
                />
              </label>
              <label>
                API key{' '}
                {d.stored_key_mask && (
                  <span className="hint-inline">stored: {d.stored_key_mask}</span>
                )}
                <input
                  type="password"
                  value={d.api_key}
                  placeholder={d.stored_key_mask ? 'Leave empty to keep stored key' : 'API key'}
                  autoComplete="off"
                  onChange={(e) => update(d.key, { api_key: e.target.value })}
                />
              </label>
              <label>
                Model
                <input
                  value={d.model}
                  placeholder={modelPlaceholder[d.type]}
                  onChange={(e) => update(d.key, { model: e.target.value })}
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
                  {testing === d.id ? 'Testing…' : 'Test connection'}
                </Button>
              ) : (
                <span className="hint-inline">Save first to test the connection.</span>
              )}
              <Button variant="danger" onClick={() => removeProvider(d.key)}>
                Remove
              </Button>
            </div>

            {(() => {
              if (!d.id) return null;
              const result = testResults[d.id];
              if (!result) return null;
              return result.ok ? (
                <div className="hint-banner">Connection OK ✓</div>
              ) : (
                <ErrorMessage message={result.error ?? 'Connection failed.'} />
              );
            })()}
            </ItemPanel>
          ))}
        </ItemPanels>
      )}

      {saveError && <ErrorMessage message={saveError} />}

      <div className="camera-row">
        <Button variant="secondary" onClick={addProvider} disabled={saving}>
          + Add connector
        </Button>
        <Button onClick={handleSave} disabled={saving}>
          {saving ? 'Saving…' : 'Save all'}
        </Button>
      </div>
    </div>
  );
}