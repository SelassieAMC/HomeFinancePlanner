/** The 8px thin progress bar used by the planning tables and compact rows. */
export function ProgressBar({ pct, over, done }: { pct: number; over?: boolean; done?: boolean }) {
  return (
    <div className="progress-track">
      <div
        className={over ? 'progress-fill over' : done ? 'progress-fill done' : 'progress-fill'}
        style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
      />
    </div>
  );
}