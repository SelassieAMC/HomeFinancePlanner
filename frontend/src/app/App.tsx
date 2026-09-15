import { RouterProvider } from 'react-router-dom';
import { router } from './router';
import { CartProvider } from '../features/cart/CartContext';

export function App() {
  return (
    <CartProvider>
      <RouterProvider router={router} />
    </CartProvider>
  );
}