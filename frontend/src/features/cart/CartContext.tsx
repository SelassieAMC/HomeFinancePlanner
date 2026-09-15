import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import type { Product } from '../../types/domain';

/**
 * CartItem is one purchase-cart line, persisted in localStorage so the cart
 * survives reloads. It snapshots the fields the offer search needs — product
 * edits after adding are not picked up (the search re-reads them server-side).
 */
export interface CartItem {
  product_id: number;
  name: string;
  brand?: string;
  unit?: string;
  quantity: number;
  currency?: string;
  last_price_cents?: number;
}

interface CartContextValue {
  items: CartItem[];
  /** Upserts the product (or overrides its quantity when already present). */
  add: (product: Product, quantity?: number) => void;
  setQuantity: (productId: number, quantity: number) => void;
  remove: (productId: number) => void;
  clear: () => void;
  has: (productId: number) => boolean;
  count: number;
}

// Versioned key: a future shape change bumps to .v2 instead of migrating.
const STORAGE_KEY = 'hfp.cart.v1';

const CartContext = createContext<CartContextValue | null>(null);

/** Lazy-loads the cart from localStorage; corrupt JSON starts an empty cart. */
function loadCart(): CartItem[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(
      (it): it is CartItem =>
        typeof it === 'object' && it !== null && typeof (it as CartItem).product_id === 'number',
    );
  } catch {
    return []; // private mode / corrupted entry — the cart is non-critical
  }
}

/**
 * Purchase-cart state shared across pages (written from the products page,
 * read and managed on the cart page) with a localStorage write-through.
 * Storage failures (quota, private mode) are ignored — the in-memory cart
 * keeps working for the session.
 */
export function CartProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<CartItem[]>(loadCart);

  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(items));
    } catch {
      // Non-critical: keep the in-memory cart without persistence.
    }
  }, [items]);

  const add = useCallback((product: Product, quantity = 1) => {
    setItems((prev) => {
      const existing = prev.find((it) => it.product_id === product.id);
      if (existing) {
        return prev.map((it) =>
          it.product_id === product.id ? { ...it, quantity: it.quantity + quantity } : it,
        );
      }
      return [
        ...prev,
        {
          product_id: product.id,
          name: product.name,
          brand: product.brand,
          unit: product.unit,
          quantity,
          currency: product.price_currency,
          last_price_cents: product.best_price_cents,
        },
      ];
    });
  }, []);

  const setQuantity = useCallback((productId: number, quantity: number) => {
    setItems((prev) =>
      quantity > 0
        ? prev.map((it) => (it.product_id === productId ? { ...it, quantity } : it))
        : prev.filter((it) => it.product_id !== productId),
    );
  }, []);

  const remove = useCallback((productId: number) => {
    setItems((prev) => prev.filter((it) => it.product_id !== productId));
  }, []);

  const clear = useCallback(() => setItems([]), []);

  const value = useMemo<CartContextValue>(
    () => ({
      items,
      add,
      setQuantity,
      remove,
      clear,
      has: (productId: number) => items.some((it) => it.product_id === productId),
      count: items.length,
    }),
    [items, add, setQuantity, remove, clear],
  );

  return <CartContext.Provider value={value}>{children}</CartContext.Provider>;
}

/** Accesses the purchase cart. Must be used below <CartProvider>. */
export function useCart(): CartContextValue {
  const ctx = useContext(CartContext);
  if (!ctx) throw new Error('useCart must be used inside <CartProvider>');
  return ctx;
}