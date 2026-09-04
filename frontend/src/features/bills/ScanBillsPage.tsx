import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { billsApi } from '../../api/bills';
import { settingsApi } from '../../api/settings';
import { accountsApi } from '../../api/accounts';
import { categoriesApi } from '../../api/categories';
import { budgetsApi } from '../../api/budgets';
import type { Bill, BillDraft, BillScan } from '../../types/domain';
import { useAsync } from '../../hooks/useAsync';
import { formatCents } from '../../lib/money';
import { Button, Card, Spinner, ErrorMessage } from '../../components/ui';
import {
  BillDraftEditor,
  buildConfirmInput,
  type BillBusyAction,
} from './BillDraftEditor';

type Phase = 'capture' | 'review' | 'done';

const FILE_ACCEPT = 'image/*,.heic,.heif,.pdf,application/pdf';

export function ScanBillsPage() {
  const providers = useAsync(() => settingsApi.listAIProviders(), []);
  const accounts = useAsync(() => accountsApi.list(), []);
  const categories = useAsync(() => categoriesApi.list(), []);
  const brands = useAsync(() => billsApi.brands(), []);

  const [phase, setPhase] = useState<Phase>('capture');
  const [busy, setBusy] = useState<BillBusyAction>(null);
  const [scan, setScan] = useState<BillScan | null>(null);
  const [draft, setDraft] = useState<BillDraft | null>(null);
  // Bumped whenever a fresh draft arrives, so cell inputs remount with the
  // new values (draft line ids restart at 1 on every extraction).
  const [draftKey, setDraftKey] = useState(0);
  // Budgets for the draft's month — refetched when the bill date changes.
  const budgetMonth = draft?.date ? draft.date.slice(0, 7) : '';
  const budgets = useAsync(
    () => (budgetMonth ? budgetsApi.listByMonth(budgetMonth) : Promise.resolve([])),
    [budgetMonth],
  );
  const [accepted, setAccepted] = useState<Bill | null>(null);
  const [previewUrl, setPreviewUrl] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const cameraInputRef = useRef<HTMLInputElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const firstProvider = providers.data?.[0];

  useEffect(() => {
    // Revoke the object URL when it changes or the page unmounts.
    return () => {
      if (previewUrl) URL.revokeObjectURL(previewUrl);
    };
  }, [previewUrl]);

  async function handleFile(file: File | undefined) {
    if (!file || busy) return;
    setError(null);
    setBusy('extract');

    const url = URL.createObjectURL(file);
    setPreviewUrl(url);

    try {
      const result = await billsApi.scan(file, firstProvider?.id);
      setScan(result);
      setDraft(result.draft);
      setDraftKey((k) => k + 1);
      setPhase('review');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to process the receipt.');
      setPreviewUrl(null);
      URL.revokeObjectURL(url);
    } finally {
      setBusy(null);
    }
  }

  async function handleConfirm(accountId?: number, createCardAccount?: boolean) {
    if (!scan || !draft) return;
    setBusy('confirm');
    setError(null);
    try {
      let targetId = accountId;
      // "Create new card account" — register an account for this card's
      // digits, then record the expense on it.
      if (!targetId && createCardAccount && draft.card_last_digits) {
        const created = await accountsApi.create({
          name: `Card •${draft.card_last_digits}`,
          type: 'credit',
          currency: draft.currency || 'USD',
          balance_cents: 0,
          card_last_digits: draft.card_last_digits,
        });
        targetId = created.id;
        accounts.reload();
      }
      const bill = await billsApi.confirm(scan.scan_token, buildConfirmInput(draft, targetId));
      setAccepted(bill);
      setPhase('done');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to confirm the bill.');
    } finally {
      setBusy(null);
    }
  }

  async function handleRetry() {
    if (!scan) return;
    setBusy('extract');
    setError(null);
    try {
      const result = await billsApi.reextract(scan.scan_token, firstProvider?.id);
      setScan(result);
      setDraft(result.draft);
      setDraftKey((k) => k + 1); // replaces any in-progress edits
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Re-read failed.');
    } finally {
      setBusy(null);
    }
  }

  async function handleDiscard() {
    if (!scan) return;
    setBusy('discard');
    setError(null);
    try {
      await billsApi.discardScan(scan.scan_token);
      reset();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to discard the scan.');
    } finally {
      setBusy(null);
    }
  }

  function reset() {
    if (previewUrl) URL.revokeObjectURL(previewUrl);
    setPreviewUrl(null);
    setScan(null);
    setDraft(null);
    setAccepted(null);
    setError(null);
    setPhase('capture');
  }

  const noProviderConfigured = !providers.loading && (providers.data ?? []).length === 0;

  return (
    <div className="page">
      <h2 className="page-title">Scan a bill</h2>

      {noProviderConfigured && (
        <div className="hint-banner">
          No AI connector configured yet — set one up in{' '}
          <Link to="/settings">Settings</Link> first. (Ollama runs locally and
          needs no API key.)
        </div>
      )}

      {phase === 'capture' && (
        <Card title="Receipt photo or PDF">
          <input
            ref={cameraInputRef}
            type="file"
            accept={FILE_ACCEPT}
            capture="environment"
            className="visually-hidden"
            onChange={(e) => handleFile(e.target.files?.[0])}
          />
          <input
            ref={fileInputRef}
            type="file"
            accept={FILE_ACCEPT}
            className="visually-hidden"
            onChange={(e) => handleFile(e.target.files?.[0])}
          />
          <div className="camera-row">
            <Button onClick={() => cameraInputRef.current?.click()} disabled={busy !== null}>
              📷 Take photo
            </Button>
            <Button
              variant="secondary"
              onClick={() => fileInputRef.current?.click()}
              disabled={busy !== null}
            >
              📁 Choose file
            </Button>
          </div>
          <p className="hint-text">
            On a phone, “Take photo” opens the camera directly. Photos (JPEG,
            HEIC) and PDF receipts are accepted. Flat, well-lit receipts read
            best.
          </p>
          {previewUrl && (
            <img className="bill-preview" src={previewUrl} alt="Receipt preview" />
          )}
          {busy === 'extract' && (
            <Spinner label="Uploading and reading the receipt — this can take up to a minute…" />
          )}
          {error && <ErrorMessage message={error} />}
        </Card>
      )}

      {phase === 'review' && draft && (
        <>
          {previewUrl && (
            <Card title="Receipt">
              <img className="bill-preview" src={previewUrl} alt="Receipt preview" />
            </Card>
          )}
          <Card title="Extracted draft — please review before confirming">
            <BillDraftEditor
              key={draftKey}
              draft={draft}
              onChange={setDraft}
              onConfirm={handleConfirm}
              onRetry={handleRetry}
              onDiscard={handleDiscard}
              busy={busy}
              error={error}
              accounts={(accounts.data ?? []).map((a) => ({
                id: a.id,
                name: a.name,
                card_last_digits: a.card_last_digits,
              }))}
              categories={categories.data ?? []}
              brands={brands.data ?? []}
              budgets={budgets.data ?? []}
            />
          </Card>
        </>
      )}

      {phase === 'done' && accepted && (
        <Card title="Saved">
          <p>
            Bill <strong>{accepted.market_name || `#${accepted.id}`}</strong> (
            {formatCents(accepted.total_cents, accepted.currency)}) accepted and
            stored for analysis.
          </p>
          <div className="camera-row">
            <Button onClick={reset}>Scan another bill</Button>
            <Link className="btn btn-secondary" to="/bills">
              View bills &amp; analysis
            </Link>
          </div>
        </Card>
      )}
    </div>
  );
}