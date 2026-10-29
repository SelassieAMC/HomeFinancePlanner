import { useEffect, useRef, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
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
import { decodeImage, reencodeFileRotated } from '../../lib/image';
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

// Mirrors the backend's MaxBillScanFiles — a receipt split across photos can
// take at most this many files.
const MAX_BILL_SCAN_FILES = 8;

// One line in the post-upload summary: accepted, or rejected with a reason.
interface UploadOutcome {
  name: string;
  ok: boolean;
  message?: string;
}

export function ScanBillsPage() {
  const { t } = useTranslation('bills');
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
  // Budget envelopes are open-ended — any of them can be attached to a bill
  // regardless of the bill's date, so no month filtering here.
  const budgets = useAsync(() => budgetsApi.list('open'), []);
  const [accepted, setAccepted] = useState<Bill | null>(null);
  const [error, setError] = useState<string | null>(null);
  // Summary of the last batch of uploads (capture phase only).
  const [uploadResults, setUploadResults] = useState<UploadOutcome[]>([]);
  // Checked = the selected files are consecutive parts of ONE long receipt,
  // sent as a single scan and merged into one bill by the AI read.
  const [groupAsOneBill, setGroupAsOneBill] = useState(false);
  // Staged but not yet sent photos/PDFs: camera taps and file picks append
  // here, and the confirm button below submits the whole batch for analysis.
  const [picked, setPicked] = useState<File[]>([]);
  // Preview URL per staged entry (null = not decodable in-browser, e.g. a
  // PDF) — rebuilt when a file is rotated into a new blob.
  const [thumbs, setThumbs] = useState<(string | null)[]>([]);
  // Shown after a re-read is enqueued: analysis runs in the background.
  const [rereadSent, setRereadSent] = useState(false);
  const navigate = useNavigate();

  const cameraInputRef = useRef<HTMLInputElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // The connector used when the user doesn't pick one: the connector marked
  // default for bill reads, falling back to the first configured.
  const firstProvider = providers.data?.find((p) => p.default_for_bills) ?? providers.data?.[0];

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
      // Older stored drafts may marshal `items: null` (an AI read with no
      // usable lines); the editor reduces over the list at render time.
      setDraft({ ...polled.draft, items: polled.draft.items ?? [] });
      setDraftKey((k) => k + 1); // fresh extraction → remount the editor cells
      setPhase('review');
    }
  }, [scanPoll.data, phase]);

  // A consumed/expired token (confirmed, discarded, or swept) stops polling.
  useEffect(() => {
    if (phase === 'analyzing' && scanPoll.error instanceof ApiError && scanPoll.error.status === 404) {
      setScan(null);
      setPhase('capture');
      setError(t('errors.scanExpired'));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
          setDraft({ ...res.draft, items: res.draft.items ?? [] });
          setDraftKey((k) => k + 1);
          setPhase('review');
        } else {
          setPhase('analyzing');
        }
      })
      .catch((err: unknown) => {
        setScan(null);
        setPhase('capture');
        setError(err instanceof Error ? err.message : t('errors.scanNotFound'));
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchParams]);

  // Stages the newly chosen files without uploading: the user collects
  // camera shots and file picks first and confirms the batch with the button
  // below. The list is copied synchronously and eagerly — the onChange that
  // calls this clears input.value right after, which detaches the FileList,
  // so deferring the copy into a state updater would read an emptied list.
  // Duplicates of an already-staged file (same name, size and timestamp) are
  // dropped — the server rejects a photo twice within one upload anyway.
  function handleFiles(files: FileList | null) {
    if (!files || files.length === 0 || busy) return;
    setError(null);
    const fresh: File[] = [];
    const seen = new Set(picked.map((f) => `${f.name} ${f.size} ${f.lastModified}`));
    for (const file of Array.from(files)) {
      const key = `${file.name} ${file.size} ${file.lastModified}`;
      if (seen.has(key)) continue;
      seen.add(key);
      fresh.push(file);
    }
    if (fresh.length === 0) return;
    setPicked((prev) => [...prev, ...fresh]);
  }

  // Preview URLs track the staged entries: one thumb per file, null when the
  // browser cannot render it. Each rebuild revokes the previous generation —
  // the staged files are local blobs, so the URLs are free to die with them.
  const thumbsRef = useRef<(string | null)[]>([]);
  useEffect(() => {
    const previous = thumbsRef.current;
    let cancelled = false;
    Promise.all(
      picked.map((file) =>
        decodeImage(file).then(
          (b) => {
            if (b !== null) b.close();
            return b !== null ? URL.createObjectURL(file) : null;
          },
          () => null,
        ),
      ),
    ).then((next) => {
      if (cancelled) {
        for (const u of next) if (u !== null) URL.revokeObjectURL(u);
        return;
      }
      for (const u of previous) if (u !== null) URL.revokeObjectURL(u);
      thumbsRef.current = next;
      setThumbs(next);
    });
    return () => {
      cancelled = true; // the next run (or none) still revokes both generations
    };
  }, [picked]);

  // Turns one staged photo a quarter-turn clockwise and swaps in the
  // re-encoded file, so both the preview and the upload show it upright.
  async function rotatePicked(index: number) {
    const file = picked[index];
    if (!file || busy) return;
    try {
      const turned = await reencodeFileRotated(file, 1);
      setPicked((prev) => prev.map((f, i) => (i === index ? turned : f)));
    } catch {
      // Not decodable in this browser (PDF, HEIC in Chromium) — the rotate
      // controls stay disabled; the original file is uploaded unchanged.
    }
  }

  function removePicked(index: number) {
    setPicked((prev) => prev.filter((_, i) => i !== index));
  }

  // Sends the staged batch for analysis — the confirm button in the capture
  // card. Analysis runs in the background — the user only gets an upload
  // summary here and checks the result later in the bills view. With "one
  // bill" unchecked, each file is its own scan. With it checked and several
  // files staged, they are sent as ONE grouped scan (the consecutive parts of
  // one long receipt) and merged into a single bill by the AI read. Files
  // over the size limit are skipped client-side.
  async function submitForScan() {
    if (picked.length === 0 || busy) return;
    setError(null);
    setUploadResults([]);
    setBusy('extract');

    const list = picked;
    const results: UploadOutcome[] = [];

    if (groupAsOneBill && list.length > 1) {
      await uploadGrouped(list, results);
    } else {
      for (const file of list) {
        if (file.size > MAX_BILL_IMAGE_BYTES) {
          results.push({
            name: file.name,
            ok: false,
            message: t('scan.skipOversize', { limit: MAX_BILL_IMAGE_BYTES >> 20 }),
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
            message: err instanceof Error ? err.message : t('errors.upload'),
          });
        }
      }
    }
    setUploadResults(results);
    setBusy(null);
    // Sent receipts leave the staging list; on a total failure (everything
    // skipped or rejected) it stays as it is, so the list can be fixed up
    // with the ✕ buttons and retried without picking the files again.
    if (results.some((r) => r.ok)) setPicked([]);
  }

  // One receipt split across several files: a single grouped upload with every
  // part. Pre-checks reject the whole group before it is sent (a partial
  // receipt would lose lines), naming each offending file.
  async function uploadGrouped(list: File[], results: UploadOutcome[]) {
    const groupLabel = t('scan.groupPhotos', { n: list.length });
    if (list.length > MAX_BILL_SCAN_FILES) {
      results.push({
        name: groupLabel,
        ok: false,
        message: t('scan.skipTooMany', { max: MAX_BILL_SCAN_FILES }),
      });
      return;
    }
    const oversized = list.filter((f) => f.size > MAX_BILL_IMAGE_BYTES);
    if (oversized.length > 0) {
      for (const file of oversized) {
        results.push({
          name: file.name,
          ok: false,
          message: t('scan.skipOversize', { limit: MAX_BILL_IMAGE_BYTES >> 20 }),
        });
      }
      results.push({ name: groupLabel, ok: false, message: t('scan.groupNotSent') });
      return;
    }
    try {
      // One request, one scan token: the parts are merged into one bill.
      await billsApi.scan(list, firstProvider?.id);
      results.push({ name: groupLabel, ok: true });
    } catch (err) {
      results.push({
        name: groupLabel,
        ok: false,
        message: err instanceof Error ? err.message : t('errors.upload'),
      });
    }
  }

  async function handleConfirm(accountId?: number, createCardAccount?: boolean, fxRate?: number) {
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
          opening_balance_cents: 0,
          card_last_digits: draft.card_last_digits,
        });
        targetId = created.id;
        accounts.reload();
      }
      const bill = await billsApi.confirm(
        scan.scan_token,
        buildConfirmInput(
          draft,
          targetId,
          (accounts.data ?? []).map((a) => ({
            id: a.id,
            type: a.type,
            card_last_digits: a.card_last_digits,
          })),
          fxRate,
        ),
      );
      setAccepted(bill);
      setPhase('done');
    } catch (err) {
      setError(err instanceof Error ? err.message : t('errors.confirm'));
    } finally {
      setBusy(null);
    }
  }

  async function handleRetry() {
    if (!scan) return;
    setBusy('extract');
    setError(null);
    try {
      const next = await billsApi.reextract(scan.scan_token, firstProvider?.id);
      setScan(next); // back to analyzing — switches the view off failed/cancelled
      // Analysis continues in the background — confirm via dialog, then the
      // user goes where the result will be (or stays to upload more).
      setRereadSent(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('errors.reread'));
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

  // Aborts an in-progress analysis: the scan is marked cancelled and the
  // running AI read is stopped server-side. The poll picks up the cancelled
  // state, which offers re-running the read or deleting the request.
  async function handleCancelScan() {
    if (!scan) return;
    setBusy('cancel');
    setError(null);
    try {
      setScan(await billsApi.cancelScan(scan.scan_token));
    } catch (err) {
      setError(err instanceof Error ? err.message : t('errors.cancel'));
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
      setError(err instanceof Error ? err.message : t('errors.discard'));
    } finally {
      setBusy(null);
    }
  }

  function reset() {
    setScan(null);
    setDraft(null);
    setAccepted(null);
    setError(null);
    setPicked([]);
    setUploadResults([]);
    setPhase('capture');
  }

  const noProviderConfigured = !providers.loading && (providers.data ?? []).length === 0;
  const acceptedCount = uploadResults.filter((r) => r.ok).length;

  return (
    <div className="page">
      <h2 className="page-title">{t('scan.title')}</h2>

      {noProviderConfigured && (
        <div className="hint-banner">
          {t('scan.noProviderBefore')}
          <Link to="/settings">{t('scan.settings')}</Link>
          {t('scan.noProviderAfter')}
        </div>
      )}

      {phase === 'capture' && (
        <Card
          title={t('scan.cardTitle')}
          help={
            <>
              {t('scan.hint', { sizeLimit: MAX_BILL_IMAGE_BYTES >> 20, maxFiles: MAX_BILL_SCAN_FILES })}
              <em>{t('scan.hintHorizontal')}</em>
              {t('scan.hintTail')}
            </>
          }
        >
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
              {t('scan.takePhoto')}
            </Button>
            <Button
              variant="secondary"
              onClick={() => fileInputRef.current?.click()}
              disabled={busy !== null}
            >
              {t('scan.chooseFiles')}
            </Button>
          </div>
          <label className="default-radio">
            <input
              type="checkbox"
              checked={groupAsOneBill}
              onChange={(e) => setGroupAsOneBill(e.target.checked)}
            />
            {t('scan.groupLabel')}
          </label>
          {groupAsOneBill && (
            <p className="hint-inline">
              {t('scan.groupHint')}
            </p>
          )}
          {picked.length > 0 && (
            <ul className="upload-results">
              {picked.map((file, index) => (
                <li key={`${file.name} ${index}`} className="picked-entry">
                  {thumbs[index] !== null && thumbs[index] !== undefined ? (
                    <img
                      className="picked-thumb"
                      src={thumbs[index] as string}
                      alt={t('scan.previewAlt', { name: file.name })}
                    />
                  ) : (
                    <span className="picked-thumb picked-thumb-blank" aria-hidden="true" />
                  )}
                  <span className="picked-meta">
                    {file.name}
                    <span className="hint-inline">
                      {' '}— {(file.size / (1024 * 1024)).toFixed(1)} MB
                    </span>
                  </span>
                  <span className="picked-actions">
                    <button
                      type="button"
                      className="btn btn-ghost remove-picked"
                      onClick={() => rotatePicked(index)}
                      disabled={busy !== null || thumbs[index] === null}
                      aria-label={t('scan.turnAria', { name: file.name })}
                      title={t('scan.rotateTitle')}
                    >
                      ↻
                    </button>
                    <button
                      type="button"
                      className="btn btn-ghost remove-picked"
                      onClick={() => removePicked(index)}
                      disabled={busy !== null}
                      aria-label={t('scan.removeAria', { name: file.name })}
                    >
                      ✕
                    </button>
                  </span>
                </li>
              ))}
            </ul>
          )}
          <div className="camera-row">
            <Button
              onClick={submitForScan}
              disabled={busy !== null || picked.length === 0}
              title={
                picked.length === 0
                  ? t('scan.emptyTitle')
                  : undefined
              }
            >
              {t('scan.confirmScan')}{picked.length > 0 ? ` (${picked.length})` : ''}
            </Button>
            <Button
              variant="ghost"
              onClick={() => setPicked([])}
              disabled={busy !== null || picked.length === 0}
            >
              {t('scan.clear')}
            </Button>
          </div>
          {busy === 'extract' && (
            <Spinner label={t('scan.uploading')} />
          )}
          {error && <ErrorMessage message={error} />}
          {uploadResults.length > 0 && !busy && (
            <>
              <ul className="upload-results">
                {uploadResults.map((r) => (
                  <li key={r.name}>
                    <span aria-hidden="true">{r.ok ? '✓' : '✕'}</span> {r.name}
                    {r.message && <span className="hint-inline"> — {r.message}</span>}
                  </li>
                ))}
              </ul>
              {acceptedCount > 0 ? (
                <div className="hint-banner">
                  {t('scan.accepted', { count: acceptedCount })}
                  <Link to="/bills">{t('common.nav')}</Link>
                  {t('scan.acceptedTail')}
                </div>
              ) : (
                <div className="hint-banner">
                  {t('scan.noneAccepted')}
                </div>
              )}
            </>
          )}
        </Card>
      )}

      {phase === 'analyzing' && (
        <Card title={t('analyzing.cardTitle')}>
          {scan?.status === 'failed' ? (
            <>
              <ErrorMessage message={scan.error || t('errors.analysisFailed')} />
              <div className="camera-row">
                <Button onClick={handleRetry} disabled={busy !== null}>
                  {t('action.tryAgain')}
                </Button>
                <Button variant="secondary" onClick={handleDiscard} disabled={busy !== null}>
                  {t('action.discard')}
                </Button>
              </div>
            </>
          ) : scan?.status === 'cancelled' ? (
            <>
              <div className="hint-banner">
                {t('analyzing.cancelledBanner')}
              </div>
              <div className="camera-row">
                <Button onClick={handleRetry} disabled={busy !== null}>
                  {t('action.tryAgain')}
                </Button>
                <Button variant="danger" onClick={handleDiscard} disabled={busy !== null}>
                  {t('action.deleteRequest')}
                </Button>
              </div>
            </>
          ) : scanPoll.timedOut ? (
            <>
              <div className="hint-banner">
                {t('analyzing.stillRunning')}
                <Link to="/bills">{t('common.nav')}</Link>
                {t('analyzing.stillTail')}
              </div>
              <div className="camera-row">
                <Button variant="danger" onClick={handleCancelScan} disabled={busy !== null}>
                  {t('action.cancelAnalysis')}
                </Button>
              </div>
            </>
          ) : (
            <>
              <Spinner label={t('analyzing.readingLong')} />
              <div className="camera-row">
                <Button variant="danger" onClick={handleCancelScan} disabled={busy !== null}>
                  {t('action.cancelAnalysis')}
                </Button>
              </div>
            </>
          )}
          {error && <ErrorMessage message={error} />}
        </Card>
      )}

      {phase === 'review' && draft && (
        <Card title={t('review.cardTitle')}>
          <BillDraftEditor
            key={draftKey}
            draft={draft}
            onChange={setDraft}
            onConfirm={handleConfirm}
            onRetry={handleRetry}
            onDiscard={handleDiscard}
            busy={busy}
            receipt={
              scan
                ? {
                    // The stored photo(s) of this exact scan — any pipeline
                    // state is servable, so the preview works mid-analyze too.
                    src: (part: number) =>
                      `/api/v1/bills/scan/${scan.scan_token}/image?part=${part}`,
                    parts: scan.file_count ?? 1,
                  }
                : null
            }
            error={error}
            accounts={(accounts.data ?? []).map((a) => ({
              id: a.id,
              name: a.name,
              type: a.type,
              currency: a.currency,
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
        <Card title={t('done.title')}>
          <p>
            {t('done.prefix')}
            <strong>{accepted.market_name || `#${accepted.id}`}</strong> (
            {formatCents(accepted.total_cents, accepted.currency)})
            {t('done.tail')}
          </p>
          <div className="hint-banner">
            {t('done.insightHint')}
          </div>
          <div className="camera-row">
            <Button onClick={reset}>{t('done.scanAnother')}</Button>
            <Link className="btn btn-secondary" to="/bills">
              {t('done.viewBills')}
            </Link>
          </div>
        </Card>
      )}

      {rereadSent && (
        <Dialog title={t('rereadDialog.title')} onClose={() => setRereadSent(false)}>
          <p className="hint-text">
            {t('rereadDialog.scanIntro')}{' '}
            {rereadReturnsToBills
              ? t('rereadDialog.scanReturns')
              : t('rereadDialog.scanKeep')}
          </p>
          <div className="dialog-actions">
            <Button onClick={dismissReread}>{t('common.ok')}</Button>
          </div>
        </Dialog>
      )}
    </div>
  );
}