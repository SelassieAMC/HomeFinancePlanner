import { useEffect, useRef, useState } from 'react';
import { authApi, avatarUrl } from '../../api/auth';
import { useAuth } from '../auth/AuthContext';
import { Button, Card, ErrorMessage } from '../../components/ui';

// Own-account profile (settings page, any family member): the avatar photo,
// the full name shown in the login dropdown and the header, and the password
// change with a confirm-field double-check.
export function ProfileTab() {
  const { user, setUser, reload } = useAuth();
  const [fullName, setFullName] = useState('');
  const [nameSaved, setNameSaved] = useState(false);
  const [nameError, setNameError] = useState<string | null>(null);
  const [nameSaving, setNameSaving] = useState(false);

  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [pwError, setPwError] = useState<string | null>(null);
  const [pwSaved, setPwSaved] = useState(false);
  const [pwSaving, setPwSaving] = useState(false);

  const [photoError, setPhotoError] = useState<string | null>(null);
  const [photoBusy, setPhotoBusy] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    setFullName(user?.full_name ?? '');
  }, [user?.full_name]);

  if (!user) return null;

  async function saveName(e: React.FormEvent) {
    e.preventDefault();
    setNameSaving(true);
    setNameError(null);
    setNameSaved(false);
    try {
      setUser(await authApi.updateFullName(fullName));
      setNameSaved(true);
    } catch (err) {
      setNameError(err instanceof Error ? err.message : 'Failed to save.');
    } finally {
      setNameSaving(false);
    }
  }

  async function savePassword(e: React.FormEvent) {
    e.preventDefault();
    setPwError(null);
    setPwSaved(false);
    if (next !== confirm) {
      setPwError('New passwords do not match.');
      return;
    }
    if (next.length < 4) {
      setPwError('New password must be at least 4 characters.');
      return;
    }
    setPwSaving(true);
    try {
      await authApi.changePassword(current, next);
      setPwSaved(true);
      setCurrent('');
      setNext('');
      setConfirm('');
    } catch (err) {
      setPwError(err instanceof Error ? err.message : 'Failed to change the password.');
    } finally {
      setPwSaving(false);
    }
  }

  async function pickFile(file: File) {
    setPhotoError(null);
    setPhotoBusy(true);
    try {
      setUser(await authApi.uploadPhoto(file));
      reload();
    } catch (err) {
      setPhotoError(err instanceof Error ? err.message : 'Photo upload failed.');
    } finally {
      setPhotoBusy(false);
    }
  }

  async function removePhoto() {
    setPhotoError(null);
    setPhotoBusy(true);
    try {
      setUser(await authApi.removePhoto());
      reload();
    } catch (err) {
      setPhotoError(err instanceof Error ? err.message : 'Removing the photo failed.');
    } finally {
      setPhotoBusy(false);
    }
  }

  return (
    <>
      <Card title="Profile photo">
        <div className="profile-photo-row">
          {user.has_photo ? (
            <img
              className="profile-photo"
              src={`${avatarUrl(user.id)}?v=${encodeURIComponent(user.updated_at)}`}
              alt="Profile photo"
            />
          ) : (
            <div className="profile-photo profile-photo-empty">?</div>
          )}
          <div className="camera-row">
            <input
              ref={fileRef}
              type="file"
              accept="image/*"
              hidden
              onChange={(e) => {
                const f = e.target.files?.[0];
                e.target.value = '';
                if (f) void pickFile(f);
              }}
            />
            <Button variant="secondary" disabled={photoBusy} onClick={() => fileRef.current?.click()}>
              {photoBusy ? 'Working…' : user.has_photo ? 'Replace photo' : 'Choose photo'}
            </Button>
            {user.has_photo && (
              <Button variant="danger" disabled={photoBusy} onClick={() => void removePhoto()}>
                Remove
              </Button>
            )}
          </div>
        </div>
        <p className="hint-text">
          Small photo shown at the top right of the page and next to the name in
          the login list. Any image works — heavy phone formats (HEIC, TIFF)
          are converted to a small JPEG automatically.
        </p>
        {photoError && <ErrorMessage message={photoError} />}
      </Card>

      <Card title="Full name">
        <form
          className="form-row"
          onSubmit={(e) => {
            e.preventDefault();
            void saveName(e);
          }}
        >
          <input
            value={fullName}
            placeholder="e.g. Anna Weber"
            maxLength={80}
            disabled={nameSaving}
            onChange={(e) => {
              setFullName(e.target.value);
              setNameSaved(false);
            }}
          />
          <Button type="submit" disabled={nameSaving}>
            {nameSaving ? 'Saving…' : 'Save'}
          </Button>
        </form>
        {nameError && <ErrorMessage message={nameError} />}
        {nameSaved && <div className="hint-banner">Saved ✓</div>}
      </Card>

      <Card title="Change password">
        <form className="profile-pw-form" onSubmit={savePassword}>
          <label>
            Current password
            <input
              type="password"
              value={current}
              autoComplete="current-password"
              disabled={pwSaving}
              onChange={(e) => setCurrent(e.target.value)}
            />
          </label>
          <label>
            New password
            <input
              type="password"
              value={next}
              autoComplete="new-password"
              disabled={pwSaving}
              onChange={(e) => setNext(e.target.value)}
            />
          </label>
          <label>
            Repeat new password
            <input
              type="password"
              value={confirm}
              autoComplete="new-password"
              disabled={pwSaving}
              onChange={(e) => setConfirm(e.target.value)}
            />
          </label>
          {next.length > 0 && confirm.length > 0 && next !== confirm ? (
            <span className="form-error-inline">New passwords do not match.</span>
          ) : null}
          {pwError && <ErrorMessage message={pwError} />}
          {pwSaved && <div className="hint-banner">Password changed ✓</div>}
          <div className="camera-row">
            <Button type="submit" disabled={pwSaving || next !== confirm || !current}>
              {pwSaving ? 'Saving…' : 'Change password'}
            </Button>
          </div>
          <p className="hint-text">
            Changing the password signs this user out on other devices; this
            device stays signed in.
          </p>
        </form>
      </Card>
    </>
  );
}