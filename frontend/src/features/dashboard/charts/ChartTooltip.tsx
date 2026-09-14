// Recharts' default tooltip is hardcoded white; this themed replacement is
// used as `content` on every tooltip.

interface TooltipEntry {
  name?: string | number;
  value?: number | string;
  color?: string;
  dataKey?: string | number;
  /** The row object this entry came from (shape depends on the chart). */
  payload?: unknown;
}

export interface ChartTooltipPayload {
  active?: boolean;
  label?: string | number;
  payload?: TooltipEntry[];
}

interface ChartTooltipProps extends ChartTooltipPayload {
  /** Formats the title (defaults to the raw label). */
  title?: (label: string | number) => string;
  /** Formats one row's value; defaults to the raw value. Receives the row's
   *  entry and the axis label (e.g. day or month). */
  format?: (value: number | string, entry: TooltipEntry, label?: string | number) => string;
  /** Entry names to hide (e.g. internal helper series). */
  hide?: Array<string | number>;
}

/** The recharts custom-tooltip contract: render null when inactive. */
export function ChartTooltip({ active, label, payload, title, format, hide }: ChartTooltipProps) {
  if (!active || !payload || payload.length === 0) return null;
  const entries = payload.filter((e) => !hide || !hide.includes(e.dataKey ?? ''));
  if (entries.length === 0) return null;
  return (
    <div className="chart-tooltip" role="status">
      {label !== undefined && <div className="chart-tooltip-title">{title ? title(label) : label}</div>}
      {entries.map((entry, i) => (
        <div className="chart-tooltip-row" key={i}>
          {entry.color && <span className="chart-legend-swatch" style={{ background: entry.color }} />}
          <span>{entry.name ?? entry.dataKey}</span>
          <span style={{ marginLeft: 'auto', fontVariantNumeric: 'tabular-nums' }}>
            {format && entry.value !== undefined ? format(entry.value, entry, label) : String(entry.value)}
          </span>
        </div>
      ))}
    </div>
  );
}