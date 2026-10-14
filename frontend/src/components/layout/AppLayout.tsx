import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Navigate, NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';
import { Spinner } from '../ui';
import { useAuth } from '../../features/auth/AuthContext';
import { UserAvatar } from './UserAvatar';

// Mobile bottom tab bar: the three destinations that matter daily get tabs;
// the rest live behind "More". The center button opens a speed dial with the
// two quick-entry actions. The desktop sidebar is unchanged — plain text
// navigation, no icons.
// External items are plain anchors outside the SPA router (the backend serves
// them directly — e.g. the River job-queue UI), never NavLink routes.
// The `label` field holds a `common` i18n key (nav.*) — translated at render
// time so the language switch applies without a reload of the module.
type NavItem = { to: string; label: string; end?: boolean };

const TAB_ITEMS: NavItem[] = [
  { to: '/', label: 'nav.home', end: true },
  { to: '/transactions', label: 'nav.activity' },
  { to: '/planning', label: 'nav.planning' },
];

const MORE_ITEMS: (NavItem & { external?: true; href?: string })[] = [
  { to: '/accounts', label: 'nav.accounts' },
  { to: '/stores', label: 'nav.stores' },
  { to: '/products', label: 'nav.products' },
  { to: '/cart', label: 'nav.purchase_cart' },
  { to: '/bills', label: 'nav.bills' },
  { to: '/scan', label: 'nav.scan_bill' },
  { to: '/settings', label: 'nav.settings' },
  {
    to: '/riverui',
    label: 'nav.job_queue',
    external: true,
    href: '/riverui',
  },
];

const FAB_ITEMS = [
  { to: '/scan', label: 'nav.scan_bill' },
  { to: '/transactions', label: 'nav.add_transaction' },
];

// Every destination in one list for the desktop sidebar; the union keeps
// external fields available while mapping.
const ALL_ITEMS: (NavItem & { external?: true; href?: string })[] = [
  ...TAB_ITEMS,
  ...MORE_ITEMS,
];

export function AppLayout() {
  const { user, loading } = useAuth();
  const { t } = useTranslation('common');
  const location = useLocation();
  const navigate = useNavigate();
  const [moreOpen, setMoreOpen] = useState(false);
  const [fabOpen, setFabOpen] = useState(false);

  // Any navigation closes the overlays.
  useEffect(() => {
    setMoreOpen(false);
    setFabOpen(false);
  }, [location]);

  // Escape closes whichever overlay is open.
  useEffect(() => {
    if (!moreOpen && !fabOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setMoreOpen(false);
        setFabOpen(false);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [moreOpen, fabOpen]);

  // "More" is active while any of its pages is on screen.
  const moreActive =
    !TAB_ITEMS.some((item) => item.to === location.pathname) &&
    MORE_ITEMS.some((item) => location.pathname.startsWith(item.to));

  // The session check gates the shell: no user means the login page, and a
  // mid-session sign-out (any 401) lands here on the next render.
  if (loading) return <div className="app-layout"><Spinner /></div>;
  if (!user) return <Navigate to="/login" replace />;

  return (
    <div className="app-layout">
      {/* Top-right profile chip (fixed; visible on desktop and mobile). */}
      <UserAvatar />
      {/* Desktop navigation — hidden behind the tab bar on mobile. */}
      <aside className="sidebar">
        <div className="sidebar-brand">Home Finance Planner</div>
        <nav aria-label={t('nav.main_aria')}>
          <ul className="sidebar-nav">
            {ALL_ITEMS.map((item) =>
              item.external ? (
                <li key={item.to}>
                  <a href={item.href} className="nav-link">
                    {t(item.label)}
                  </a>
                </li>
              ) : (
                <li key={item.to}>
                  <NavLink
                    to={item.to}
                    end={item.end}
                    className={({ isActive }) => (isActive ? 'nav-link active' : 'nav-link')}
                  >
                    {t(item.label)}
                  </NavLink>
                </li>
              ),
            )}
          </ul>
        </nav>
      </aside>
      <main className="app-main">
        <Outlet />
      </main>

      {/* Mobile: bottom tab bar with a center speed-dial FAB. */}
      <nav className="tabbar" aria-label={t('nav.main_aria')}>
        <NavLink
          to="/"
          end
          className={({ isActive }) => (isActive ? 'tab-link active' : 'tab-link')}
        >
          {t('nav.home')}
        </NavLink>
        <NavLink
          to="/transactions"
          className={({ isActive }) => (isActive ? 'tab-link active' : 'tab-link')}
        >
          {t('nav.activity')}
        </NavLink>
        <span className="tab-fab-slot">
          {fabOpen && (
            <>
              {/* Transparent tap-catcher: tapping anywhere else closes the dial. */}
              <div
                className="fab-backdrop"
                aria-hidden="true"
                onClick={() => setFabOpen(false)}
              />
              <div className="fab-menu">
                {FAB_ITEMS.map((item) => (
                  <button
                    key={item.to}
                    type="button"
                    className="fab-item"
                    onClick={() => navigate(item.to)}
                  >
                    {t(item.label)}
                  </button>
                ))}
              </div>
            </>
          )}
          <button
            type="button"
            className={fabOpen ? 'tab-fab open' : 'tab-fab'}
            aria-label={t('nav.quick_actions_aria')}
            aria-expanded={fabOpen}
            onClick={() => setFabOpen((open) => !open)}
          >
            +
          </button>
        </span>
        <NavLink
          to="/planning"
          className={({ isActive }) => (isActive ? 'tab-link active' : 'tab-link')}
        >
          {t('nav.planning')}
        </NavLink>
        <button
          type="button"
          className={moreActive || moreOpen ? 'tab-link active' : 'tab-link'}
          aria-haspopup="dialog"
          aria-expanded={moreOpen}
          onClick={() => setMoreOpen(true)}
        >
          {t('nav.more')}
        </button>
      </nav>

      {/* Mobile: "More" bottom sheet with the secondary destinations. */}
      {moreOpen && (
        <>
          <div
            className="sheet-backdrop"
            aria-hidden="true"
            onClick={() => setMoreOpen(false)}
          />
          <div className="more-sheet" role="dialog" aria-label={t('nav.more_pages_aria')}>
            <nav>
              <ul onClick={() => setMoreOpen(false)}>
                {MORE_ITEMS.map((item) => (
                  <li key={item.to}>
                    {item.external ? (
                      <a href={item.href} className="more-link">
                        {t(item.label)}
                      </a>
                    ) : (
                      <NavLink
                        to={item.to}
                        className={({ isActive }) =>
                          isActive ? 'more-link active' : 'more-link'
                        }
                      >
                        {t(item.label)}
                      </NavLink>
                    )}
                  </li>
                ))}
              </ul>
            </nav>
          </div>
        </>
      )}
    </div>
  );
}