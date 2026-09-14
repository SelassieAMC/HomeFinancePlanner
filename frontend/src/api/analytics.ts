import { apiClient } from './client';
import type {
  CategorySunburst,
  FixedSplit,
  PriceIndex,
  RunRate,
  SpendHeatmap,
  StorePriceIndex,
} from '../types/analytics';

const BASE = '/api/v1/analytics';

/** Dashboard chart aggregates. Every endpoint returns base-currency cents;
 *  conversion warnings follow the same convention as the summary. */
export const analyticsApi = {
  /** Per-store relative price index over overlapping products. */
  storePriceIndex: (from: string, to: string) =>
    apiClient.get<StorePriceIndex>(
      `${BASE}/store-price-index?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
    ),
  /** Accepted bill spend as section → category hierarchy. */
  categorySunburst: (from: string, to: string) =>
    apiClient.get<CategorySunburst>(
      `${BASE}/category-sunburst?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
    ),
  /** Cumulative spend vs. references for one month (YYYY-MM). */
  runRate: (month: string) =>
    apiClient.get<RunRate>(`${BASE}/run-rate?month=${encodeURIComponent(month)}`),
  /** Personal price index for staple items over the window. */
  priceIndex: (from: string, to: string) =>
    apiClient.get<PriceIndex>(
      `${BASE}/price-index?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
    ),
  /** Weekday × purchase-type spend grid. */
  spendHeatmap: (from: string, to: string) =>
    apiClient.get<SpendHeatmap>(
      `${BASE}/spend-heatmap?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
    ),
  /** Fixed vs. discretionary monthly split. */
  fixedSplit: (from: string, to: string) =>
    apiClient.get<FixedSplit>(
      `${BASE}/fixed-split?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
    ),
};