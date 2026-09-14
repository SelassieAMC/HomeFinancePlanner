import { useMemo, useState } from 'react';
import type { CategorySunburst } from '../../../types/analytics';
import { formatCents } from '../../../lib/money';
import { sunburstArcs } from '../../../lib/analytics';
import { ChartCard } from './ChartCard';

interface Props {
  data: CategorySunburst | null;
  loading: boolean;
  error: Error | null;
}

/** Inner-ring fill: the series color for the segment. Outer-ring fill: the
 *  same hue lightened toward the surface, so rings read as parent → child. */
function fill(index: number, outer: boolean): string {
  const base = `var(--chart-${(index % 8) + 1})`;
  return outer ? `color-mix(in srgb, ${base} 55%, var(--surface))` : base;
}

function midAngle(slice: { startAngle: number; endAngle: number }): number {
  return (slice.startAngle + slice.endAngle) / 2;
}

function truncate(s: string, max: number): string {
  return s.length > max ? `${s.slice(0, max - 1)}…` : s;
}

const SIZE = 240;
const CENTER = SIZE / 2;

/** Two-ring donut: inner = section, outer = product category. Hovering a
 *  slice dims the rest; every slice carries an accessible <title>. */
export function SpendSunburst({ data, loading, error }: Props) {
  const segments = useMemo(() => data?.segments ?? [], [data]);
  const [hover, setHover] = useState<number | null>(null);

  const { inner, outer } = useMemo(() => sunburstArcs(segments, 52, 88, 116), [segments]);

  return (
    <ChartCard
      title="Micro-Category Distribution"
      subtitle="Accepted bill lines by section and category."
      loading={loading}
      error={error}
      emptyMessage="No accepted bills in this period yet."
      empty={segments.length === 0}
    >
      <div className="sunburst-wrap">
        <svg
          width={SIZE}
          height={SIZE}
          viewBox={`0 0 ${SIZE} ${SIZE}`}
          role="img"
          aria-label="Spending by category section and category"
        >
          <g transform={`translate(${CENTER} ${CENTER})`}>
            {inner.map((slice, i) => {
              const dim = hover !== null && hover !== slice.segmentIndex;
              return (
                <g key={`i${i}`}>
                  <path
                    className={dim ? 'sunburst-segment dim' : 'sunburst-segment'}
                    d={slice.path}
                    fill={fill(slice.segmentIndex, false)}
                    onMouseEnter={() => setHover(slice.segmentIndex)}
                    onMouseLeave={() => setHover(null)}
                  >
                    <title>
                      {`${segments[slice.segmentIndex]?.section}: ${formatCents(
                        segments[slice.segmentIndex]?.total_cents ?? 0,
                        data?.currency ?? '',
                      )}`}
                    </title>
                  </path>
                  {/* Label only on wide slices to avoid crowding. */}
                  {slice.endAngle - slice.startAngle > 0.35 && (
                    <text
                      x={72 * Math.sin(midAngle(slice))}
                      y={-72 * Math.cos(midAngle(slice))}
                      textAnchor="middle"
                      dominantBaseline="middle"
                      fontSize="10"
                      fill="var(--text)"
                      pointerEvents="none"
                    >
                      {truncate(segments[slice.segmentIndex]?.section ?? '', 10)}
                    </text>
                  )}
                </g>
              );
            })}
            {outer.map((slice, i) => {
              const dim = hover !== null && hover !== slice.segmentIndex;
              return (
                <path
                  key={`o${i}`}
                  className={dim ? 'sunburst-segment dim' : 'sunburst-segment'}
                  d={slice.path}
                  fill={fill(slice.segmentIndex, true)}
                  onMouseEnter={() => setHover(slice.segmentIndex)}
                  onMouseLeave={() => setHover(null)}
                >
                  <title>
                    {`${slice.leaf?.name}: ${formatCents(slice.leaf?.total_cents ?? 0, data?.currency ?? '')}`}
                  </title>
                </path>
              );
            })}
          </g>
        </svg>
      </div>
    </ChartCard>
  );
}