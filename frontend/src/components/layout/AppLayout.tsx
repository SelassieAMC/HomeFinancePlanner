import { useState } from 'react';
import { NavLink, Outlet } from 'react-router-dom';

const NAV_ITEMS = [
  { to: '/', label: 'Dashboard', end: true },
  { to: '/accounts', label: 'Accounts' },
  { to: '/transactions', label: 'Transactions' },
  { to: '/budgets', label: 'Budgets' },
  { to: '/stores', label: 'Stores' },
  { to: '/scan', label: 'Scan bill' },
  { to: '/bills', label: 'Bills' },
  { to: '/settings', label: 'Settings' },
];

export function AppLayout() {
  // Mobile: the nav collapses behind a hamburger toggle.
  const [menuOpen, setMenuOpen] = useState(false);

  return (
    <div className="app-layout">
      <aside className={menuOpen ? 'sidebar open' : 'sidebar'}>
        <div className="sidebar-brand">
          <span className="sidebar-logo" aria-hidden="true">💰</span>
          <span>Home Finance Planner</span>
          <button
            type="button"
            className="hamburger"
            aria-label={menuOpen ? 'Close menu' : 'Open menu'}
            aria-expanded={menuOpen}
            onClick={() => setMenuOpen((open) => !open)}
          >
            {menuOpen ? '✕' : '☰'}
          </button>
        </div>
        <nav aria-label="Main navigation">
          <ul className="sidebar-nav" onClick={() => setMenuOpen(false)}>
            {NAV_ITEMS.map((item) => (
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
    </div>
  );
}