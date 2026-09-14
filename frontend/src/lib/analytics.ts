// Chart transforms shared by the analytics dashboard components. Pure math
// (no React) so the geometry stays unit-testable.
import type { PriceIndex, SunburstLeaf, SunburstSegment } from '../types/analytics';

/** Pivot the price-index response into one recharts row per month with a
 *  column per series (undefined = no purchases that month → line gap). */
export interface PriceIndexRow extends Record<string, number | string | undefined> {
  month: string;
}

export function priceIndexRows(res: PriceIndex): {
  rows: PriceIndexRow[];
  labels: string[];
  /** Absolute per-base-unit prices for tooltips, keyed `${label}|${month}`. */
  unitPrices: Map<string, number>;
  units: Map<string, string>;
} {
  const labels = [...res.series.map((s) => s.label), 'Average'];
  const rows: PriceIndexRow[] = res.average.map((avg) => ({
    month: avg.month,
    ...(avg.index !== undefined && avg.index !== null ? { Average: avg.index } : {}),
  }));
  const unitPrices = new Map<string, number>();
  const units = new Map<string, string>();
  for (const series of res.series) {
    units.set(series.label, series.base_unit);
    for (const point of series.points) {
      if (point.index !== null) {
        const row = rows.find((r) => r.month === point.month);
        if (row) row[series.label] = point.index;
      }
      if (point.unit_price_cents !== null) {
        unitPrices.set(`${series.label}|${point.month}`, point.unit_price_cents);
      }
    }
  }
  return { rows, labels, unitPrices, units };
}

// --- Sunburst arc geometry ---------------------------------------------------

const TAU = Math.PI * 2;

/** One donut-ring slice, pre-computed to an SVG path. */
export interface ArcSlice {
  path: string;
  startAngle: number;
  endAngle: number;
  /** Index into the segments array this slice belongs to (color key). */
  segmentIndex: number;
  /** Present on outer-ring slices only. */
  leaf?: SunburstLeaf;
}

function arcPath(r0: number, r1: number, start: number, end: number): string {
  const large = end - start > Math.PI ? 1 : 0;
  const [x0, y0] = [r0 * Math.sin(start), -r0 * Math.cos(start)];
  const [x1, y1] = [r1 * Math.sin(start), -r1 * Math.cos(start)];
  const [x2, y2] = [r1 * Math.sin(end), -r1 * Math.cos(end)];
  const [x3, y3] = [r0 * Math.sin(end), -r0 * Math.cos(end)];
  return [
    `M ${x0} ${y0}`,
    `L ${x1} ${y1}`,
    `A ${r1} ${r1} 0 ${large} 1 ${x2} ${y2}`,
    `L ${x3} ${y3}`,
    `A ${r0} ${r0} 0 ${large} 0 ${x0} ${y0}`,
    'Z',
  ].join(' ');
}

/** Compute the two rings of the sunburst around (0,0) — the component
 *  translates the group into place. Angles start at 12 o'clock and run
 *  clockwise in the server's draw order (largest first). Slices under
 *  minShare are dropped. */
export function sunburstArcs(
  segments: SunburstSegment[],
  innerR: number,
  midR: number,
  outerR: number,
  minShare = 0.005,
): { inner: ArcSlice[]; outer: ArcSlice[] } {
  let angle = 0;
  const inner: ArcSlice[] = [];
  const outer: ArcSlice[] = [];
  for (let s = 0; s < segments.length; s++) {
    const seg = segments[s]!;
    if (seg.share < minShare) continue;
    const span = seg.share * TAU;
    inner.push({
      path: arcPath(innerR, midR, angle, angle + span),
      startAngle: angle,
      endAngle: angle + span,
      segmentIndex: s,
    });

    // Outer ring: children share the parent's span proportionally.
    const childTotal = seg.children.reduce((sum, c) => sum + c.share, 0) || 1;
    let childAngle = angle;
    for (const child of seg.children) {
      if (child.share < minShare) continue;
      const childSpan = (child.share / childTotal) * span;
      outer.push({
        path: arcPath(midR, outerR, childAngle, childAngle + childSpan),
        startAngle: childAngle,
        endAngle: childAngle + childSpan,
        segmentIndex: s,
        leaf: child,
      });
      childAngle += childSpan;
    }
    angle += span;
  }
  return { inner, outer };
}

// --- Heatmap shading ---------------------------------------------------------

/** Quantize a value into one of `steps` sequential palette steps (0..steps-1).
 *  Zero renders as step 0 (empty); the max value gets the darkest step. */
export function heatmapShade(value: number, max: number, steps = 6): number {
  if (value <= 0 || max <= 0) return 0;
  const ratio = Math.min(1, value / max);
  return Math.min(steps - 1, 1 + Math.floor(ratio * (steps - 1)));
}