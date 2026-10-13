import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { authApi } from '../../api/auth';
import { onUnauthorized } from '../../api/client';
import type { User } from '../../types/domain';

interface AuthState {
  /** The signed-in user, or null when the login page should show. */
  user: User | null;
  /** True until the first me() round-trip decides logged-in vs out. */
  loading: boolean;
  setUser: (user: User | null) => void;
  reload: () => void;
}

const AuthContext = createContext<AuthState | null>(null);

// Session holder: the router gate (AppLayout/login page) renders from this.
// A 401 from ANY api call — an expired cookie, a revoked session — flips the
// state back to logged out via the client's unauthorized hook.
export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);

  const reload = useCallback(() => {
    setLoading(true);
    authApi
      .me()
      .then((u) => setUser(u))
      .catch(() => setUser(null))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    reload();
  }, [reload]);

  // 401s land here once this provider is mounted. The login endpoint can
  // 401 on a wrong password too — user is already null then, no-op.
  useEffect(() => {
    onUnauthorized(() => setUser((current) => (current ? null : current)));
    // The handler reads nothing else; user flips are handled via the updater.
    return () => {
      onUnauthorized(null);
    };
  }, []);

  const value = useMemo<AuthState>(
    () => ({ user, loading, setUser, reload }),
    [user, loading, reload],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used inside AuthProvider');
  return ctx;
}