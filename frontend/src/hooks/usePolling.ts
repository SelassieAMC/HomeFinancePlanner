import { useCallback, useEffect, useRef, useState } from 'react';

export interface PollingState<T> {
  data: T | null;
  loading: boolean;
  error: Error | null;
  /** True once maxMs elapsed without a terminal result. */
  timedOut: boolean;
  /** Stops the timer; the latest data stays available. */
  stop: () => void;
}

interface PollingOptions {
  /** Milliseconds between polls. */
  intervalMs: number;
  /** False stops the timer (and aborts an in-flight request). */
  enabled: boolean;
  /** Give-up deadline in ms; undefined polls until stopped. */
  maxMs?: number;
}

/**
 * Interval polling for short-lived waits (e.g. a scan being analyzed in the
 * background). Mirrors useAsync's cancelled-flag cleanup; stops itself on the
 * deadline, on unmount, or when enabled flips to false.
 */
export function usePolling<T>(
  fn: () => Promise<T>,
  { intervalMs, enabled, maxMs }: PollingOptions,
): PollingState<T> {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(enabled);
  const [error, setError] = useState<Error | null>(null);
  const [timedOut, setTimedOut] = useState(false);
  const [stopped, setStopped] = useState(false);

  const fnRef = useRef(fn);
  fnRef.current = fn;
  const abortRef = useRef<AbortController | null>(null);

  const stop = useCallback(() => setStopped(true), []);

  useEffect(() => {
    if (!enabled || stopped) {
      setLoading(false);
      return;
    }
    let cancelled = false;
    const start = Date.now();
    setLoading(true);
    setTimedOut(false);

    const poll = () => {
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;
      fnRef
        .current()
        .then((result) => {
          if (!cancelled) {
            setData(result);
            setError(null);
          }
        })
        .catch((err: unknown) => {
          if (!cancelled && !controller.signal.aborted) {
            setError(err instanceof Error ? err : new Error(String(err)));
          }
        });
    };

    poll();
    const timer = setInterval(() => {
      if (maxMs !== undefined && Date.now() - start >= maxMs) {
        if (!cancelled) setTimedOut(true);
        setStopped(true);
        return;
      }
      poll();
    }, intervalMs);

    return () => {
      cancelled = true;
      clearInterval(timer);
      abortRef.current?.abort();
    };
    // Re-run only when the gating flags change; fn is read via fnRef.
  }, [enabled, stopped, intervalMs, maxMs]);

  return { data, loading, error, timedOut, stop };
}