import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { usersApi } from '../../api/users';
import { avatarUrl } from '../../api/auth';
import type { User, UserInput, UserUpdateInput } from '../../types/domain';
import { Button, Card, EmptyState, ErrorMessage, HelpNote, ItemPanel, ItemPanels, Spinner } from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { useAuth } from '../auth/AuthContext';

// Admin-only user management: create household members (they appear in the
// login dropdown immediately), edit names/flags, reset passwords, delete.
// Deleting your own account and removing the last administrator are refused
// server-side too. The household data (accounts, bills, budgets) stays shared
// between everyone — one planner, not one per person.
export function UsersTab() {
  const { t } = useTranslation('settings');
  const { user: me } = useAuth();
  const list = useAsync(() => usersApi.list(), []);

  const [username, setUsername] = useState('');
  const [fullName, setFullName] = useState('');
  const [password, setPassword] = useState('');
  const [passwordConfirm, setPasswordConfirm] = useState('');
  const [isAdmin, setIsAdmin] = useState(false);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<string | null>(null);

  // Per-member editing state (one member open at a time).
  const [editingId, setEditingId] = useState<number | null>(null);
  const [deletingId, setDeletingId] = useState<number | null>(null);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setCreated(null);
    const input: UserInput = {
      username: username.trim(),
      full_name: fullName.trim(),
      password,
      is_admin: isAdmin,
    };
    if (!input.username || !password || !input.full_name) {
      setError(t('users.requiredFields'));
      return;
    }
    if (password !== passwordConfirm) {
      setError(t('users.pwMismatch'));
      return;
    }
    setCreating(true);
    try {
      const u = await usersApi.create(input);
      setCreated(u.username);
      setUsername('');
      setFullName('');
      setPassword('');
      setPasswordConfirm('');
      setIsAdmin(false);
      list.reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : t('users.createError'));
    } finally {
      setCreating(false);
    }
  }

  function done() {
    setEditingId(null);
    setDeletingId(null);
    list.reload();
  }

  if (list.loading) return <Spinner />;
  if (list.error) return <ErrorMessage message={list.error.message} />;
  const users: User[] = list.data ?? [];

  return (
    <>
      <HelpNote note={t('users.hint')} />

      <Card title={t('users.addTitle')}>
        <form className="profile-pw-form" onSubmit={create}>
          <label>
            {t('users.username')}
            <input
              value={username}
              placeholder={t('users.usernamePlaceholder')}
              maxLength={32}
              disabled={creating}
              onChange={(e) => setUsername(e.target.value)}
            />
          </label>
          <label>
            {t('users.fullName')}
            <input
              value={fullName}
              placeholder={t('users.fullNamePlaceholder')}
              maxLength={80}
              disabled={creating}
              onChange={(e) => setFullName(e.target.value)}
            />
          </label>
          <label>
            {t('users.password')}
            <input
              type="password"
              value={password}
              autoComplete="new-password"
              disabled={creating}
              onChange={(e) => setPassword(e.target.value)}
            />
          </label>
          <label>
            {t('users.repeatPassword')}
            <input
              type="password"
              value={passwordConfirm}
              autoComplete="new-password"
              disabled={creating}
              onChange={(e) => setPasswordConfirm(e.target.value)}
            />
          </label>
          <label className="default-radio">
            {t('users.administrator')}
            <input
              type="checkbox"
              checked={isAdmin}
              disabled={creating}
              onChange={(e) => setIsAdmin(e.target.checked)}
            />
          </label>
          {password.length > 0 &&
            passwordConfirm.length > 0 &&
            password !== passwordConfirm ? (
            <span className="form-error-inline">{t('users.pwMismatch')}</span>
          ) : null}
          {error && <ErrorMessage message={error} />}
          {created && (
            <div className="hint-banner">{t('users.created', { name: created })}</div>
          )}
          <div className="camera-row">
            <Button type="submit" disabled={creating}>
              {creating ? t('users.creating') : t('users.createUser')}
            </Button>
          </div>
        </form>
      </Card>

      <h3 className="section-heading">{t('users.members')}</h3>
      {users.length === 0 ? (
        <EmptyState message={t('users.empty')} />
      ) : (
        <ItemPanels>
          {users.map((u) =>
            editingId === u.id ? (
              <EditMemberForm key={`edit-${u.id}`} user={u} onDone={done} />
            ) : (
              <ItemPanel
                key={u.id}
                title={u.full_name || u.username}
                subtitle={
                  <>
                    @{u.username}
                    {u.is_admin && (
                      <span className="default-badge"> {t('users.adminBadge')}</span>
                    )}
                    {me?.id === u.id && (
                      <span className="hint-inline"> {t('users.youBadge')}</span>
                    )}
                  </>
                }
              >
                <div className="user-row">
                  {u.has_photo ? (
                    <img
                      className="user-row-photo"
                      src={`${avatarUrl(u.id)}?v=${encodeURIComponent(u.updated_at)}`}
                      alt=""
                    />
                  ) : (
                    <div className="user-row-photo user-photo-empty">
                      {(u.full_name || u.username).charAt(0).toUpperCase()}
                    </div>
                  )}
                  <span className="hint-inline">
                    {t('users.createdOn', { date: new Date(u.created_at).toLocaleDateString() })}
                  </span>
                </div>
                <div className="camera-row">
                  <Button variant="secondary" onClick={() => setEditingId(u.id)}>
                    {t('users.edit')}
                  </Button>
                  {me?.id !== u.id && (
                    deletingId === u.id ? (
                      <DeleteConfirm
                        user={u}
                        onCancel={() => setDeletingId(null)}
                        onConfirmed={done}
                      />
                    ) : (
                      <Button variant="danger" onClick={() => setDeletingId(u.id)}>
                        {t('users.delete')}
                      </Button>
                    )
                  )}
                </div>
              </ItemPanel>
            ),
          )}
        </ItemPanels>
      )}
    </>
  );
}

// EditMemberForm swaps in for a member's panel: names/flags plus an optional
// password reset. Empty password fields keep the current password.
function EditMemberForm({ user, onDone }: { user: User; onDone: () => void }) {
  const { t } = useTranslation('settings');
  const { user: me } = useAuth();
  const [username, setUsername] = useState(user.username);
  const [fullName, setFullName] = useState(user.full_name);
  const [isAdmin, setIsAdmin] = useState(user.is_admin);
  const [password, setPassword] = useState('');
  const [passwordConfirm, setPasswordConfirm] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (password !== passwordConfirm) {
      setError(t('users.pwMismatch'));
      return;
    }
    const input: UserUpdateInput = {
      username: username.trim(),
      full_name: fullName.trim(),
      is_admin: isAdmin,
      ...(password ? { password } : {}),
    };
    setSaving(true);
    try {
      await usersApi.update(user.id, input);
      onDone();
    } catch (err) {
      setError(err instanceof Error ? err.message : t('usersEdit.saveError'));
      setSaving(false);
    }
  }

  const selfDemote = me?.id === user.id && user.is_admin && !isAdmin;

  return (
    <ItemPanel
      key={`editing-${user.id}`}
      title={t('usersEdit.editTitle', { name: user.full_name || user.username })}
      subtitle={<>&nbsp;</>}
    >
      <form className="profile-pw-form" onSubmit={save}>
        <label>
          {t('users.username')}
          <input
            value={username}
            maxLength={32}
            disabled={saving}
            onChange={(e) => setUsername(e.target.value)}
          />
        </label>
        <label>
          {t('users.fullName')}
          <input
            value={fullName}
            maxLength={80}
            disabled={saving}
            onChange={(e) => setFullName(e.target.value)}
          />
        </label>
        <label className="default-radio">
          {t('users.administrator')}
          <input
            type="checkbox"
            checked={isAdmin}
            disabled={saving || (me?.id === user.id && user.is_admin)}
            onChange={(e) => setIsAdmin(e.target.checked)}
          />
        </label>
        {me?.id === user.id && user.is_admin && (
          <span className="hint-inline">{t('usersEdit.selfDemoteNote')}</span>
        )}
        <label>
          {t('usersEdit.newPassword')}{' '}
          <span className="hint-inline">{t('usersEdit.keepCurrent')}</span>
          <input
            type="password"
            value={password}
            autoComplete="new-password"
            disabled={saving}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        <label>
          {t('usersEdit.repeatNewPassword')}
          <input
            type="password"
            value={passwordConfirm}
            autoComplete="new-password"
            disabled={saving}
            onChange={(e) => setPasswordConfirm(e.target.value)}
          />
        </label>
        {password.length > 0 &&
          passwordConfirm.length > 0 &&
          password !== passwordConfirm && (
            <span className="form-error-inline">{t('users.pwMismatch')}</span>
          )}
        {selfDemote && (
          <span className="form-error-inline">{t('usersEdit.selfDemoteError')}</span>
        )}
        {error && <ErrorMessage message={error} />}
        <div className="camera-row">
          <Button onClick={() => onDone()} variant="secondary" disabled={saving}>
            {t('usersEdit.cancel')}
          </Button>
          <Button type="submit" disabled={saving || selfDemote}>
            {saving ? t('usersEdit.saving') : t('usersEdit.save')}
          </Button>
        </div>
      </form>
    </ItemPanel>
  );
}

// DeleteConfirm replaces the Delete button with a two-step inline confirm.
function DeleteConfirm({
  user,
  onCancel,
  onConfirmed,
}: {
  user: User;
  onCancel: () => void;
  onConfirmed: () => void;
}) {
  const { t } = useTranslation('settings');
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function confirm() {
    setError(null);
    setDeleting(true);
    try {
      await usersApi.delete(user.id);
      onConfirmed();
    } catch (err) {
      setError(err instanceof Error ? err.message : t('usersDelete.deleteError'));
      setDeleting(false);
    }
  }

  return (
    <div className="delete-confirm-row">
      <span className="hint-inline">
        {t('usersDelete.confirmPrompt', { name: user.username })}
      </span>
      <Button variant="secondary" onClick={onCancel} disabled={deleting}>
        {t('usersDelete.no')}
      </Button>
      <Button variant="danger" onClick={confirm} disabled={deleting}>
        {deleting ? t('usersDelete.deleting') : t('usersDelete.yesDelete')}
      </Button>
      {error && <ErrorMessage message={error} />}
    </div>
  );
}