import { useState } from 'react';
import { useAsync } from '../../hooks/useAsync';
import { promptsApi } from '../../api/prompts';
import type { AIPromptView } from '../../types/domain';
import {
  Button,
  Spinner,
  ErrorMessage,
  EmptyState,
  Dialog,
  ItemPanel,
  ItemPanels,
} from '../../components/ui';

// Which process resolves each known prompt key. Custom keys are not looked
// up by anything yet — they are templates or reserved for future features.
const processUsage: Record<string, string> = {
  bill_extraction:
    'Used by: AI bill scanning — every receipt photo/PDF you scan (Bills → Scan) is read with this prompt.',
  offer_search:
    'Used by: purchase-cart offer search — the current market prices of your cart products (Cart) are researched with this prompt.',
};

const unknownUsage =
  'Not used by any process — kept as a template, or reserved for a future feature.';

interface PromptDraft {
  name: string;
  description: string;
  content: string;
}

function draftOf(p: AIPromptView): PromptDraft {
  return { name: p.name, description: p.description, content: p.content };
}

export function PromptsPage() {
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
        [p.id]: err instanceof Error ? err.message : 'Failed to save prompt.',
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
        [p.id]: err instanceof Error ? err.message : 'Failed to reset prompt.',
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
          err instanceof Error ? err.message : 'Failed to delete prompt.',
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
      setAddError(err instanceof Error ? err.message : 'Failed to create prompt.');
    } finally {
      setAddSaving(false);
    }
  }

  if (prompts.loading) return <Spinner />;
  if (prompts.error) return <ErrorMessage message={prompts.error.message} />;

  const list = prompts.data ?? [];

  return (
    <div className="page">
      <h2 className="page-title">AI prompts</h2>

      <p className="hint-text">
        These instruction texts are sent to your AI connectors. Editing them
        changes how bills are read and how offers are searched — a broken
        prompt can break extraction, so keep the JSON schema intact. A prompt
        with empty content uses its built-in default; deleting one of the two
        seeded prompts is safe for the same reason.{' '}
        <code>{'{{categories}}'}</code> is replaced at run time with the live
        product-category list from your categories.
      </p>

      {list.length === 0 ? (
        <EmptyState message="No prompts yet. The built-in defaults are in use." />
      ) : (
        <ItemPanels>
          {list.map((p) => {
            const draft = drafts[p.id] ?? draftOf(p);
            const usage = processUsage[p.key] ?? unknownUsage;
            const error = rowErrors[p.id];
            return (
              <ItemPanel
                key={p.id}
                icon="📝"
                title={draft.name || p.name}
                subtitle={
                  <>
                    <span className="default-badge"> {p.key}</span>
                    {p.uses_default && (
                      <span className="hint-inline"> using built-in default</span>
                    )}
                  </>
                }
                value={p.uses_default ? 'default' : undefined}
              >
                <p className="hint-text">{usage}</p>
                {p.description && <p className="hint-text">{p.description}</p>}

                <div className="form-grid">
                  <label>
                    Name
                    <input
                      value={draft.name}
                      onChange={(e) => setDraft(p.id, { name: e.target.value })}
                    />
                  </label>
                  <label>
                    Description
                    <input
                      value={draft.description}
                      placeholder="What this prompt is for"
                      onChange={(e) => setDraft(p.id, { description: e.target.value })}
                    />
                  </label>
                </div>

                <label>
                  Prompt content
                  <textarea
                    rows={16}
                    value={draft.content}
                    placeholder={p.default_content || 'Prompt text'}
                    onChange={(e) => setDraft(p.id, { content: e.target.value })}
                  />
                </label>
                {draft.content.includes('{{categories}}') && (
                  <p className="hint-text">
                    {'{{categories}}'} will be replaced at run time with the live
                    product-category list (names with commas are quoted).
                  </p>
                )}

                {error && <ErrorMessage message={error} />}

                <div className="camera-row">
                  <Button onClick={() => handleSave(p)} disabled={busyId !== null}>
                    {busyId === p.id ? 'Saving…' : 'Save'}
                  </Button>
                  <Button
                    variant="secondary"
                    onClick={() => handleReset(p)}
                    disabled={busyId !== null || !p.default_content || p.uses_default}
                    title={p.default_content ? 'Restore the built-in default text' : 'This prompt has no built-in default'}
                  >
                    Reset to default
                  </Button>
                  <Button variant="danger" onClick={() => setConfirmDelete(p)} disabled={busyId !== null}>
                    Delete
                  </Button>
                </div>
              </ItemPanel>
            );
          })}
        </ItemPanels>
      )}

      <div className="camera-row">
        <Button variant="secondary" onClick={() => setAdding(true)}>
          + Add prompt
        </Button>
      </div>

      {adding && (
        <Dialog title="Add prompt" wide>
          <div className="form-grid">
            <label>
              Key{' '}
              <span className="hint-inline">
                lowercase letters, digits or underscores, starting with a
                letter; immutable after create
              </span>
              <input
                value={addKey}
                placeholder="my_custom_prompt"
                onChange={(e) => setAddKey(e.target.value)}
              />
            </label>
            <label>
              Name
              <input
                value={addName}
                placeholder="Display name"
                onChange={(e) => setAddName(e.target.value)}
              />
            </label>
            <label>
              Description
              <input
                value={addDescription}
                placeholder="What this prompt is for"
                onChange={(e) => setAddDescription(e.target.value)}
              />
            </label>
          </div>
          <label>
            Prompt content
            <textarea
              rows={8}
              value={addContent}
              placeholder="Prompt text (empty = the process will use nothing unless a default exists)"
              onChange={(e) => setAddContent(e.target.value)}
            />
          </label>
          {addError && <ErrorMessage message={addError} />}
          <div className="camera-row">
            <Button variant="secondary" onClick={() => setAdding(false)} disabled={addSaving}>
              Cancel
            </Button>
            <Button onClick={handleCreate} disabled={addSaving || addKey.trim() === '' || addName.trim() === ''}>
              {addSaving ? 'Creating…' : 'Create prompt'}
            </Button>
          </div>
        </Dialog>
      )}

      {confirmDelete && (
        <Dialog title={`Delete “${confirmDelete.name}”?`}>
          <p className="hint-text">
            {processUsage[confirmDelete.key]
              ? 'This prompt is used by a process. After deletion the process falls back to the built-in default prompt.'
              : 'This prompt is not used by any process.'}
          </p>
          <div className="camera-row">
            <Button variant="secondary" onClick={() => setConfirmDelete(null)} disabled={busyId !== null}>
              Cancel
            </Button>
            <Button variant="danger" onClick={handleDelete} disabled={busyId !== null}>
              {busyId === confirmDelete.id ? 'Deleting…' : 'Delete'}
            </Button>
          </div>
        </Dialog>
      )}
    </div>
  );
}