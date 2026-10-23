// Reference exchange rates (Frankfurter/ECB, cached 24h server-side). The
// transfer form and bill review use it to show the reference rate a
// cross-currency movement converts at — and to accept the user's override
// when the bank applies its own rate.
import { apiClient } from './client';
import type { FxSnapshot } from '../types/domain';

const BASE = '/api/v1';

export const fxApi = {
  rates: () => apiClient.get<FxSnapshot>(`${BASE}/fx/rates`),
};

/**
 * Cross rate between two currencies through the snapshot's pivot (units of
 * `to` per 1 of `from`). Returns undefined when either side is missing or no
 * snapshot was served — callers then show no rate and send no override.
 */
export function crossRate(
  snap: FxSnapshot | null | undefined,
  from?: string | null,
  to?: string | null,
): number | undefined {
  if (!snap || !from || !to || from === to) return undefined;
  const fromRate = from === snap.pivot ? 1 : snap.rates[from];
  const toRate = to === snap.pivot ? 1 : snap.rates[to];
  if (!fromRate || !toRate) return undefined;
  return toRate / fromRate;
}

/** Applies a rate to a cent amount and rounds once, half away from zero. */
export function convertCents(cents: number, rate: number): number {
  return Math.round(cents * rate);
}