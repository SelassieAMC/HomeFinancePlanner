import { useState } from 'react';
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
  const [keep, setKeep] = useState<'source' | 'target' | null>(null);
  const target = check.match;

  if (!target) return null;

  const confirm = () => {
    if (keep) onKeep(keep);
  };

  return (
    <Dialog title="Merge products" wide>
      {check.reason === 'same_store' ? (
        <>
          <p>
            Both products were bought at <strong>{check.store_name}</strong>. Their
            information is compared below — choose which record to keep. The other
            one is removed and its purchase history (prices included) is grouped
            under the kept product.
          </p>
          <div className="table-scroll">
            <table className="data-table merge-compare-table">
              <thead>
                <tr>
                  <th aria-label="Field" />
                  <th className={keep === 'source' ? 'selected' : ''}>
                    <label className="merge-keep-label">
                      <input
                        type="radio"
                        name="merge-keep"
                        checked={keep === 'source'}
                        onChange={() => setKeep('source')}
                      />
                      Keep “{source.name}”
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
                      Keep “{target.name}”
                    </label>
                  </th>
                </tr>
              </thead>
              <tbody>
                <CompareRow label="Brand" selected={keep} source={source.brand} target={target.brand} />
                <CompareRow label="Measure" selected={keep} source={source.unit} target={target.unit} />
                <CompareRow
                  label="Category"
                  selected={keep}
                  source={source.category_name}
                  target={target.category_name}
                />
                <CompareRow
                  label="Description"
                  selected={keep}
                  source={source.description}
                  target={target.description}
                />
                <CompareRow
                  label={`Latest price at ${check.store_name}`}
                  selected={keep}
                  source={formatStorePrice(check.source_store_price)}
                  target={formatStorePrice(check.target_store_price)}
                />
                <CompareRow
                  label="Last purchased"
                  selected={keep}
                  source={source.last_purchase_date}
                  target={target.last_purchase_date}
                />
                <CompareRow
                  label="Times bought"
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
          “{source.name}” will be merged with the existing product “{target.name}”.
          They were bought at different stores ({joinNames(check.source_stores)} vs{' '}
          {joinNames(check.target_stores)}), so this is the same product bought in
          different markets. No information is lost — the purchase history is
          grouped under one product.
        </p>
      )}

      {error && <ErrorMessage message={error} />}

      <div className="dialog-actions">
        <Button variant="ghost" onClick={onClose} disabled={busy}>
          Cancel
        </Button>
        {check.reason === 'same_store' ? (
          <Button onClick={confirm} disabled={busy || !keep}>
            {busy ? 'Merging…' : 'Merge products'}
          </Button>
        ) : (
          <Button onClick={() => onKeep('source')} disabled={busy}>
            {busy ? 'Merging…' : 'Merge products'}
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