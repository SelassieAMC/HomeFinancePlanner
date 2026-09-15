import { useRef, useState } from 'react';
import { productsApi } from '../../api/products';
import { formatCents } from '../../lib/money';
import type { Category, Product, ProductInput, ProductMergeCheck } from '../../types/domain';
import { ProductMergeDialog } from './ProductMergeDialog';
import {
  Button,
  CategorySelect,
  Dialog,
  ErrorMessage,
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
 * actions on the right. Saving rewrites name/measure/category on every
 * historical bill and transaction lines too — a confirmation dialog warns
 * first.
 */
export function ProductEditModal({ product, categories, onClose, onSaved }: ProductEditModalProps) {
  const [name, setName] = useState(product.name);
  const [brand, setBrand] = useState(product.brand ?? '');
  const [unit, setUnit] = useState(product.unit ?? '');
  const [categoryId, setCategoryId] = useState<number | null>(product.category_id ?? null);
  const [description, setDescription] = useState(product.description ?? '');
  // Photo edits return a fresh product (new updated_at → cache-busted URL).
  const [current, setCurrent] = useState(product);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirmPropagate, setConfirmPropagate] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  // Rename matched an existing product: the merge plan awaiting confirmation.
  const [mergeCheck, setMergeCheck] = useState<ProductMergeCheck | null>(null);
  const [pendingInput, setPendingInput] = useState<ProductInput | null>(null);
  const [mergeBusy, setMergeBusy] = useState(false);
  const [mergeError, setMergeError] = useState<string | null>(null);
  const photoInputRef = useRef<HTMLInputElement>(null);

  /** True when the edit rewrites a field that propagates to bill items and
   *  manually recorded transaction items. */
  function touchesBillItems(): boolean {
    return (
      name.trim() !== product.name ||
      unit.trim().toLowerCase() !== (product.unit ?? '') ||
      (categoryId ?? null) !== (product.category_id ?? null)
    );
  }

  function buildInput(): ProductInput {
    return {
      name: name.trim(),
      brand: brand.trim(),
      unit: unit.trim().toLowerCase(),
      category_id: categoryId,
      description: description.trim(),
    };
  }

  async function doSave(input: ProductInput) {
    setBusy(true);
    setFormError(null);
    try {
      await productsApi.update(product.id, input);
      onSaved();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to save product.');
    } finally {
      setBusy(false);
      setConfirmPropagate(false);
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
      setMergeError(err instanceof Error ? err.message : 'Failed to merge products.');
    } finally {
      setMergeBusy(false);
    }
  }

  async function handleSaveClick() {
    if (!name.trim()) {
      setFormError('Product name is required.');
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
            setFormError(
              `A product named “${check.match.name}” already exists and cannot be merged. Choose a different name.`,
            );
            return;
          }
          setPendingInput(input);
          setMergeCheck(check);
          return;
        }
      } catch (err) {
        setFormError(err instanceof Error ? err.message : 'Failed to check for a matching product.');
        return;
      } finally {
        setBusy(false);
      }
    }
    if (touchesBillItems()) {
      // Historical bill and transaction lines are rewritten with the
      // product — warn first.
      setConfirmPropagate(true);
    } else {
      void doSave(input);
    }
  }

  async function handleUploadPhoto(file: File | null | undefined) {
    if (!file) return;
    setFormError(null);
    try {
      setCurrent(await productsApi.uploadPhoto(product.id, file));
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to upload photo.');
    }
  }

  async function handleRemovePhoto() {
    setFormError(null);
    try {
      setCurrent(await productsApi.removePhoto(product.id));
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to remove photo.');
    }
  }

  return (
    <Dialog title="Edit product" wide>
      <div className="edit-layout">
        <div className="edit-main">
          <section className="card edit-card">
            <h4 className="edit-section-title">Basic information</h4>
            <div className="form-grid">
              <label>
                Product name
                <input value={name} onChange={(e) => setName(e.target.value)} required />
              </label>
              <label>
                Brand
                <input value={brand} onChange={(e) => setBrand(e.target.value)} />
              </label>
              <div className="form-row-2">
                <label>
                  Measure
                  <UnitSelect
                    value={unit}
                    ariaLabel="Product measure"
                    emptyLabel="No measure"
                    onChange={setUnit}
                  />
                </label>
                <label>
                  Category
                  <CategorySelect
                    categories={categories}
                    kind="product"
                    value={categoryId}
                    ariaLabel="Product category"
                    onChange={setCategoryId}
                  />
                </label>
              </div>
              <label>
                Description
                <textarea
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  rows={4}
                />
              </label>
            </div>
          </section>

          <section className="card edit-card">
            <h4 className="edit-section-title">Purchase data</h4>
            <div className="item-field-grid">
              <div className="item-field">
                <span>Best price</span>
                <strong>
                  {current.best_price_cents !== undefined && current.price_currency
                    ? formatCents(current.best_price_cents, current.price_currency)
                    : '—'}
                </strong>
              </div>
              <div className="item-field">
                <span>Last purchased</span>
                <span>{current.last_purchase_date || '—'}</span>
              </div>
              <div className="item-field">
                <span>Times bought</span>
                <strong>{current.times_bought}</strong>
              </div>
            </div>
          </section>

          {formError && <ErrorMessage message={formError} />}
        </div>

        <div className="edit-side">
          <section className="card edit-card">
            <h4 className="edit-section-title">Product photo</h4>
            {current.has_image ? (
              <img
                className="product-photo product-photo-preview"
                src={productsApi.photoUrl(current)}
                alt={`${current.name} photo`}
              />
            ) : (
              <div className="product-photo-placeholder" aria-hidden="true">
                No photo yet
              </div>
            )}
            <div className="camera-row">
              <Button variant="secondary" onClick={() => photoInputRef.current?.click()}>
                {current.has_image ? 'Replace image' : 'Upload image'}
              </Button>
              {current.has_image && (
                <Button variant="ghost" onClick={() => void handleRemovePhoto()}>
                  Remove
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
              <span aria-hidden="true">⬆️</span>
              <span>Or drag &amp; drop a photo here</span>
            </div>
          </section>

          <div className="edit-actions">
            <Button variant="secondary" onClick={onClose}>
              Cancel
            </Button>
            <Button onClick={handleSaveClick} disabled={busy}>
              {busy ? 'Saving…' : 'Save changes'}
            </Button>
          </div>
        </div>
      </div>

      <input
        ref={photoInputRef}
        type="file"
        accept="image/jpeg,image/png,image/webp"
        style={{ display: 'none' }}
        onChange={(e) => {
          void handleUploadPhoto(e.target.files?.[0]);
          e.target.value = ''; // allow re-selecting the same file
        }}
      />

      {confirmPropagate && (
        <Dialog title="Update product">
          <p>
            This will also update{' '}
            <strong>
              {current.times_bought}{' '}
              {current.times_bought === 1 ? 'purchase line' : 'purchase lines'}
            </strong>{' '}
            on past bills and manual transactions (name, measure and category
            are rewritten on every linked line).
          </p>
          <div className="camera-row">
            <Button onClick={() => void doSave(buildInput())}>Update product</Button>
            <Button variant="ghost" onClick={() => setConfirmPropagate(false)}>
              Cancel
            </Button>
          </div>
        </Dialog>
      )}

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