import { useEffect, useState } from 'react';
import { Navigate } from 'react-router-dom';
import { authApi } from '../../api/auth';
import type { LoginUser } from '../../types/domain';
import { Spinner } from '../../components/ui';
import { useAuth } from './AuthContext';

// The login gate: pick a user from the family list, type the password.
// No username field — usernames are household names, not secrets, so the
// selection is a dropdown the admin-created users fill from the database.
// The "keep me logged in" check shapes the cookie server-side: unchecked =
// browser-session (12h), checked = 30 days without being asked again.
export function LoginPage() {
  const { user, loading, setUser } = useAuth();
  const usersState = useLoginUsers(user !== null);
  const [userId, setUserId] = useState<number | ''>('');
  const [password, setPassword] = useState('');
  const [keepLoggedIn, setKeepLoggedIn] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Default the dropdown to the first user once the list lands.
  useEffect(() => {
    const first = usersState.data?.[0];
    if (userId === '' && first) {
      setUserId(first.id);
    }
  }, [usersState.data, userId]);

  if (loading) return <LoginShell>{<Spinner />}</LoginShell>;
  if (user) return <Navigate to="/" replace />;

  if (usersState.loading && !usersState.data) {
    return <LoginShell>{<Spinner />}</LoginShell>;
  }
  if (usersState.error) {
    return (
      <LoginShell>
        <p className="login-error">Could not load users: {usersState.error.message}</p>
      </LoginShell>
    );
  }
  const users: LoginUser[] = usersState.data ?? [];

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (typeof userId !== 'number' || password === '') return;
    setSubmitting(true);
    setError(null);
    try {
      setUser(await authApi.login(userId, password, keepLoggedIn));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed.');
      setPassword('');
      setSubmitting(false);
    }
  }

  return (
    <LoginShell>
      <form className="login-card card" onSubmit={handleSubmit}>
        <h2 className="login-title">Home Finance Planner</h2>
        <p className="login-subtitle">Who is planning?</p>

        <label>
          User
          <select
            className="login-user-select"
            value={userId}
            onChange={(e) => setUserId(Number(e.target.value))}
            autoFocus
          >
            {users.map((u) => (
              <option key={u.id} value={u.id}>
                {u.full_name || u.username}
              </option>
            ))}
          </select>
        </label>

        <label>
          Password
          <input
            type="password"
            value={password}
            autoComplete="current-password"
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>

        <label className="login-keep">
          <input
            type="checkbox"
            checked={keepLoggedIn}
            onChange={(e) => setKeepLoggedIn(e.target.checked)}
          />
          Keep me logged in
        </label>

        {error && <p className="login-error">{error}</p>}

        <button
          type="submit"
          className="btn login-submit"
          disabled={submitting || typeof userId !== 'number'}
        >
          {submitting ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </LoginShell>
  );
}

// useLoginUsers fetches the dropdown identities; skipped once we are already
// signed in (the me() check decides the page anyway).
function useLoginUsers(skip: boolean) {
  const [state, setState] = useState<{
    data: LoginUser[] | null;
    loading: boolean;
    error: Error | null;
  }>({ data: null, loading: !skip, error: null });

  useEffect(() => {
    if (skip) return;
    let cancelled = false;
    authApi
      .loginUsers()
      .then((users) => {
        if (!cancelled) setState({ data: users, loading: false, error: null });
      })
      .catch((err: unknown) => {
        if (!cancelled)
          setState({
            data: null,
            loading: false,
            error: err instanceof Error ? err : new Error(String(err)),
          });
      });
    return () => {
      cancelled = true;
    };
  }, [skip]);

  return state;
}

// LoginShell is the full-viewport canvas the card floats on.
function LoginShell({ children }: { children: React.ReactNode }) {
  return <div className="login-screen">{children}</div>;
}