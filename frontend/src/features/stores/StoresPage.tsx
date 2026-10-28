import { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAsync } from '../../hooks/useAsync';
import { storesApi } from '../../api/stores';
import type { Store, StoreInput } from '../../types/domain';
import { Button, Card, Spinner, ErrorMessage, EmptyState, Field, ItemPanel, ItemPanels } from '../../components/ui';

export function StoresPage() {
  const { t } = useTranslation('stores');
  const { data, loading, error, reload } = useAsync(() => storesApi.list(), []);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [location, setLocation] = useState('');
  const [formError, setFormError] = useState<string | null>(null);

  // Inline edit state (one store at a time).
  const [editingId, setEditingId] = useState<number | null>(null);
  const [editName, setEditName] = useState('');
  const [editDescription, setEditDescription] = useState('');
  const [editLocation, setEditLocation] = useState('');
  const logoInputRef = useRef<HTMLInputElement>(null);
  const logoTargetRef = useRef<number | null>(null);

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    if (!name.trim()) {
      setFormError(t('errors.nameRequired'));
      return;
    }
    const input: StoreInput = { name, description, location };
    try {
      await storesApi.create(input);
      setName('');
      setDescription('');
      setLocation('');
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.create'));
    }
  }

  function startEdit(store: Store) {
    setEditingId(store.id);
    setEditName(store.name);
    setEditDescription(store.description ?? '');
    setEditLocation(store.location ?? '');
  }

  async function handleSaveEdit(e: React.FormEvent) {
    e.preventDefault();
    if (editingId === null) return;
    setFormError(null);
    try {
      await storesApi.update(editingId, {
        name: editName,
        description: editDescription,
        location: editLocation,
      });
      setEditingId(null);
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.save'));
    }
  }

  async function handleUploadLogo(storeId: number | null, file: File | undefined) {
    if (!file || storeId === null) return;
    setFormError(null);
    try {
      await storesApi.uploadLogo(storeId, file);
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.uploadLogo'));
    }
  }

  async function handleRemoveLogo(storeId: number) {
    setFormError(null);
    try {
      await storesApi.removeLogo(storeId);
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.removeLogo'));
    }
  }

  async function handleDelete(id: number) {
    setFormError(null);
    try {
      await storesApi.remove(id);
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.delete'));
    }
  }

  if (loading) return <Spinner />;
  if (error) return <ErrorMessage message={error.message} />;

  const stores: Store[] = data ?? [];

  return (
    <div className="page">
      <h2 className="page-title">{t('title')}</h2>

      <Card title={t('add.title')}>
        <form className="form-row" onSubmit={handleCreate}>
          <Field label={t('labels.name')}>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              aria-label={t('labels.name')}
              required
            />
          </Field>
          <Field label={t('labels.description')}>
            <input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              aria-label={t('labels.description')}
            />
          </Field>
          <Field label={t('labels.location')}>
            <input
              value={location}
              onChange={(e) => setLocation(e.target.value)}
              aria-label={t('labels.location')}
            />
          </Field>
          <Button type="submit">{t('add.submit')}</Button>
        </form>
        {formError && <ErrorMessage message={formError} />}
      </Card>

      {stores.length === 0 ? (
        <EmptyState message={t('list.empty')} />
      ) : (
        <ItemPanels>
          {stores.map((store) => (
            <ItemPanel
              key={store.id}
              title={store.name}
              subtitle={store.location || store.description || ''}
              value={t('list.bills', { count: store.bill_count })}
            >
              {editingId === store.id ? (
                <form className="form-grid" onSubmit={handleSaveEdit}>
                  <Field label={t('labels.name')}>
                    <input
                      value={editName}
                      onChange={(e) => setEditName(e.target.value)}
                      aria-label={t('labels.name')}
                      required
                    />
                  </Field>
                  <Field label={t('labels.description')}>
                    <input
                      value={editDescription}
                      onChange={(e) => setEditDescription(e.target.value)}
                      aria-label={t('labels.description')}
                    />
                  </Field>
                  <Field label={t('labels.location')}>
                    <input
                      value={editLocation}
                      onChange={(e) => setEditLocation(e.target.value)}
                      aria-label={t('labels.location')}
                    />
                  </Field>
                  <div className="camera-row">
                    <Button type="submit">{t('actions.save')}</Button>
                    <Button variant="ghost" onClick={() => setEditingId(null)}>
                      {t('actions.cancel')}
                    </Button>
                  </div>
                </form>
              ) : (
                <>
                {store.has_logo && (
                    <img
                      className="store-logo"
                      src={storesApi.logoUrl(store)}
                      alt={t('list.logoAlt', { name: store.name })}
                    />
                  )}
                  <div className="item-field-grid">
                    <div className="item-field">
                      <span>{t('labels.description')}</span>
                      <span>{store.description || '—'}</span>
                    </div>
                    <div className="item-field">
                      <span>{t('labels.location')}</span>
                      <span>{store.location || '—'}</span>
                    </div>
                    <div className="item-field">
                      <span>{t('labels.bills')}</span>
                      <strong>{store.bill_count}</strong>
                    </div>
                  </div>
                  <div className="camera-row">
                    <Button variant="secondary" onClick={() => startEdit(store)}>
                      {t('actions.edit')}
                    </Button>
                    <Button
                      variant="secondary"
                      onClick={() => {
                        logoTargetRef.current = store.id;
                        logoInputRef.current?.click();
                      }}
                    >
                    {store.has_logo ? t('actions.replaceLogo') : t('actions.uploadLogo')}
                    </Button>
                  {store.has_logo && (
                      <Button variant="secondary" onClick={() => handleRemoveLogo(store.id)}>
                        {t('actions.removeLogo')}
                      </Button>
                    )}
                    <Button variant="danger" onClick={() => handleDelete(store.id)}>
                      {t('actions.delete')}
                    </Button>
                  </div>
                </>
              )}
            </ItemPanel>
          ))}
        </ItemPanels>
      )}

      <input
        ref={logoInputRef}
        type="file"
        accept="image/*,.heic,.heif,.tif,.tiff,.bmp,.gif"
        style={{ display: 'none' }}
        onChange={(e) => {
          const storeId = logoTargetRef.current;
          handleUploadLogo(storeId, e.target.files?.[0]);
          logoTargetRef.current = null;
          e.target.value = ''; // allow re-selecting the same file
        }}
      />
    </div>
  );
}