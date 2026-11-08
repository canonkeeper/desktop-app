import { useEffect, useState } from 'react';

const readViewport = () => ({
  compact: window.innerWidth <= 760 || window.innerHeight <= 760,
  overlay: window.innerWidth < 1200 || window.innerHeight <= 760,
  short: window.innerHeight <= 760,
});

/** Choose Notes composition by usable viewport, including height (compact Notes UC2). */
export function useNotesViewport() {
  const [viewport, setViewport] = useState(readViewport);
  useEffect(() => {
    const resize = () => setViewport(readViewport());
    window.addEventListener('resize', resize);
    return () => window.removeEventListener('resize', resize);
  }, []);
  return viewport;
}
