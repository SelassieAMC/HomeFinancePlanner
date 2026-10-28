import { useCallback, useEffect, useState } from 'react';
import {
  apply,
  persist,
  readStoredChoice,
  resolve,
  type ResolvedTheme,
  type ThemeChoice,
} from '../lib/theme';

// Module-level store so every consumer shares one state: the sidebar toggle,
// the profile card and any future switch stay in lockstep through the
// subscription, and the matchMedia listener lives exactly once. initialized
// from storage at module load (index.html already applied the resolved
// theme to <html>; this just mirrors the stored choice after first paint).
let storedChoice: ThemeChoice = 'system';
const listeners = new Set<() => void>();
let mediaSubscribed = false;

if (typeof window !== 'undefined') {
  storedChoice = readStoredChoice();
  subscribeToOs();
}

function subscribeToOs() {
  if (mediaSubscribed) return;
  mediaSubscribed = true;
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    if (storedChoice === 'system') apply(storedChoice);
    // Resolve only changes under 'system' — notify regardless so consumers
    // that show the resolved value stay correct.
    listeners.forEach((notify) => notify());
  });
}

export function useTheme(): {
  choice: ThemeChoice;
  resolved: ResolvedTheme;
  setChoice: (choice: ThemeChoice) => void;
} {
  const [, force] = useState(0);
  useEffect(() => {
    const notify = () => force((n) => n + 1);
    listeners.add(notify);
    return () => {
      listeners.delete(notify);
    };
  }, []);
  const resolved = resolve(storedChoice);
  const setChoice = useCallback((next: ThemeChoice) => {
    storedChoice = next;
    persist(next);
    listeners.forEach((notify) => notify());
  }, []);
  return { choice: storedChoice, resolved, setChoice };
}