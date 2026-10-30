import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

/**
 * Receipt viewer used by the scan review: the photo the draft was read from,
 * rendered in an in-page overlay with zoom (+/− steps and double-click to
 * toggle 2×) and, for multi-part uploads, part tabs. Zoom scales the image's
 * natural size so the scrollbars stay proportional (scroll to pan).
 */
export function ReceiptPreviewModal({
  src,
  parts,
  onClose,
}: {
  /** Image URL builder for one receipt part (1-based). */
  src: (part: number) => string;
  parts: number;
  onClose: () => void;
}) {
  const { t } = useTranslation('bills');
  const [part, setPart] = useState(1);
  const [zoom, setZoom] = useState(1);

  // Esc closes like the dialogs; Backdrop-click closes too.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const stepZoom = (delta: number) =>
    setZoom((z) => Math.min(4, Math.max(1, Math.round((z + delta) * 10) / 10)));

  return (
    <div
      className="dialog-backdrop receipt-backdrop"
      role="dialog"
      aria-modal="true"
      aria-label={t('editor.receipt')}
      onClick={onClose}
    >
      <div className="receipt-viewer" onClick={(e) => e.stopPropagation()}>
        <div className="receipt-viewer-bar">
          {parts > 1 &&
            Array.from({ length: parts }, (_, i) => (
              <button
                key={i}
                type="button"
                className={`receipt-part-tab${part === i + 1 ? ' active' : ''}`}
                aria-pressed={part === i + 1}
                onClick={() => {
                  setPart(i + 1);
                  setZoom((z) => Math.max(1, Math.min(z, 2))); // new photo, sane zoom
                }}
              >
                {t('view.part', { n: i + 1 })}
              </button>
            ))}
          <span className="receipt-viewer-spacer" />
          <a
            className="receipt-download-btn"
            href={src(part)}
            download={`receipt-part-${part}.jpg`}
            aria-label={t('editor.receiptDownload')}
            title={t('editor.receiptDownload')}
          >
            ⬇
          </a>
          <span className="receipt-zoom-controls">
            <button
              type="button"
              onClick={() => stepZoom(-0.5)}
              disabled={zoom <= 1}
              aria-label={t('editor.receiptZoomOut')}
              title={t('editor.receiptZoomOut')}
            >
              −
            </button>
            <button
              type="button"
              onClick={() => setZoom(1)}
              disabled={zoom === 1}
              aria-label={t('editor.receiptZoomReset')}
              title={t('editor.receiptZoomReset')}
            >
              {Math.round(zoom * 100)}%
            </button>
            <button
              type="button"
              onClick={() => stepZoom(0.5)}
              disabled={zoom >= 4}
              aria-label={t('editor.receiptZoomIn')}
              title={t('editor.receiptZoomIn')}
            >
              +
            </button>
          </span>
          <button
            type="button"
            className="receipt-viewer-close"
            onClick={onClose}
            aria-label={t('editor.receiptClose')}
            title={t('editor.receiptClose')}
          >
            ✕
          </button>
        </div>
        <div className="receipt-viewer-body">
          <img
            src={src(part)}
            alt={t('editor.receipt')}
            style={{ width: `${Math.round(zoom * 100)}%`, maxWidth: 'none' }}
            onDoubleClick={() => setZoom((z) => (z > 1 ? 1 : 2))}
          />
        </div>
      </div>
    </div>
  );
}