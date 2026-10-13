import { createBrowserRouter, Navigate } from 'react-router-dom';
import { AppLayout } from '../components/layout/AppLayout';
import { LoginPage } from '../features/auth/LoginPage';
import { DashboardPage } from '../features/dashboard/DashboardPage';
import { AccountsPage } from '../features/accounts/AccountsPage';
import { TransactionsPage } from '../features/transactions/TransactionsPage';
import { PlanningPage } from '../features/planning/PlanningPage';
import { StoresPage } from '../features/stores/StoresPage';
import { ProductsPage } from '../features/products/ProductsPage';
import { CartPage } from '../features/cart/CartPage';
import { ScanBillsPage } from '../features/bills/ScanBillsPage';
import { BillsPage } from '../features/bills/BillsPage';
import { SettingsPage } from '../features/settings/SettingsPage';
import { PromptsPage } from '../features/prompts/PromptsPage';

export const router = createBrowserRouter([
  // The login gate lives outside the app shell (no sidebar, no avatar).
  { path: '/login', element: <LoginPage /> },
  {
    path: '/',
    element: <AppLayout />,
    children: [
      { index: true, element: <DashboardPage /> },
      { path: 'accounts', element: <AccountsPage /> },
      { path: 'transactions', element: <TransactionsPage /> },
      // One Planning view with tabs; the old per-layer routes redirect so
      // existing links (bookmarks, old navigation targets) keep landing right.
      { path: 'budgets', element: <Navigate to="/planning" replace /> },
      { path: 'goals', element: <Navigate to="/planning?tab=goals" replace /> },
      { path: 'bags', element: <Navigate to="/planning?tab=bags" replace /> },
      { path: 'planning', element: <PlanningPage /> },
      { path: 'stores', element: <StoresPage /> },
      { path: 'products', element: <ProductsPage /> },
      { path: 'cart', element: <CartPage /> },
      { path: 'scan', element: <ScanBillsPage /> },
      { path: 'bills', element: <BillsPage /> },
      { path: 'settings', element: <SettingsPage /> },
      { path: 'settings/prompts', element: <PromptsPage /> },
    ],
  },
]);