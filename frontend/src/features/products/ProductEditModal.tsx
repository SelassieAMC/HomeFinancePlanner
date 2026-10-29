import { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { productsApi } from '../../api/products';
import { formatCents, parseDecimalInput } from '../../lib/money';
import type { Category, Product, ProductInput, ProductMergeCheck } from '../../types/domain';
import { ProductMergeDialog } from './ProductMergeDialog';
import {
  Button,
  CategorySelect,
  Dialog,
  ErrorMessage,
  Field,
  UnitSelect,
} from '../../components/ui';

interface ProductEditModalProps {
  product: Product;
  categories: Category[];
  /** Discard changes and close the modal. */
  onClose: () => void;
  /** Save succeeded — reload the list and close. */
  onSaved: () => void;
}

/**
 * Product edit modal, laid out like a mini editor page: basic information and
 * purchase stats on the left, photo card with drag & drop and the footer
 * actions on the right. Saving updates the products row only — historical
 * bill and transaction lines keep their snapshot values — and the
 * standardized-name field learns the normalization mapping (source user).
 */
export function ProductEditModal({ product, categories, onClose, onSaved }: ProductEditModalProps) {
  const { t } = useTranslation('products');
  const [name, setName] = useState(product.name);
  const [brand, setBrand] = useState(product.brand ?? '');
  const [unit, setUnit] = useState(product.unit ?? '');
  // Printed size magnitude ("500" for "500ml") as text; '' keeps it untouched
  // (learned values survive), a non-positive number clears it.
  const [unitValue, setUnitValue] = useState(product.unit_value != null ? String(product.unit_value) : '');
  const [categoryId, setCategoryId] = useState<number | null>(product.category_id ?? null);
  const [description, setDescription] = useState(product.description ?? '');
  // Prefilled with the remembered standard name (raw name when unmapped);
  // saving learns it into the normalization mapping like bill line edits do.
  const [standardName, setStandardName] = useState(product.standard_name || product.name);
  // Remembered generic product family ('' = no broader family known).
  const [genericName, setGenericName] = useState(product.generic_name || '');
  // Photo edits return a fresh product (new updated_at → cache-busted URL).
  const [current, setCurrent] = useState(product);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  // Rename matched an existing product: the merge plan awaiting confirmation.
  const [mergeCheck, setMergeCheck] = useState<ProductMergeCheck | null>(null);
  const [pendingInput, setPendingInput] = useState<ProductInput | null>(null);
  const [mergeBusy, setMergeBusy] = useState(false);
  const [mergeError, setMergeError] = useState<string | null>(null);
  const photoInputRef = useRef<HTMLInputElement>(null);

  function buildInput(): ProductInput {
    // Decimal comma tolerated (mobile keypads type it); blank = untouched,
    // non-positive or unparseable = clear.
    const magnitude = unitValue.trim();
    const parsed = magnitude === '' ? NaN : parseDecimalInput(magnitude);
    return {
      name: name.trim(),
      brand: brand.trim(),
      unit: unit.trim().toLowerCase(),
      unit_value: magnitude === '' ? undefined : Number.isFinite(parsed) && parsed > 0 ? parsed : 0,
      category_id: categoryId,
      description: description.trim(),
      standard_name: standardName.trim(),
      generic_name: genericName.trim(),
    };
  }

  async function doSave(input: ProductInput) {
    setBusy(true);
    setFormError(null);
    try {
      await productsApi.update(product.id, input);
      onSaved();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.save'));
    } finally {
      setBusy(false);
    }
  }

  /** Fold the product into the matched one (the rename target). */
  async function doMerge(keep: 'source' | 'target') {
    if (!mergeCheck?.match || !pendingInput) return;
    setMergeBusy(true);
    setMergeError(null);
    try {
      await productsApi.merge(product.id, {
        merge_with: mergeCheck.match.id,
        keep,
        // The typed edit applies only when the source survives; keeping the
        // target means the user chose its data over the edit.
        product: keep === 'source' ? pendingInput : undefined,
      });
      onSaved();
    } catch (err) {
      setMergeError(err instanceof Error ? err.message : t('errors.merge'));
    } finally {
      setMergeBusy(false);
    }
  }

  async function handleSaveClick() {
    if (!name.trim()) {
      setFormError(t('errors.nameRequired'));
      return;
    }
    const input = buildInput();
    // A changed name may collide with an existing product: pre-check before
    // any write so the UI can offer the merge instead of a 409.
    if (input.name.toLowerCase() !== product.name.toLowerCase()) {
      setBusy(true);
      setFormError(null);
      try {
        const check = await productsApi.checkMerge(product.id, input.name);
        if (check.match) {
          if (!check.mergeable) {
            setFormError(t('errors.mergeConflict', { name: check.match.name }));
            return;
          }
          setPendingInput(input);
          setMergeCheck(check);
          return;
        }
      } catch (err) {
        setFormError(err instanceof Error ? err.message : t('errors.check'));
        return;
      } finally {
        setBusy(false);
      }
    }
    void doSave(input);
  }

  async function handleUploadPhoto(file: File | null | undefined) {
    if (!file) return;
    setFormError(null);
    try {
      setCurrent(await productsApi.uploadPhoto(product.id, file));
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.uploadPhoto'));
    }
  }

  async function handleRemovePhoto() {
    setFormError(null);
    try {
      setCurrent(await productsApi.removePhoto(product.id));
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.removePhoto'));
    }
  }

  return (
    <Dialog title={t('actions.editTitle')} wide onClose={onClose}>
      <div className="edit-layout">
        <div className="edit-main">
          <section className="card edit-card">
            <h4 className="edit-section-title">{t('edit.basicInfo')}</h4>
            <div className="form-grid">
              <label>
                {t('edit.name')}
                <input value={name} onChange={(e) => setName(e.target.value)} required />
              </label>
              <label>
                {t('edit.brand')}
                <input value={brand} onChange={(e) => setBrand(e.target.value)} />
              </label>
              <div className="form-row-2">
                {/* Unit and size get their own fields: one wrapping label over
                    two controls left the size input unlabeled on the wire. */}
                <Field label={t('labels.measure')}>
                  <UnitSelect
                    value={unit}
                    ariaLabel={t('edit.measureAria')}
                    emptyLabel={t('edit.noMeasure')}
                    onChange={setUnit}
                  />
                </Field>
                <Field label={t('edit.sizeLabel')} hint={t('edit.sizeTitle')}>
                  <input
                    type="text"
                    inputMode="decimal"
                    value={unitValue}
                    onChange={(e) => setUnitValue(e.target.value)}
                    placeholder="500"
                    aria-label={t('edit.sizeAria')}
                  />
                </Field>
              </div>
              <Field label={t('edit.category')}>
                <CategorySelect
                  categories={categories}
                  kind="product"
                  value={categoryId}
                  ariaLabel={t('edit.categoryAria')}
                  onChange={setCategoryId}
                />
              </Field>
              <label>
                {t('edit.standardName')}{' '}
                <span className="item-field-note">{t('edit.rawName', { name: name.trim() || '—' })}</span>
                <input
                  value={standardName}
                  placeholder={t('edit.standardNamePlaceholder')}
                  aria-label={t('edit.standardNameAria')}
                  onChange={(e) => setStandardName(e.target.value)}
                />
              </label>
              <label>
                {t('edit.genericName')}{' '}
                <span className="item-field-note">{t('edit.genericExample')}</span>
                <input
                  value={genericName}
                  placeholder={t('edit.genericPlaceholder')}
                  aria-label={t('edit.genericAria')}
                  onChange={(e) => setGenericName(e.target.value)}
                />
              </label>
              <label>
                {t('edit.description')}
                <textarea
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  rows={4}
                />
              </label>
            </div>
          </section>

          <section className="card edit-card">
            <h4 className="edit-section-title">{t('edit.purchaseData')}</h4>
            <div className="item-field-grid">
              <div className="item-field">
                <span>{t('labels.bestPrice')}</span>
                <strong>
                  {current.best_price_cents !== undefined && current.price_currency
                    ? formatCents(current.best_price_cents, current.price_currency)
                    : '—'}
                </strong>
              </div>
              <div className="item-field">
                <span>{t('labels.lastPurchased')}</span>
                <span>{current.last_purchase_date || '—'}</span>
              </div>
              <div className="item-field">
                <span>{t('labels.timesBought')}</span>
                <strong>{current.times_bought}</strong>
              </div>
            </div>
          </section>

          {formError && <ErrorMessage message={formError} />}
        </div>

        <div className="edit-side">
          <section className="card edit-card">
            <h4 className="edit-section-title">{t('edit.photo')}</h4>
            {current.has_image ? (
              <img
                className="product-photo product-photo-preview"
                src={productsApi.photoUrl(current)}
                alt={t('edit.photoAlt', { name: current.name })}
              />
            ) : (
              <div className="product-photo-placeholder" aria-hidden="true">
                {t('edit.noPhoto')}
              </div>
            )}
            <div className="camera-row">
              <Button variant="secondary" onClick={() => photoInputRef.current?.click()}>
                {current.has_image ? t('edit.replaceImage') : t('edit.uploadImage')}
              </Button>
              {current.has_image && (
                <Button variant="ghost" onClick={() => void handleRemovePhoto()}>
                  {t('edit.remove')}
                </Button>
              )}
            </div>
            <div
              className={`photo-dropzone${dragOver ? ' dragover' : ''}`}
              onDragOver={(e) => {
                e.preventDefault();
                setDragOver(true);
              }}
              onDragLeave={() => setDragOver(false)}
              onDrop={(e) => {
                e.preventDefault();
                setDragOver(false);
                void handleUploadPhoto(e.dataTransfer.files?.[0]);
              }}
            >

              <span>{t('edit.dropzone')}</span>
            </div>
          </section>

          <div className="edit-actions">
            <Button variant="secondary" onClick={onClose}>
              {t('actions.cancel')}
            </Button>
            <Button onClick={handleSaveClick} disabled={busy}>
              {busy ? t('actions.saving') : t('actions.saveChanges')}
            </Button>
          </div>
        </div>
      </div>

      <input
        ref={photoInputRef}
        type="file"
        accept="image/*,.heic,.heif,.tif,.tiff,.bmp,.gif"
        style={{ display: 'none' }}
        onChange={(e) => {
          void handleUploadPhoto(e.target.files?.[0]);
          e.target.value = ''; // allow re-selecting the same file
        }}
      />

      {mergeCheck?.match && (
        <ProductMergeDialog
          check={mergeCheck}
          source={product}
          busy={mergeBusy}
          error={mergeError}
          onKeep={(keep) => void doMerge(keep)}
          onClose={() => {
            setMergeCheck(null);
            setPendingInput(null);
            setMergeError(null);
          }}
        />
      )}
    </Dialog>
  );
}