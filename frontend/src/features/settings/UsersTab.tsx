import { useState } from 'react';
import { usersApi } from '../../api/users';
import { avatarUrl } from '../../api/auth';
import type { User, UserInput } from '../../types/domain';
import { Button, Card, EmptyState, ErrorMessage, ItemPanel, ItemPanels, Spinner } from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';

// Admin-only user management: create household members (they appear in the
// login dropdown immediately), see who exists. Deleting and editing stays
// with the admin once the scheduler-era flows settle — this slice is
// creation + visibility only.
export function UsersTab() {
  const list = useAsync(() => usersApi.list(), []);

  const [username, setUsername] = useState('');
  const [fullName, setFullName] = useState('');
  const [password, setPassword] = useState('');
  const [passwordConfirm, setPasswordConfirm] = useState('');
  const [isAdmin, setIsAdmin] = useState(false);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<string | null>(null);

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
      setError('Username, full name and password are required.');
      return;
    }
    if (password !== passwordConfirm) {
      setError('Passwords do not match.');
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
      setError(err instanceof Error ? err.message : 'Creating the user failed.');
    } finally {
      setCreating(false);
    }
  }

  if (list.loading) return <Spinner />;
  if (list.error) return <ErrorMessage message={list.error.message} />;
  const users: User[] = list.data ?? [];

  return (
    <>
      <p className="hint-text">
        Members appear in the login dropdown and each carry their own password
        and avatar. The household data (accounts, bills, budgets) stays shared
        between everyone — there is one planner, not one per person.
      </p>

      <Card title="Add a family member">
        <form className="profile-pw-form" onSubmit={create}>
          <label>
            Username
            <input
              value={username}
              placeholder="anna"
              maxLength={32}
              disabled={creating}
              onChange={(e) => setUsername(e.target.value)}
            />
          </label>
          <label>
            Full name
            <input
              value={fullName}
              placeholder="Anna Weber"
              maxLength={80}
              disabled={creating}
              onChange={(e) => setFullName(e.target.value)}
            />
          </label>
          <label>
            Password
            <input
              type="password"
              value={password}
              autoComplete="new-password"
              disabled={creating}
              onChange={(e) => setPassword(e.target.value)}
            />
          </label>
          <label>
            Repeat password
            <input
              type="password"
              value={passwordConfirm}
              autoComplete="new-password"
              disabled={creating}
              onChange={(e) => setPasswordConfirm(e.target.value)}
            />
          </label>
          <label className="default-radio">
            Administrator
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
            <span className="form-error-inline">Passwords do not match.</span>
          ) : null}
          {error && <ErrorMessage message={error} />}
          {created && <div className="hint-banner">User {created} created ✓</div>}
          <div className="camera-row">
            <Button type="submit" disabled={creating}>
              {creating ? 'Creating…' : 'Create user'}
            </Button>
          </div>
        </form>
      </Card>

      <h3 className="section-heading">Members</h3>
      {users.length === 0 ? (
        <EmptyState message="No users yet." />
      ) : (
        <ItemPanels>
          {users.map((u) => (
            <ItemPanel
              key={u.id}
              title={u.full_name || u.username}
              subtitle={
                <>
                  @{u.username}
                  {u.is_admin && <span className="default-badge"> admin</span>}
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
                <span className="hint-inline">Created {new Date(u.created_at).toLocaleDateString()}</span>
              </div>
            </ItemPanel>
          ))}
        </ItemPanels>
      )}
    </>
  );
}