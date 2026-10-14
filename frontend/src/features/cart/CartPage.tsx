import { useEffect, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import i18n from '../../i18n';
import { cartApi } from '../../api/cart';
import { storesApi } from '../../api/stores';
import { ApiError } from '../../api/client';
import type { OfferResult } from '../../types/domain';
import { useAsync } from '../../hooks/useAsync';
import { usePolling } from '../../hooks/usePolling';
import { formatCents } from '../../lib/money';
import { Button, Card, Tabs, EmptyState, ErrorMessage, ProductAutocomplete, Spinner } from '../../components/ui';
import { useCart } from './CartContext';
import { OfferResultView } from './OfferResultView';

type Phase = 'cart' | 'searching' | 'result' | 'failed';
type CartTab = 'cart' | 'searches';

const POLL_INTERVAL_MS = 2000;
const POLL_MAX_MS = 10 * 60_000; // grounded searches can take minutes

export function CartPage() {
  const cart = useCart();
  const { t } = useTranslation('cart');
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();

  // The active search (created here or deep-linked via ?token=…).
  const [token, setToken] = useState<string | null>(() => searchParams.get('token'));
  const [phase, setPhase] = useState<Phase>(token ? 'searching' : 'cart');
  const [tab, setTab] = useState<CartTab>(token ? 'searches' : 'cart');
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
      setFailed(polled.error || i18n.t('cart:errors.searchFailed'));
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
      setError(i18n.t('cart:errors.notFoundOrExpired'));
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
          setFailed(res.error || i18n.t('cart:errors.searchFailed'));
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
            ? i18n.t('cart:errors.notFoundOrExpired')
            : err instanceof Error
              ? err.message
              : i18n.t('cart:errors.load'),
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
    setTab('cart');
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
      setError(err instanceof Error ? err.message : i18n.t('cart:errors.start'));
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
      setError(err instanceof Error ? err.message : i18n.t('cart:errors.retry'));
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

  async function discardSearch(searchToken: string) {
    setBusy(true);
    try {
      await cartApi.remove(searchToken);
      setHistoryKey((k) => k + 1);
      if (searchToken === token) backToCart();
    } catch (err) {
      setError(err instanceof Error ? err.message : i18n.t('cart:errors.discard'));
    } finally {
      setBusy(false);
    }
  }

  const noProviderHint = phase === 'cart' && (history.data?.length ?? 0) === 0;

  return (
    <div className="page">
      <h2 className="page-title">{t('title')}</h2>

      {confirmed && (
        <div className="hint-banner">
          {t('confirmedBanner')}
        </div>
      )}
      {noProviderHint && cart.items.length === 0 && (
        <div className="hint-banner">
          {t('hints.addBefore')}
          <Link to="/products">{t('hints.productsLink')}</Link>
          {t('hints.addMiddle')}
          <Link to="/settings">{t('hints.settingsLink')}</Link>
          {t('hints.addAfter')}
        </div>
      )}

      <Tabs
        ariaLabel={t('tabs.aria')}
        tabs={[
          { key: 'cart', label: t('tabs.cart') },
          { key: 'searches', label: t('tabs.searches') },
        ]}
        active={tab}
        onChange={setTab}
      />

      {tab === 'cart' && (
      <>
      {phase === 'cart' && (
        <Card title={t('cart.cardTitle')}>
          {cart.items.length === 0 ? (
            <EmptyState message={t('cart.empty')} />
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
                      aria-label={t('qty.decreaseAria', { name: it.name })}
                      onClick={() => cart.setQuantity(it.product_id, it.quantity - 1)}
                    >
                      −
                    </button>
                    <span aria-label={t('qty.aria', { name: it.name })}>{it.quantity}</span>
                    <button
                      type="button"
                      className="icon-btn"
                      aria-label={t('qty.increaseAria', { name: it.name })}
                      onClick={() => cart.setQuantity(it.product_id, it.quantity + 1)}
                    >
                      +
                    </button>
                  </span>
                  <span className="cart-last-price">
                    {it.last_price_cents !== undefined && it.currency
                      ? t('lastPrice', { price: formatCents(it.last_price_cents, it.currency) })
                      : ''}
                  </span>
                  <button
                    type="button"
                    className="icon-btn"
                    title={t('remove.title')}
                    aria-label={t('remove.aria', { name: it.name })}
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
              ariaLabel={t('add.aria')}
              placeholder={t('add.placeholder')}
            />
          </div>

          <div className="camera-row">
            <Button
              onClick={() => setOptionsOpen((open) => !open)}
              disabled={busy || cart.items.length === 0}
            >
              {t('search.offers')}
            </Button>
            <Button variant="secondary" onClick={() => cart.clear()} disabled={busy || cart.items.length === 0}>
              {t('search.clearAll')}
            </Button>
          </div>

          {optionsOpen && cart.items.length > 0 && (
            <div className="search-options">
              <fieldset className="search-options-group">
                <legend>{t('options.storeScope')}</legend>
                <label className="search-options-choice">
                  <input
                    type="radio"
                    name="store-scope"
                    checked={!pinStores}
                    onChange={() => setPinStores(false)}
                  />
                  {t('options.anyMarket')}
                </label>
                <label className="search-options-choice">
                  <input
                    type="radio"
                    name="store-scope"
                    checked={pinStores}
                    onChange={() => setPinStores(true)}
                  />
                  {t('options.pinnedStores')}
                </label>
                {pinStores && (
                  stores.loading && !stores.data ? (
                    <Spinner label={t('options.loadingStores')} />
                  ) : (stores.data ?? []).length === 0 ? (
                    <p className="hint-text">
                      {t('options.noStores')}
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
                {t('options.loose')}
              </label>

              <div className="camera-row">
                <Button
                  onClick={handleSearch}
                  disabled={busy || cart.items.length === 0 || (pinStores && pickedStores.length === 0)}
                >
                  {t('options.start')}
                </Button>
                <Button variant="secondary" onClick={() => setOptionsOpen(false)} disabled={busy}>
                  {t('actions.cancel')}
                </Button>
              </div>
            </div>
          )}
          {error && <ErrorMessage message={error} />}
        </Card>
      )}

      {phase === 'searching' && (
        <Card title={t('searching.title')}>
          {poll.timedOut ? (
            <div className="hint-banner">
              {t('searching.timedOut')}
            </div>
          ) : (
            <Spinner label={t('searching.spinner')} />
          )}
          {error && <ErrorMessage message={error} />}
        </Card>
      )}

      {phase === 'failed' && (
        <Card title={t('failed.title')}>
          <ErrorMessage message={failed || t('errors.searchFailed')} />
          <div className="hint-banner">
            {t('failed.hintBefore')}
            <Link to="/settings">{t('failed.settingsLink')}</Link>
            {t('failed.hintAfter')}
          </div>
          <div className="camera-row">
            <Button onClick={handleRetry} disabled={busy}>
              {t('failed.retry')}
            </Button>
            <Button variant="secondary" onClick={backToCart} disabled={busy}>
              {t('actions.backToCart')}
            </Button>
          </div>
          {error && <ErrorMessage message={error} />}
        </Card>
      )}

      {phase === 'result' && result && (
        <Card title={t('result.title')}>
          <OfferResultView result={result} />
          <div className="camera-row">
            <Button onClick={handleConfirmPurchase}>{t('result.confirm')}</Button>
            <Button variant="secondary" onClick={backToCart}>
              {t('actions.backToCart')}
            </Button>
          </div>
          {error && <ErrorMessage message={error} />}
        </Card>
      )}
      </>
      )}

      {tab === 'searches' && (
      <Card title={t('history.title')}>
        {history.loading && !history.data ? (
          <Spinner />
        ) : (history.data ?? []).length === 0 ? (
          <EmptyState message={t('history.empty')} />
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
                    setTab('cart');
                    setSearchParams({ token: s.search_token });
                  }}
                >
                  {new Date(s.created_at ?? '').toLocaleString()} ·{' '}
                  {t('history.products', { count: s.products?.length ?? 0 })}
                  {s.stores && s.stores.length > 0 && ` · ${t('history.pinned', { count: s.stores.length })}`}
                  {s.name_match === 'loose' && ` · ${t('history.loose')}`}
                </button>
                <span className={`offer-status offer-status-${s.status}`}>
                  {t(`statuses.${s.status}`, { defaultValue: s.status })}
                </span>
                <Button
                  type="button"
                  variant="ghost"
                  className="btn-sm"
                  disabled={busy || s.status === 'searching'}
                  onClick={() => discardSearch(s.search_token)}
                >
                  {t('actions.discard')}
                </Button>
              </li>
            ))}
          </ul>
        )}
        {history.error && <ErrorMessage message={history.error.message} />}
      </Card>
      )}
    </div>
  );
}