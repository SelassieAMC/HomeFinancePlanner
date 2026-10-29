import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { productsApi } from '../../api/products';
import type { ProductGroup } from '../../types/domain';
import { Button, Dialog, ErrorMessage } from '../../components/ui';

interface ProductGroupEditModalProps {
  group: ProductGroup;
  /** Discard changes and close the modal. */
  onClose: () => void;
  /** Save succeeded — reload the list and close. */
  onSaved: () => void;
}

/**
 * Edit one generic-product family. The only editable thing here is the family
 * name itself — which products belong to the group is decided by the
 * normalization mappings, never here. Saving writes the new generic name to
 * every member's mapping (source user); the grouping recomputes on reload.
 * Members with no mapping yet record an identity standard name beside the new
 * family, exactly like the per-product editor does.
 */
export function ProductGroupEditModal({ group, onClose, onSaved }: ProductGroupEditModalProps) {
  const { t } = useTranslation('products');
  const [genericName, setGenericName] = useState(group.generic_name);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function handleSave() {
    const value = genericName.trim();
    if (!value) {
      setError(t('errors.genericRequired'));
      return;
    }
    if (value === group.generic_name) {
      onSaved();
      return;
    }
    setBusy(true);
    setError(null);
    try {
      // The update endpoint rewrites the products row, so every member field
      // is sent back unchanged — only the generic name is the new decision.
      // standard_name resends the remembered value (raw name when unmapped),
      // which keeps reviewed decisions intact.
      for (const p of group.items) {
        await productsApi.update(p.id, {
          name: p.name,
          brand: p.brand ?? '',
          unit: p.unit ?? '',
          category_id: p.category_id ?? null,
          description: p.description ?? '',
          standard_name: p.standard_name || p.name,
          generic_name: value,
        });
      }
      onSaved();
    } catch (err) {
      setError(err instanceof Error ? err.message : t('errors.saveGeneric'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog title={t('group.title')} onClose={onClose}>
      <div className="form-grid">
        <label>
          {t('group.name')}
          <input
            value={genericName}
            placeholder={t('group.placeholder')}
            aria-label={t('group.nameAria')}
            onChange={(e) => setGenericName(e.target.value)}
            autoFocus
          />
        </label>
        <p className="item-field-note">
          {t('group.appliesNote', { count: group.items.length })}
        </p>
      </div>

      {error && <ErrorMessage message={error} />}

      <div className="dialog-actions">
        <Button variant="secondary" onClick={onClose}>
          {t('actions.cancel')}
        </Button>
        <Button onClick={() => void handleSave()} disabled={busy}>
          {busy ? t('actions.saving') : t('actions.saveChanges')}
        </Button>
      </div>
    </Dialog>
  );
}