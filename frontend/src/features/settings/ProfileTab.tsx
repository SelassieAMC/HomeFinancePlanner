import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import i18n from '../../i18n';
import { LOCALE_LABELS, SUPPORTED_LOCALES } from '../../i18n/core';
import { authApi, avatarUrl } from '../../api/auth';
import { useAuth } from '../auth/AuthContext';
import { Button, Card, ErrorMessage, Field } from '../../components/ui';
import { useTheme } from '../../hooks/useTheme';
import type { ThemeChoice } from '../../lib/theme';

// Own-account profile (settings page, any family member): the UI language,
// the avatar photo, the full name shown in the login dropdown and the header,
// and the password change with a confirm-field double-check.
export function ProfileTab() {
  const { t } = useTranslation('settings');
  const { user, setUser, reload } = useAuth();
  const { choice: themeChoice, setChoice: setThemeChoice } = useTheme();
  const [localeError, setLocaleError] = useState<string | null>(null);
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

  // Switch first, persist second; on failure put the language back.
  async function pickLocale(lng: string) {
    const previous = i18n.language;
    setLocaleError(null);
    if (lng !== i18n.language) {
      void i18n.changeLanguage(lng);
    }
    try {
      setUser(await authApi.updateLocale(lng));
    } catch (err) {
      void i18n.changeLanguage(previous);
      setLocaleError(
        err instanceof Error ? err.message : t('errors.saveLocale'),
      );
    }
  }

  async function saveName(e: React.FormEvent) {
    e.preventDefault();
    setNameSaving(true);
    setNameError(null);
    setNameSaved(false);
    try {
      setUser(await authApi.updateFullName(fullName));
      setNameSaved(true);
    } catch (err) {
      setNameError(err instanceof Error ? err.message : t('errors.saveName'));
    } finally {
      setNameSaving(false);
    }
  }

  async function savePassword(e: React.FormEvent) {
    e.preventDefault();
    setPwError(null);
    setPwSaved(false);
    if (next !== confirm) {
      setPwError(t('profile.pwMismatch'));
      return;
    }
    if (next.length < 4) {
      setPwError(t('errors.passwordShort'));
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
      setPwError(err instanceof Error ? err.message : t('errors.savePassword'));
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
      setPhotoError(err instanceof Error ? err.message : t('errors.uploadPhoto'));
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
      setPhotoError(err instanceof Error ? err.message : t('errors.removePhoto'));
    } finally {
      setPhotoBusy(false);
    }
  }

  return (
    <>
      <Card title={t('theme.title')}>
        <Field label={t('theme.label')} hint={t('theme.hint')}>
          <select
            value={themeChoice}
            onChange={(e) => setThemeChoice(e.target.value as ThemeChoice)}
          >
            <option value="system">{t('theme.system')}</option>
            <option value="light">{t('theme.light')}</option>
            <option value="dark">{t('theme.dark')}</option>
          </select>
        </Field>
      </Card>

      <Card title={t('language.title')}>
        <Field
          label={t('language.label')}
          hint={t('language.hint')}
          error={localeError}
        >
          <select
            value={i18n.language}
            aria-label={t('language.label')}
            onChange={(e) => {
              setLocaleError(null);
              void pickLocale(e.target.value);
            }}
          >
            {SUPPORTED_LOCALES.map((lng) => (
              <option key={lng} value={lng}>
                {LOCALE_LABELS[lng]}
              </option>
            ))}
          </select>
        </Field>
      </Card>

      <Card title={t('profile.photoTitle')} help={t('profile.photoHint')}>
        <div className="profile-photo-row">
          {user.has_photo ? (
            <img
              className="profile-photo"
              src={`${avatarUrl(user.id)}?v=${encodeURIComponent(user.updated_at)}`}
              alt={t('profile.photoAlt')}
            />
          ) : (
            <div className="profile-photo profile-photo-empty">{t('profile.photoEmpty')}</div>
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
              {photoBusy ? t('profile.working') : user.has_photo ? t('profile.replacePhoto') : t('profile.choosePhoto')}
            </Button>
            {user.has_photo && (
              <Button variant="danger" disabled={photoBusy} onClick={() => void removePhoto()}>
                {t('profile.remove')}
              </Button>
            )}
          </div>
        </div>
        {photoError && <ErrorMessage message={photoError} />}
      </Card>

      <Card title={t('profile.nameTitle')}>
        <form
          className="form-row"
          onSubmit={(e) => {
            e.preventDefault();
            void saveName(e);
          }}
        >
          <Field label={t('profile.namePlaceholder')} className="profile-name-field">
            <input
              id="profile-full-name"
              value={fullName}
              placeholder={t('profile.namePlaceholder')}
              maxLength={80}
              disabled={nameSaving}
              onChange={(e) => {
                setFullName(e.target.value);
                setNameSaved(false);
              }}
            />
          </Field>
          <Button type="submit" disabled={nameSaving}>
            {nameSaving ? t('profile.saving') : t('profile.save')}
          </Button>
        </form>
        {nameError && <ErrorMessage message={nameError} />}
        {nameSaved && <div className="hint-banner">{t('profile.saved')}</div>}
      </Card>

      <Card title={t('profile.pwTitle')} help={t('profile.pwHint')}>
        <form className="profile-pw-form" onSubmit={savePassword}>
          <label>
            {t('profile.pwCurrent')}
            <input
              type="password"
              value={current}
              autoComplete="current-password"
              disabled={pwSaving}
              onChange={(e) => setCurrent(e.target.value)}
            />
          </label>
          <label>
            {t('profile.pwNew')}
            <input
              type="password"
              value={next}
              autoComplete="new-password"
              disabled={pwSaving}
              onChange={(e) => setNext(e.target.value)}
            />
          </label>
          <label>
            {t('profile.pwRepeat')}
            <input
              type="password"
              value={confirm}
              autoComplete="new-password"
              disabled={pwSaving}
              onChange={(e) => setConfirm(e.target.value)}
            />
          </label>
          {next.length > 0 && confirm.length > 0 && next !== confirm ? (
            <span className="form-error-inline">{t('profile.pwMismatch')}</span>
          ) : null}
          {pwError && <ErrorMessage message={pwError} />}
          {pwSaved && <div className="hint-banner">{t('profile.pwSaved')}</div>}
          <div className="camera-row">
            <Button type="submit" disabled={pwSaving || next !== confirm || !current}>
              {pwSaving ? t('profile.saving') : t('profile.pwSubmit')}
            </Button>
          </div>
        </form>
      </Card>
    </>
  );
}