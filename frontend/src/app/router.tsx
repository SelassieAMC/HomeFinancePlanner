import { createBrowserRouter } from 'react-router-dom';
import { AppLayout } from '../components/layout/AppLayout';
import { DashboardPage } from '../features/dashboard/DashboardPage';
import { AccountsPage } from '../features/accounts/AccountsPage';
import { TransactionsPage } from '../features/transactions/TransactionsPage';
import { BudgetsPage } from '../features/budgets/BudgetsPage';
import { ScanBillsPage } from '../features/bills/ScanBillsPage';
import { BillsPage } from '../features/bills/BillsPage';
import { SettingsPage } from '../features/settings/SettingsPage';

export const router = createBrowserRouter([
  {
    path: '/',
    element: <AppLayout />,
    children: [
      { index: true, element: <DashboardPage /> },
      { path: 'accounts', element: <AccountsPage /> },
      { path: 'transactions', element: <TransactionsPage /> },
      { path: 'budgets', element: <BudgetsPage /> },
      { path: 'scan', element: <ScanBillsPage /> },
      { path: 'bills', element: <BillsPage /> },
      { path: 'settings', element: <SettingsPage /> },
    ],
  },
]);