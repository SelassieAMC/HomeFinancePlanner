// Underline-style tab strip for pages that split parallel functionality
// (activity vs. entry forms, connector config vs. normalization jobs). The
// active tab is marked by the accent underline; content switching stays with
// the caller. Generic over the tab key type so onChange feeds a typed state.
export interface TabDef<K extends string = string> {
  key: K;
  label: string;
}

export function Tabs<K extends string>({
  tabs,
  active,
  onChange,
  ariaLabel,
}: {
  tabs: TabDef<K>[];
  active: K;
  onChange: (key: K) => void;
  ariaLabel: string;
}) {
  return (
    <div className="tabs" role="tablist" aria-label={ariaLabel}>
      {tabs.map((t) => (
        <button
          key={t.key}
          type="button"
          role="tab"
          aria-selected={active === t.key}
          className={t.key === active ? 'tab-btn active' : 'tab-btn'}
          onClick={() => onChange(t.key)}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}