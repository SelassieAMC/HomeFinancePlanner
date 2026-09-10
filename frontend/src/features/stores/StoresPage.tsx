import { useRef, useState } from 'react';
import { useAsync } from '../../hooks/useAsync';
import { storesApi } from '../../api/stores';
import type { Store, StoreInput } from '../../types/domain';
import { Button, Card, Spinner, ErrorMessage, EmptyState, ItemPanel, ItemPanels } from '../../components/ui';

export function StoresPage() {
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
      setFormError('Store name is required.');
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
      setFormError(err instanceof Error ? err.message : 'Failed to create store.');
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
      setFormError(err instanceof Error ? err.message : 'Failed to save store.');
    }
  }

  async function handleUploadLogo(storeId: number | null, file: File | undefined) {
    if (!file || storeId === null) return;
    setFormError(null);
    try {
      await storesApi.uploadLogo(storeId, file);
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to upload logo.');
    }
  }

  async function handleRemoveLogo(storeId: number) {
    setFormError(null);
    try {
      await storesApi.removeLogo(storeId);
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to remove logo.');
    }
  }

  async function handleDelete(id: number) {
    setFormError(null);
    try {
      await storesApi.remove(id);
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to delete store.');
    }
  }

  if (loading) return <Spinner />;
  if (error) return <ErrorMessage message={error.message} />;

  const stores: Store[] = data ?? [];

  return (
    <div className="page">
      <h2 className="page-title">Stores</h2>

      <Card title="Add store">
        <form className="form-row" onSubmit={handleCreate}>
          <input
            placeholder="Name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
          />
          <input
            placeholder="Description (optional)"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
          <input
            placeholder="Location (optional)"
            value={location}
            onChange={(e) => setLocation(e.target.value)}
          />
          <Button type="submit">Add</Button>
        </form>
        {formError && <ErrorMessage message={formError} />}
      </Card>

      {stores.length === 0 ? (
        <EmptyState message="No stores yet — add your first one above, or scan a bill with a new market name." />
      ) : (
        <ItemPanels>
          {stores.map((store) => (
            <ItemPanel
              key={store.id}
              icon="🏪"
              title={store.name}
              subtitle={store.location || store.description || ''}
              value={`${store.bill_count} ${store.bill_count === 1 ? 'bill' : 'bills'}`}
            >
              {editingId === store.id ? (
                <form className="form-grid" onSubmit={handleSaveEdit}>
                  <label>
                    Name
                    <input
                      value={editName}
                      onChange={(e) => setEditName(e.target.value)}
                      required
                    />
                  </label>
                  <label>
                    Description
                    <input
                      value={editDescription}
                      onChange={(e) => setEditDescription(e.target.value)}
                    />
                  </label>
                  <label>
                    Location
                    <input
                      value={editLocation}
                      onChange={(e) => setEditLocation(e.target.value)}
                    />
                  </label>
                  <div className="camera-row">
                    <Button type="submit">Save</Button>
                    <Button variant="ghost" onClick={() => setEditingId(null)}>
                      Cancel
                    </Button>
                  </div>
                </form>
              ) : (
                <>
                  {store.has_logo && (
                    <img
                      className="store-logo"
                      src={storesApi.logoUrl(store)}
                      alt={`${store.name} logo`}
                    />
                  )}
                  <div className="item-field-grid">
                    <div className="item-field">
                      <span>Description</span>
                      <span>{store.description || '—'}</span>
                    </div>
                    <div className="item-field">
                      <span>Location</span>
                      <span>{store.location || '—'}</span>
                    </div>
                    <div className="item-field">
                      <span>Bills</span>
                      <strong>{store.bill_count}</strong>
                    </div>
                  </div>
                  <div className="camera-row">
                    <Button variant="secondary" onClick={() => startEdit(store)}>
                      ✏️ Edit
                    </Button>
                    <Button
                      variant="secondary"
                      onClick={() => {
                        logoTargetRef.current = store.id;
                        logoInputRef.current?.click();
                      }}
                    >
                      🖼️ {store.has_logo ? 'Replace logo' : 'Upload logo'}
                    </Button>
                    {store.has_logo && (
                      <Button variant="secondary" onClick={() => handleRemoveLogo(store.id)}>
                        Remove logo
                      </Button>
                    )}
                    <Button variant="danger" onClick={() => handleDelete(store.id)}>
                      Delete store
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
        accept="image/jpeg,image/png,image/webp"
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