import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAsync } from '../../hooks/useAsync';
import { promptsApi } from '../../api/prompts';
import type { AIPromptView } from '../../types/domain';
import {
  Button,
  Spinner,
  ErrorMessage,
  EmptyState,
  Dialog,
  HelpNote,
  ItemPanel,
  ItemPanels,
} from '../../components/ui';

// Which process resolves each known prompt key. Custom keys are not looked
// up by anything yet — they are templates or reserved for future features.
// The values are translation keys resolved with the `prompts` namespace.
const processUsage: Record<string, string> = {
  bill_extraction: 'usage.billExtraction',
  offer_search: 'usage.offerSearch',
};

const unknownUsageKey = 'usage.unknown';

interface PromptDraft {
  name: string;
  description: string;
  content: string;
}

function draftOf(p: AIPromptView): PromptDraft {
  return { name: p.name, description: p.description, content: p.content };
}

export function PromptsPage() {
  const { t } = useTranslation('prompts');
  const prompts = useAsync(() => promptsApi.list(), []);

  // Edits live here, keyed by prompt id; untouched prompts render their
  // stored values. Cleared after every successful save.
  const [drafts, setDrafts] = useState<Record<number, PromptDraft>>({});
  const [busyId, setBusyId] = useState<number | null>(null);
  const [rowErrors, setRowErrors] = useState<Record<number, string>>({});
  const [confirmDelete, setConfirmDelete] = useState<AIPromptView | null>(null);

  // --- add-prompt dialog ----------------------------------------------------
  const [adding, setAdding] = useState(false);
  const [addKey, setAddKey] = useState('');
  const [addName, setAddName] = useState('');
  const [addDescription, setAddDescription] = useState('');
  const [addContent, setAddContent] = useState('');
  const [addSaving, setAddSaving] = useState(false);
  const [addError, setAddError] = useState<string | null>(null);

  function setDraft(id: number, patch: Partial<PromptDraft>) {
    const base = prompts.data?.find((p) => p.id === id);
    if (!base) return;
    setDrafts((prev) => ({ ...prev, [id]: { ...(drafts[id] ?? draftOf(base)), ...patch } }));
  }

  async function handleSave(p: AIPromptView) {
    const draft = drafts[p.id] ?? draftOf(p);
    setBusyId(p.id);
    setRowErrors((prev) => ({ ...prev, [p.id]: '' }));
    try {
      await promptsApi.update(p.id, {
        name: draft.name,
        description: draft.description,
        content: draft.content,
      });
      setDrafts((prev) => {
        const next = { ...prev };
        delete next[p.id];
        return next;
      });
      await prompts.reload();
    } catch (err) {
      setRowErrors((prev) => ({
        ...prev,
        [p.id]: err instanceof Error ? err.message : t('errors.save'),
      }));
    } finally {
      setBusyId(null);
    }
  }

  async function handleReset(p: AIPromptView) {
    setBusyId(p.id);
    setRowErrors((prev) => ({ ...prev, [p.id]: '' }));
    try {
      await promptsApi.update(p.id, {
        name: (drafts[p.id] ?? draftOf(p)).name,
        description: (drafts[p.id] ?? draftOf(p)).description,
        content: p.default_content,
      });
      setDrafts((prev) => {
        const next = { ...prev };
        delete next[p.id];
        return next;
      });
      await prompts.reload();
    } catch (err) {
      setRowErrors((prev) => ({
        ...prev,
        [p.id]: err instanceof Error ? err.message : t('errors.reset'),
      }));
    } finally {
      setBusyId(null);
    }
  }

  async function handleDelete() {
    if (!confirmDelete) return;
    setBusyId(confirmDelete.id);
    try {
      await promptsApi.remove(confirmDelete.id);
      setConfirmDelete(null);
      await prompts.reload();
    } catch (err) {
      setRowErrors((prev) => ({
        ...prev,
        [confirmDelete.id]:
          err instanceof Error ? err.message : t('errors.delete'),
      }));
      setConfirmDelete(null);
    } finally {
      setBusyId(null);
    }
  }

  async function handleCreate() {
    setAddSaving(true);
    setAddError(null);
    try {
      await promptsApi.create({
        key: addKey.trim(),
        name: addName,
        description: addDescription,
        content: addContent,
      });
      setAdding(false);
      setAddKey('');
      setAddName('');
      setAddDescription('');
      setAddContent('');
      await prompts.reload();
    } catch (err) {
      setAddError(err instanceof Error ? err.message : t('errors.create'));
    } finally {
      setAddSaving(false);
    }
  }

  if (prompts.loading) return <Spinner />;
  if (prompts.error) return <ErrorMessage message={prompts.error.message} />;

  const list = prompts.data ?? [];

  return (
    <div className="page">
      <h2 className="page-title">{t('title')}</h2>

      <HelpNote
        note={
          <>
            {t('hints.overviewBefore')}
            <code>{'{{categories}}'}</code>
            {t('hints.overviewAfter')}
          </>
        }
      />

      {list.length === 0 ? (
        <EmptyState message={t('empty')} />
      ) : (
        <ItemPanels>
          {list.map((p) => {
            const draft = drafts[p.id] ?? draftOf(p);
            const usageKey = processUsage[p.key] ?? unknownUsageKey;
            const error = rowErrors[p.id];
            return (
              <ItemPanel
                key={p.id}
                title={draft.name || p.name}
                subtitle={
                  <>
                    <span className="default-badge"> {p.key}</span>
                    {p.uses_default && (
                      <span className="hint-inline">{t('panel.usingDefault')}</span>
                    )}
                  </>
                }
                value={p.uses_default ? t('panel.defaultBadge') : undefined}
              >
                <p className="hint-text">{t(usageKey)}</p>
                {p.description && <p className="hint-text">{p.description}</p>}

                <div className="form-grid">
                  <label>
                    {t('fields.name')}
                    <input
                      value={draft.name}
                      onChange={(e) => setDraft(p.id, { name: e.target.value })}
                    />
                  </label>
                  <label>
                    {t('fields.description')}
                    <input
                      value={draft.description}
                      placeholder={t('fields.descriptionPlaceholder')}
                      onChange={(e) => setDraft(p.id, { description: e.target.value })}
                    />
                  </label>
                </div>

                <label>
                  {t('fields.content')}
                  <textarea
                    rows={16}
                    value={draft.content}
                    placeholder={p.default_content || t('fields.contentPlaceholder')}
                    onChange={(e) => setDraft(p.id, { content: e.target.value })}
                  />
                </label>
                {draft.content.includes('{{categories}}') && (
                  <p className="hint-text">
                    {t('hints.categoriesNote', { token: '{{categories}}' })}
                  </p>
                )}

                {error && <ErrorMessage message={error} />}

                <div className="camera-row">
                  <Button onClick={() => handleSave(p)} disabled={busyId !== null}>
                    {busyId === p.id ? t('actions.saving') : t('actions.save')}
                  </Button>
                  <Button
                    variant="secondary"
                    onClick={() => handleReset(p)}
                    disabled={busyId !== null || !p.default_content || p.uses_default}
                    title={p.default_content ? t('actions.resetTitle') : t('actions.resetTitleNone')}
                  >
                    {t('actions.reset')}
                  </Button>
                  <Button variant="danger" onClick={() => setConfirmDelete(p)} disabled={busyId !== null}>
                    {t('actions.delete')}
                  </Button>
                </div>
              </ItemPanel>
            );
          })}
        </ItemPanels>
      )}

      <div className="camera-row">
        <Button variant="secondary" onClick={() => setAdding(true)}>
          {t('actions.add')}
        </Button>
      </div>

      {adding && (
        <Dialog title={t('add.title')} wide onClose={() => !addSaving && setAdding(false)}>
          <div className="form-grid">
            <label>
              {t('add.key')}{' '}
              <span className="hint-inline">
                {t('add.keyHint')}
              </span>
              <input
                value={addKey}
                placeholder={t('add.keyPlaceholder')}
                onChange={(e) => setAddKey(e.target.value)}
              />
            </label>
            <label>
              {t('fields.name')}
              <input
                value={addName}
                placeholder={t('add.namePlaceholder')}
                onChange={(e) => setAddName(e.target.value)}
              />
            </label>
            <label>
              {t('fields.description')}
              <input
                value={addDescription}
                placeholder={t('fields.descriptionPlaceholder')}
                onChange={(e) => setAddDescription(e.target.value)}
              />
            </label>
          </div>
          <label>
            {t('fields.content')}
            <textarea
              rows={8}
              value={addContent}
              placeholder={t('add.contentPlaceholder')}
              onChange={(e) => setAddContent(e.target.value)}
            />
          </label>
          {addError && <ErrorMessage message={addError} />}
          <div className="camera-row">
            <Button variant="secondary" onClick={() => setAdding(false)} disabled={addSaving}>
              {t('actions.cancel')}
            </Button>
            <Button onClick={handleCreate} disabled={addSaving || addKey.trim() === '' || addName.trim() === ''}>
              {addSaving ? t('add.creating') : t('add.create')}
            </Button>
          </div>
        </Dialog>
      )}

      {confirmDelete && (
        <Dialog title={t('deleteDialog.title', { name: confirmDelete.name })} onClose={() => setConfirmDelete(null)}>
          <p className="hint-text">
            {processUsage[confirmDelete.key]
              ? t('deleteDialog.used')
              : t('deleteDialog.unused')}
          </p>
          <div className="camera-row">
            <Button variant="secondary" onClick={() => setConfirmDelete(null)} disabled={busyId !== null}>
              {t('actions.cancel')}
            </Button>
            <Button variant="danger" onClick={handleDelete} disabled={busyId !== null}>
              {busyId === confirmDelete.id ? t('actions.deleting') : t('actions.delete')}
            </Button>
          </div>
        </Dialog>
      )}
    </div>
  );
}