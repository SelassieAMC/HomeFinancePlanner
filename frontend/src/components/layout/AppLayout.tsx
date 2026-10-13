import { useEffect, useState } from 'react';
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';

// Mobile bottom tab bar: the three destinations that matter daily get tabs;
// the rest live behind "More". The center button opens a speed dial with the
// two quick-entry actions. The desktop sidebar is unchanged — plain text
// navigation, no icons.
// External items are plain anchors outside the SPA router (the backend serves
// them directly — e.g. the River job-queue UI), never NavLink routes.
type NavItem = { to: string; label: string; end?: boolean };

const TAB_ITEMS: NavItem[] = [
  { to: '/', label: 'Home', end: true },
  { to: '/transactions', label: 'Activity' },
  { to: '/budgets', label: 'Budgets' },
];

const MORE_ITEMS: (NavItem & { external?: true; href?: string })[] = [
  { to: '/accounts', label: 'Accounts' },
  { to: '/stores', label: 'Stores' },
  { to: '/products', label: 'Products' },
  { to: '/cart', label: 'Purchase cart' },
  { to: '/bills', label: 'Bills' },
  { to: '/scan', label: 'Scan bill' },
  { to: '/settings', label: 'Settings' },
  {
    to: '/riverui',
    label: 'Job queue',
    external: true,
    href: '/riverui',
  },
];

const FAB_ITEMS = [
  { to: '/scan', label: 'Scan bill' },
  { to: '/transactions', label: 'Add transaction' },
];

// Every destination in one list for the desktop sidebar; the union keeps
// external fields available while mapping.
const ALL_ITEMS: (NavItem & { external?: true; href?: string })[] = [
  ...TAB_ITEMS,
  ...MORE_ITEMS,
];

export function AppLayout() {
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

  return (
    <div className="app-layout">
      {/* Desktop navigation — hidden behind the tab bar on mobile. */}
      <aside className="sidebar">
        <div className="sidebar-brand">Home Finance Planner</div>
        <nav aria-label="Main navigation">
          <ul className="sidebar-nav">
            {ALL_ITEMS.map((item) =>
              item.external ? (
                <li key={item.to}>
                  <a href={item.href} className="nav-link">
                    {item.label}
                  </a>
                </li>
              ) : (
                <li key={item.to}>
                  <NavLink
                    to={item.to}
                    end={item.end}
                    className={({ isActive }) => (isActive ? 'nav-link active' : 'nav-link')}
                  >
                    {item.label}
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
      <nav className="tabbar" aria-label="Main navigation">
        <NavLink
          to="/"
          end
          className={({ isActive }) => (isActive ? 'tab-link active' : 'tab-link')}
        >
          Home
        </NavLink>
        <NavLink
          to="/transactions"
          className={({ isActive }) => (isActive ? 'tab-link active' : 'tab-link')}
        >
          Activity
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
                    {item.label}
                  </button>
                ))}
              </div>
            </>
          )}
          <button
            type="button"
            className={fabOpen ? 'tab-fab open' : 'tab-fab'}
            aria-label="Quick actions"
            aria-expanded={fabOpen}
            onClick={() => setFabOpen((open) => !open)}
          >
            +
          </button>
        </span>
        <NavLink
          to="/budgets"
          className={({ isActive }) => (isActive ? 'tab-link active' : 'tab-link')}
        >
          Budgets
        </NavLink>
        <button
          type="button"
          className={moreActive || moreOpen ? 'tab-link active' : 'tab-link'}
          aria-haspopup="dialog"
          aria-expanded={moreOpen}
          onClick={() => setMoreOpen(true)}
        >
          More
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
          <div className="more-sheet" role="dialog" aria-label="More pages">
            <nav>
              <ul onClick={() => setMoreOpen(false)}>
                {MORE_ITEMS.map((item) => (
                  <li key={item.to}>
                    {item.external ? (
                      <a href={item.href} className="more-link">
                        {item.label}
                      </a>
                    ) : (
                      <NavLink
                        to={item.to}
                        className={({ isActive }) =>
                          isActive ? 'more-link active' : 'more-link'
                        }
                      >
                        {item.label}
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