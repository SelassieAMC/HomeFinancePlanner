import { useEffect, useState } from 'react';
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';

// Mobile bottom tab bar: the three destinations that matter daily get tabs;
// the rest live behind "More". The center button opens a speed dial with the
// two quick-entry actions. The desktop sidebar is unchanged.
const TAB_ITEMS: { to: string; label: string; icon: string; end?: boolean }[] = [
  { to: '/', label: 'Home', icon: '🏠', end: true },
  { to: '/transactions', label: 'Activity', icon: '🧾' },
  { to: '/budgets', label: 'Budgets', icon: '🎯' },
];

const MORE_ITEMS: { to: string; label: string; icon: string; end?: boolean }[] = [
  { to: '/accounts', label: 'Accounts', icon: '🏦' },
  { to: '/stores', label: 'Stores', icon: '🏪' },
  { to: '/bills', label: 'Bills', icon: '🗂️' },
  { to: '/scan', label: 'Scan bill', icon: '📷' },
  { to: '/settings', label: 'Settings', icon: '⚙️' },
];

const FAB_ITEMS = [
  { to: '/scan', label: 'Scan bill', icon: '📷' },
  { to: '/transactions', label: 'Add transaction', icon: '➕' },
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
        <div className="sidebar-brand">
          <span className="sidebar-logo" aria-hidden="true">💰</span>
          <span>Home Finance Planner</span>
        </div>
        <nav aria-label="Main navigation">
          <ul className="sidebar-nav">
            {[...TAB_ITEMS, ...MORE_ITEMS].map((item) => (
              <li key={item.to}>
                <NavLink
                  to={item.to}
                  end={item.end}
                  className={({ isActive }) => (isActive ? 'nav-link active' : 'nav-link')}
                >
                  {item.label}
                </NavLink>
              </li>
            ))}
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
          <span className="tab-icon" aria-hidden="true">🏠</span>
          Home
        </NavLink>
        <NavLink
          to="/transactions"
          className={({ isActive }) => (isActive ? 'tab-link active' : 'tab-link')}
        >
          <span className="tab-icon" aria-hidden="true">🧾</span>
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
                    <span aria-hidden="true">{item.icon}</span>
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
          <span className="tab-icon" aria-hidden="true">🎯</span>
          Budgets
        </NavLink>
        <button
          type="button"
          className={moreActive || moreOpen ? 'tab-link active' : 'tab-link'}
          aria-haspopup="dialog"
          aria-expanded={moreOpen}
          onClick={() => setMoreOpen(true)}
        >
          <span className="tab-icon" aria-hidden="true">☰</span>
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
                    <NavLink
                      to={item.to}
                      className={({ isActive }) =>
                        isActive ? 'more-link active' : 'more-link'
                      }
                    >
                      <span className="more-icon" aria-hidden="true">{item.icon}</span>
                      {item.label}
                    </NavLink>
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