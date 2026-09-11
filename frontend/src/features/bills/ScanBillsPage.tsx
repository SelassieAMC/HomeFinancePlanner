import { useEffect, useRef, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { billsApi } from '../../api/bills';
import { settingsApi } from '../../api/settings';
import { accountsApi } from '../../api/accounts';
import { categoriesApi } from '../../api/categories';
import { storesApi } from '../../api/stores';
import { budgetsApi } from '../../api/budgets';
import { ApiError } from '../../api/client';
import type { Bill, BillDraft, BillScan } from '../../types/domain';
import { useAsync } from '../../hooks/useAsync';
import { usePolling } from '../../hooks/usePolling';
import { formatCents } from '../../lib/money';
import { Button, Card, Spinner, ErrorMessage, Dialog } from '../../components/ui';
import {
  BillDraftEditor,
  buildConfirmInput,
  type BillBusyAction,
} from './BillDraftEditor';

type Phase = 'capture' | 'analyzing' | 'review' | 'done';

const FILE_ACCEPT = 'image/*,.heic,.heif,.pdf,application/pdf';

// Mirrors the backend's MaxBillImageBytes — oversized files are skipped
// before upload instead of failing on the server.
const MAX_BILL_IMAGE_BYTES = 10 * 1024 * 1024;

// One line in the post-upload summary: accepted, or rejected with a reason.
interface UploadOutcome {
  name: string;
  ok: boolean;
  message?: string;
}

export function ScanBillsPage() {
  const providers = useAsync(() => settingsApi.listAIProviders(), []);
  const accounts = useAsync(() => accountsApi.list(), []);
  const categories = useAsync(() => categoriesApi.list(), []);
  const brands = useAsync(() => billsApi.brands(), []);
  const stores = useAsync(() => storesApi.list(), []);

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
  const [error, setError] = useState<string | null>(null);
  // Summary of the last batch of uploads (capture phase only).
  const [uploadResults, setUploadResults] = useState<UploadOutcome[]>([]);
  // Shown after a re-read is enqueued: analysis runs in the background.
  const [rereadSent, setRereadSent] = useState(false);
  const navigate = useNavigate();

  const cameraInputRef = useRef<HTMLInputElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const firstProvider = providers.data?.[0];

  // Deep link from the bills view: /scan?token=… resumes an existing scan.
  const [searchParams] = useSearchParams();
  const deepLinkRef = useRef(false);

  // While a scan is analyzing, poll its pipeline state every 2s.
  const scanPoll = usePolling(
    () =>
      scan
        ? billsApi.getScan(scan.scan_token)
        : Promise.reject(new Error('no scan to poll')),
    { intervalMs: 2000, enabled: phase === 'analyzing' && scan !== null, maxMs: 10 * 60_000 },
  );

  // Poll result → phase transitions.
  useEffect(() => {
    const polled = scanPoll.data;
    if (phase !== 'analyzing' || !polled) return;
    setScan(polled); // keeps the latest status/error for display
    if (polled.status === 'done' && polled.draft) {
      setDraft(polled.draft);
      setDraftKey((k) => k + 1); // fresh extraction → remount the editor cells
      setPhase('review');
    }
  }, [scanPoll.data, phase]);

  // A consumed/expired token (confirmed, discarded, or swept) stops polling.
  useEffect(() => {
    if (phase === 'analyzing' && scanPoll.error instanceof ApiError && scanPoll.error.status === 404) {
      setScan(null);
      setPhase('capture');
      setError('scan not found or expired — scan the receipt again');
    }
  }, [scanPoll.error, phase]);

  // Resume a scan reached from the bills view (?token=…).
  useEffect(() => {
    if (deepLinkRef.current) return;
    deepLinkRef.current = true;
    const token = searchParams.get('token');
    if (!token) return;
    billsApi
      .getScan(token)
      .then((res) => {
        setScan(res);
        if (res.status === 'done' && res.draft) {
          setDraft(res.draft);
          setDraftKey((k) => k + 1);
          setPhase('review');
        } else {
          setPhase('analyzing');
        }
      })
      .catch((err: unknown) => {
        setScan(null);
        setPhase('capture');
        setError(err instanceof Error ? err.message : 'Scan not found.');
      });
  }, [searchParams]);

  // Uploads each selected receipt as its own scan. Analysis runs in the
  // background — the user only gets an upload summary here and checks the
  // result later in the bills view. Files over the size limit are skipped
  // client-side.
  async function handleFiles(files: FileList | null) {
    if (!files || files.length === 0 || busy) return;
    setError(null);
    setUploadResults([]);
    setBusy('extract');

    const results: UploadOutcome[] = [];
    for (const file of Array.from(files)) {
      if (file.size > MAX_BILL_IMAGE_BYTES) {
        results.push({
          name: file.name,
          ok: false,
          message: `skipped — larger than the ${MAX_BILL_IMAGE_BYTES >> 20} MB limit`,
        });
        continue;
      }
      try {
        // Returns immediately; extraction runs in the background. Nothing
        // is lost if the user leaves the page now.
        await billsApi.scan(file, firstProvider?.id);
        results.push({ name: file.name, ok: true });
      } catch (err) {
        results.push({
          name: file.name,
          ok: false,
          message: err instanceof Error ? err.message : 'Upload failed.',
        });
      }
    }
    setUploadResults(results);
    setBusy(null);
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
      await billsApi.reextract(scan.scan_token, firstProvider?.id);
      // Analysis continues in the background — confirm via dialog, then the
      // user goes where the result will be (or stays to upload more).
      setRereadSent(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Re-read failed.');
    } finally {
      setBusy(null);
    }
  }

  // Deep-linked from the bills view (?token=…) → OK returns there; a user in
  // the middle of an upload session stays on the scan page.
  const rereadReturnsToBills = searchParams.has('token');

  function dismissReread() {
    setRereadSent(false);
    if (rereadReturnsToBills) {
      navigate('/bills');
    } else {
      reset(); // back to capture — the user may want to upload more receipts
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
    setScan(null);
    setDraft(null);
    setAccepted(null);
    setError(null);
    setUploadResults([]);
    setPhase('capture');
  }

  const noProviderConfigured = !providers.loading && (providers.data ?? []).length === 0;
  const acceptedCount = uploadResults.filter((r) => r.ok).length;

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
        <Card title="Receipt photos or PDFs">
          <input
            ref={cameraInputRef}
            type="file"
            accept={FILE_ACCEPT}
            capture="environment"
            className="visually-hidden"
            onChange={(e) => {
              handleFiles(e.target.files);
              e.target.value = ''; // allow re-selecting the same file
            }}
          />
          <input
            ref={fileInputRef}
            type="file"
            accept={FILE_ACCEPT}
            multiple
            className="visually-hidden"
            onChange={(e) => {
              handleFiles(e.target.files);
              e.target.value = ''; // allow re-selecting the same files
            }}
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
              📁 Choose files
            </Button>
          </div>
          <p className="hint-text">
            On a phone, “Take photo” opens the camera directly. You can pick
            several receipts at once. Photos (JPEG, HEIC) and PDF receipts are
            accepted — each file up to {MAX_BILL_IMAGE_BYTES >> 20} MB. Flat,
            well-lit receipts read best.
          </p>
          {busy === 'extract' && (
            <Spinner label="Uploading receipts — they are analysed in the background…" />
          )}
          {error && <ErrorMessage message={error} />}
          {uploadResults.length > 0 && !busy && (
            <>
              <ul className="upload-results">
                {uploadResults.map((r) => (
                  <li key={r.name}>
                    <span aria-hidden="true">{r.ok ? '✅' : '❌'}</span> {r.name}
                    {r.message && <span className="hint-inline"> — {r.message}</span>}
                  </li>
                ))}
              </ul>
              {acceptedCount > 0 ? (
                <div className="hint-banner">
                  {acceptedCount} receipt{acceptedCount === 1 ? '' : 's'} accepted
                  for analysis — this can take a few minutes per receipt. Check the
                  status later in <Link to="/bills">Bills &amp; analysis</Link> and
                  review each extracted draft there.
                </div>
              ) : (
                <div className="hint-banner">
                  No receipts were accepted for analysis — see the reasons above
                  and try again.
                </div>
              )}
            </>
          )}
        </Card>
      )}

      {phase === 'analyzing' && (
        <Card title="Receipt">
          {scan?.status === 'failed' ? (
            <>
              <ErrorMessage message={scan.error || 'Analysis failed.'} />
              <div className="camera-row">
                <Button onClick={handleRetry} disabled={busy !== null}>
                  🔁 Try again
                </Button>
                <Button variant="secondary" onClick={handleDiscard} disabled={busy !== null}>
                  Discard
                </Button>
              </div>
            </>
          ) : scanPoll.timedOut ? (
            <div className="hint-banner">
              Still analyzing — large receipts can take a while. You can leave
              this page; the scan keeps running and appears under{' '}
              <Link to="/bills">Bills &amp; analysis</Link> once it finishes.
            </div>
          ) : (
            <Spinner label="Reading the receipt — you can leave this page; the scan is kept in the Bills view." />
          )}
          {error && <ErrorMessage message={error} />}
        </Card>
      )}

      {phase === 'review' && draft && (
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
            stores={stores.data ?? []}
          />
        </Card>
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

      {rereadSent && (
        <Dialog title="Re-read request sent">
          <p className="hint-text">
            The receipt has been queued for analysis in the background — this
            can take a few minutes.{' '}
            {rereadReturnsToBills
              ? "The result will appear under Bills & analysis once it's ready."
              : 'You can keep uploading receipts in the meantime — the result will appear under Bills & analysis.'}
          </p>
          <div className="dialog-actions">
            <Button onClick={dismissReread}>OK</Button>
          </div>
        </Dialog>
      )}
    </div>
  );
}