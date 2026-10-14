import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatCents } from '../../lib/money';
import type { Product, ProductMergeCheck } from '../../types/domain';
import { Button, Dialog, ErrorMessage } from '../../components/ui';

interface ProductMergeDialogProps {
  check: ProductMergeCheck;
  /** The product being edited (merge source, with its live purchase stats). */
  source: Product;
  busy: boolean;
  error: string | null;
  /** Confirm the merge, keeping "source" or "target". */
  onKeep: (keep: 'source' | 'target') => void;
  /** Cancel — back to the edit form, nothing merged. */
  onClose: () => void;
}

/**
 * Merge confirmation for a rename that matched an existing product, in the
 * two shapes the backend plan allows:
 *
 * - different stores: the same product bought in different markets — a plain
 *   confirm; nothing is lost, the purchase history just groups.
 * - same store: a comparison table of both records; the user decides which
 *   to keep, the dropped record's history (prices included) folds into it.
 */
export function ProductMergeDialog({
  check,
  source,
  busy,
  error,
  onKeep,
  onClose,
}: ProductMergeDialogProps) {
  const { t } = useTranslation('products');
  const [keep, setKeep] = useState<'source' | 'target' | null>(null);
  const target = check.match;

  if (!target) return null;

  const confirm = () => {
    if (keep) onKeep(keep);
  };

  return (
    <Dialog title={t('merge.title')} wide>
      {check.reason === 'same_store' ? (
        <>
          <p>
            {t('merge.sameStore', { store: check.store_name })}
          </p>
          <div className="table-scroll">
            <table className="data-table merge-compare-table">
              <thead>
                <tr>
                  <th aria-label={t('merge.field')} />
                  <th className={keep === 'source' ? 'selected' : ''}>
                    <label className="merge-keep-label">
                      <input
                        type="radio"
                        name="merge-keep"
                        checked={keep === 'source'}
                        onChange={() => setKeep('source')}
                      />
                      {t('merge.keep', { name: source.name })}
                    </label>
                  </th>
                  <th className={keep === 'target' ? 'selected' : ''}>
                    <label className="merge-keep-label">
                      <input
                        type="radio"
                        name="merge-keep"
                        checked={keep === 'target'}
                        onChange={() => setKeep('target')}
                      />
                      {t('merge.keep', { name: target.name })}
                    </label>
                  </th>
                </tr>
              </thead>
              <tbody>
                <CompareRow label={t('merge.brand')} selected={keep} source={source.brand} target={target.brand} />
                <CompareRow label={t('merge.measure')} selected={keep} source={source.unit} target={target.unit} />
                <CompareRow
                  label={t('merge.category')}
                  selected={keep}
                  source={source.category_name}
                  target={target.category_name}
                />
                <CompareRow
                  label={t('merge.description')}
                  selected={keep}
                  source={source.description}
                  target={target.description}
                />
                <CompareRow
                  label={t('merge.latestPriceAt', { store: check.store_name })}
                  selected={keep}
                  source={formatStorePrice(check.source_store_price)}
                  target={formatStorePrice(check.target_store_price)}
                />
                <CompareRow
                  label={t('merge.lastPurchased')}
                  selected={keep}
                  source={source.last_purchase_date}
                  target={target.last_purchase_date}
                />
                <CompareRow
                  label={t('merge.timesBought')}
                  selected={keep}
                  source={String(source.times_bought)}
                  target={String(target.times_bought)}
                />
              </tbody>
            </table>
          </div>
        </>
      ) : (
        <p>
          {t('merge.differentStores', {
            source: source.name,
            target: target.name,
            sourceStores: joinNames(check.source_stores),
            targetStores: joinNames(check.target_stores),
          })}
        </p>
      )}

      {error && <ErrorMessage message={error} />}

      <div className="dialog-actions">
        <Button variant="ghost" onClick={onClose} disabled={busy}>
          {t('merge.cancel')}
        </Button>
        {check.reason === 'same_store' ? (
          <Button onClick={confirm} disabled={busy || !keep}>
            {busy ? t('merge.merging') : t('merge.mergeProducts')}
          </Button>
        ) : (
          <Button onClick={() => onKeep('source')} disabled={busy}>
            {busy ? t('merge.merging') : t('merge.mergeProducts')}
          </Button>
        )}
      </div>
    </Dialog>
  );
}

/** One comparison row: the field label and each side's value (—" when empty). */
function CompareRow({
  label,
  selected,
  source,
  target,
}: {
  label: string;
  selected: 'source' | 'target' | null;
  source?: string | null;
  target?: string | null;
}) {
  return (
    <tr>
      <th scope="row">{label}</th>
      <td className={selected === 'source' ? 'selected' : ''}>{source || '—'}</td>
      <td className={selected === 'target' ? 'selected' : ''}>{target || '—'}</td>
    </tr>
  );
}

/** Latest price at the compared store, currency-aware; "—" without one. */
function formatStorePrice(row: ProductMergeCheck['source_store_price']): string | undefined {
  if (row?.latest_price_cents === undefined || row.latest_price_cents === null) return undefined;
  return formatCents(row.latest_price_cents, row.currency ?? '');
}

/** "REWE, Aldi" — store list for the different-stores copy. */
function joinNames(names: string[] | undefined): string {
  return (names ?? []).join(', ') || '—';
}