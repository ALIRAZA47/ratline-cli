import { useCallback, useEffect, useState } from 'react';

export type Theme = 'light' | 'dark';
const KEY = 'ratline-theme';

function currentTheme(): Theme {
  if (typeof document === 'undefined') return 'light';
  return document.documentElement.classList.contains('dark') ? 'dark' : 'light';
}

/**
 * Theme state. The initial class is set by an inline script in index.html so the
 * page never flashes the wrong palette; this hook only reads it back and toggles.
 *
 * An explicit choice is stored and wins. With nothing stored, the system
 * preference is followed live — including when it changes mid-session.
 */
export function useTheme() {
  const [theme, setTheme] = useState<Theme>(currentTheme);

  useEffect(() => {
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const onChange = () => {
      let stored: string | null = null;
      try {
        stored = localStorage.getItem(KEY);
      } catch {
        /* private mode */
      }
      if (stored === 'light' || stored === 'dark') return;
      apply(mq.matches ? 'dark' : 'light');
      setTheme(mq.matches ? 'dark' : 'light');
    };
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
  }, []);

  const toggle = useCallback(() => {
    const next: Theme = currentTheme() === 'dark' ? 'light' : 'dark';
    apply(next);
    try {
      localStorage.setItem(KEY, next);
    } catch {
      /* private mode: the choice lasts for this page only */
    }
    setTheme(next);
  }, []);

  return { theme, toggle };
}

function apply(theme: Theme) {
  document.documentElement.classList.toggle('dark', theme === 'dark');
  document.documentElement.style.colorScheme = theme;
}
