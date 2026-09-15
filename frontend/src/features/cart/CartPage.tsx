import { useEffect, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { cartApi } from '../../api/cart';
import { storesApi } from '../../api/stores';
import { ApiError } from '../../api/client';
import type { OfferResult } from '../../types/domain';
import { useAsync } from '../../hooks/useAsync';
import { usePolling } from '../../hooks/usePolling';
import { formatCents } from '../../lib/money';
import { Button, Card, EmptyState, ErrorMessage, ProductAutocomplete, Spinner } from '../../components/ui';
import { useCart } from './CartContext';
import { OfferResultView } from './OfferResultView';

type Phase = 'cart' | 'searching' | 'result' | 'failed';

const POLL_INTERVAL_MS = 2000;
const POLL_MAX_MS = 10 * 60_000; // grounded searches can take minutes

export function CartPage() {
  const cart = useCart();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();

  // The active search (created here or deep-linked via ?token=…).
  const [token, setToken] = useState<string | null>(() => searchParams.get('token'));
  const [phase, setPhase] = useState<Phase>(token ? 'searching' : 'cart');
  const [result, setResult] = useState<OfferResult | null>(null);
  const [failed, setFailed] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // Shown once after "Confirm purchase": the cart was cleared, the result kept.
  const [confirmed, setConfirmed] = useState(false);

  // "Add item" field for products not yet in the cart.
  const [pickName, setPickName] = useState('');
  // Search options shown between "Search offers" and starting the search.
  const [optionsOpen, setOptionsOpen] = useState(false);
  const [pinStores, setPinStores] = useState(false);
  const [pickedStores, setPickedStores] = useState<string[]>([]);
  const [looseMatch, setLooseMatch] = useState(false);
  const stores = useAsync(storesApi.list, optionsOpen ? [optionsOpen] : []);
  // Past searches list, refreshed after new results land.
  const [historyKey, setHistoryKey] = useState(0);
  const history = useAsync(
    () => cartApi.list(['searching', 'done', 'failed']),
    [historyKey],
  );

  // While a search is running, poll its pipeline state every 2s.
  const poll = usePolling(
    () => (token ? cartApi.get(token) : Promise.reject(new Error('no search to poll'))),
    { intervalMs: POLL_INTERVAL_MS, enabled: phase === 'searching' && token !== null, maxMs: POLL_MAX_MS },
  );

  // Poll result → phase transitions.
  useEffect(() => {
    const polled = poll.data;
    if (phase !== 'searching' || !polled) return;
    if (polled.status === 'done' && polled.result) {
      setResult(polled.result);
      setFailed(null);
      setPhase('result');
      setHistoryKey((k) => k + 1);
    } else if (polled.status === 'failed') {
      setFailed(polled.error || 'The offer search failed.');
      setPhase('failed');
      setHistoryKey((k) => k + 1);
    }
  }, [poll.data, phase]);

  // A consumed/expired token (deleted or swept) stops polling.
  useEffect(() => {
    if (phase === 'searching' && poll.error instanceof ApiError && poll.error.status === 404) {
      setToken(null);
      setPhase('cart');
      setSearchParams({});
      setError('offer search not found or expired');
    }
  }, [poll.error, phase, setSearchParams]);

  // Resume a search reached from the history list (?token=…).
  useEffect(() => {
    if (!token) {
      setPhase('cart');
      return;
    }
    let cancelled = false;
    setPhase('searching');
    cartApi
      .get(token)
      .then((res) => {
        if (cancelled) return;
        if (res.status === 'done' && res.result) {
          setResult(res.result);
          setPhase('result');
        } else if (res.status === 'failed') {
          setFailed(res.error || 'The offer search failed.');
          setPhase('failed');
        }
        // still searching → keep polling
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setToken(null);
        setPhase('cart');
        setSearchParams({});
        setError(
          err instanceof ApiError && err.status === 404
            ? 'offer search not found or expired'
            : err instanceof Error
              ? err.message
              : 'Could not load the offer search.',
        );
      });
    return () => {
      cancelled = true;
    };
  }, [token, setSearchParams]);

  /** Confirms the purchase: start the offer search for the cart's products,
   *  scoped to the options chosen in the panel (pinned stores, name match). */
  async function handleSearch() {
    if (cart.items.length === 0) return;
    if (pinStores && pickedStores.length === 0) return;
    setBusy(true);
    setError(null);
    setConfirmed(false);
    try {
      const res = await cartApi.search({
        items: cart.items.map((it) => ({
          product_id: it.product_id,
          quantity: it.quantity,
          brand: it.brand || undefined,
        })),
        stores: pinStores ? pickedStores : undefined,
        name_match: looseMatch ? 'loose' : 'strict',
      });
      setOptionsOpen(false);
      setResult(null);
      setFailed(null);
      setToken(res.search_token);
      setPhase('searching');
      setSearchParams({ token: res.search_token }, { replace: true });
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not start the offer search.');
    } finally {
      setBusy(false);
    }
  }

  /** Re-runs a failed search with the default provider. */
  async function handleRetry() {
    if (!token) return;
    setBusy(true);
    setError(null);
    try {
      await cartApi.retry(token);
      setFailed(null);
      setPhase('searching');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Retry failed.');
    } finally {
      setBusy(false);
    }
  }

  /** Keeps the persisted result and empties the cart. */
  function handleConfirmPurchase() {
    cart.clear();
    setConfirmed(true);
    setToken(null);
    setResult(null);
    setPhase('cart');
    setSearchParams({});
    navigate('/cart');
  }

  /** Back from a result to the cart without clearing it. */
  function backToCart() {
    setToken(null);
    setResult(null);
    setPhase('cart');
    setSearchParams({});
  }

  async function discardSearch(t: string) {
    setBusy(true);
    try {
      await cartApi.remove(t);
      setHistoryKey((k) => k + 1);
      if (t === token) backToCart();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not discard the search.');
    } finally {
      setBusy(false);
    }
  }

  const noProviderHint = phase === 'cart' && (history.data?.length ?? 0) === 0;

  return (
    <div className="page">
      <h2 className="page-title">Purchase cart</h2>

      {confirmed && (
        <div className="hint-banner">
          Purchase confirmed — the cart was cleared. The offer report is kept
          below under “Past searches”.
        </div>
      )}
      {noProviderHint && cart.items.length === 0 && (
        <div className="hint-banner">
          Add products from the <Link to="/products">Products</Link> overview,
          then search current offers for them in your local markets with the AI
          connector from <Link to="/settings">Settings</Link>.
        </div>
      )}

      {phase === 'cart' && (
        <Card title="Cart">
          {cart.items.length === 0 ? (
            <EmptyState message="The cart is empty — add products from the products overview (🛒)." />
          ) : (
            <ul className="cart-items">
              {cart.items.map((it) => (
                <li key={it.product_id} className="cart-row">
                  <span className="cart-name">
                    {it.name}
                    {it.brand && <span className="item-brand"> {it.brand}</span>}
                  </span>
                  <span className="cart-qty">
                    <button
                      type="button"
                      className="icon-btn"
                      aria-label={`Decrease quantity of ${it.name}`}
                      onClick={() => cart.setQuantity(it.product_id, it.quantity - 1)}
                    >
                      −
                    </button>
                    <span aria-label={`Quantity of ${it.name}`}>{it.quantity}</span>
                    <button
                      type="button"
                      className="icon-btn"
                      aria-label={`Increase quantity of ${it.name}`}
                      onClick={() => cart.setQuantity(it.product_id, it.quantity + 1)}
                    >
                      +
                    </button>
                  </span>
                  <span className="cart-last-price">
                    {it.last_price_cents !== undefined && it.currency
                      ? `last: ${formatCents(it.last_price_cents, it.currency)}`
                      : ''}
                  </span>
                  <button
                    type="button"
                    className="icon-btn"
                    title="Remove from cart"
                    aria-label={`Remove ${it.name} from the cart`}
                    onClick={() => cart.remove(it.product_id)}
                  >
                    ✕
                  </button>
                </li>
              ))}
            </ul>
          )}

          <div className="cart-add-row">
            <ProductAutocomplete
              value={pickName}
              onValueChange={setPickName}
              onPick={(product) => {
                if (product) cart.add(product);
                setPickName('');
              }}
              ariaLabel="Add a product to the cart"
              placeholder="Add a product by name…"
            />
          </div>

          <div className="camera-row">
            <Button
              onClick={() => setOptionsOpen((open) => !open)}
              disabled={busy || cart.items.length === 0}
            >
              🔎 Search offers
            </Button>
            <Button variant="secondary" onClick={() => cart.clear()} disabled={busy || cart.items.length === 0}>
              Clear all
            </Button>
          </div>

          {optionsOpen && cart.items.length > 0 && (
            <div className="search-options">
              <fieldset className="search-options-group">
                <legend>Store scope</legend>
                <label className="search-options-choice">
                  <input
                    type="radio"
                    name="store-scope"
                    checked={!pinStores}
                    onChange={() => setPinStores(false)}
                  />
                  Any local market — whatever appears available
                </label>
                <label className="search-options-choice">
                  <input
                    type="radio"
                    name="store-scope"
                    checked={pinStores}
                    onChange={() => setPinStores(true)}
                  />
                  Only these stores (missing products are reported per store)
                </label>
                {pinStores && (
                  stores.loading && !stores.data ? (
                    <Spinner label="Loading your stores…" />
                  ) : (stores.data ?? []).length === 0 ? (
                    <p className="hint-text">
                      No stores recorded yet — add one on a bill, or leave the
                      scope open.
                    </p>
                  ) : (
                    <div className="search-options-stores">
                      {(stores.data ?? []).map((store) => (
                        <label key={store.id} className="search-options-choice">
                          <input
                            type="checkbox"
                            checked={pickedStores.includes(store.name)}
                            onChange={(e) =>
                              setPickedStores((picked) =>
                                e.target.checked
                                  ? [...picked, store.name]
                                  : picked.filter((name) => name !== store.name),
                              )
                            }
                          />
                          {store.name}
                        </label>
                      ))}
                    </div>
                  )
                )}
              </fieldset>

              <label className="search-options-choice">
                <input
                  type="checkbox"
                  checked={looseMatch}
                  onChange={(e) => setLooseMatch(e.target.checked)}
                />
                Include similar names and varieties (loose) — e.g. avocado also
                matches Hass, XL, ready-to-eat; each offer names what the
                market sells. Off = exact name only (strict).
              </label>

              <div className="camera-row">
                <Button
                  onClick={handleSearch}
                  disabled={busy || cart.items.length === 0 || (pinStores && pickedStores.length === 0)}
                >
                  Start search
                </Button>
                <Button variant="secondary" onClick={() => setOptionsOpen(false)} disabled={busy}>
                  Cancel
                </Button>
              </div>
            </div>
          )}
          {error && <ErrorMessage message={error} />}
        </Card>
      )}

      {phase === 'searching' && (
        <Card title="Searching offers">
          {poll.timedOut ? (
            <div className="hint-banner">
              Still searching — grounded web searches can take a while. You can
              leave this page; the result appears here (and under “Past
              searches”) once it finishes.
            </div>
          ) : (
            <Spinner label="Asking the AI connector for current offers in your local markets…" />
          )}
          {error && <ErrorMessage message={error} />}
        </Card>
      )}

      {phase === 'failed' && (
        <Card title="Offer search failed">
          <ErrorMessage message={failed || 'The offer search failed.'} />
          <div className="hint-banner">
            The configured AI connector could not search the web. Pick a model
            or provider with web search in <Link to="/settings">Settings</Link>{' '}
            (Gemini, Anthropic, or an OpenAI search model), then try again.
          </div>
          <div className="camera-row">
            <Button onClick={handleRetry} disabled={busy}>
              🔁 Try again
            </Button>
            <Button variant="secondary" onClick={backToCart} disabled={busy}>
              Back to cart
            </Button>
          </div>
          {error && <ErrorMessage message={error} />}
        </Card>
      )}

      {phase === 'result' && result && (
        <Card title="Offers found">
          <OfferResultView result={result} />
          <div className="camera-row">
            <Button onClick={handleConfirmPurchase}>✅ Confirm purchase</Button>
            <Button variant="secondary" onClick={backToCart}>
              Back to cart
            </Button>
          </div>
          {error && <ErrorMessage message={error} />}
        </Card>
      )}

      <Card title="Past searches">
        {history.loading && !history.data ? (
          <Spinner />
        ) : (history.data ?? []).length === 0 ? (
          <EmptyState message="No offer searches yet." />
        ) : (
          <ul className="cart-history">
            {history.data!.map((s) => (
              <li key={s.search_token} className="cart-history-row">
                <button
                  type="button"
                  className="cart-history-link"
                  onClick={() => {
                    setToken(s.search_token);
                    setResult(null);
                    setSearchParams({ token: s.search_token });
                  }}
                >
                  {new Date(s.created_at ?? '').toLocaleString()} ·{' '}
                  {s.products?.length ?? 0} product(s)
                  {s.stores && s.stores.length > 0 && ` · ${s.stores.length} pinned`}
                  {s.name_match === 'loose' && ' · loose'}
                </button>
                <span className={`offer-status offer-status-${s.status}`}>{s.status}</span>
                <button
                  type="button"
                  className="icon-btn"
                  title="Discard this search"
                  aria-label="Discard this search"
                  disabled={busy || s.status === 'searching'}
                  onClick={() => discardSearch(s.search_token)}
                >
                  🗑️
                </button>
              </li>
            ))}
          </ul>
        )}
        {history.error && <ErrorMessage message={history.error.message} />}
      </Card>
    </div>
  );
}