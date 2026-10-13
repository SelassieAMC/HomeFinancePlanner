import { RouterProvider } from 'react-router-dom';
import { router } from './router';
import { CartProvider } from '../features/cart/CartContext';
import { AuthProvider } from '../features/auth/AuthContext';

export function App() {
  return (
    <AuthProvider>
      <CartProvider>
        <RouterProvider router={router} />
      </CartProvider>
    </AuthProvider>
  );
}