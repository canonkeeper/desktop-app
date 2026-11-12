/** The frames the mark stays: the new colours stand, nothing is left to animate (specs/2026-11-12-ux11-dark-core.md TH3.3). */
let frames: number[] = [];

/**
 * Changes the colours of the page in one frame: while the theme of the site or the background of the text changes, the
 * mark on <html> takes the transitions of colour away (src/styles/base.css), or for 130 ms a word would keep the colour
 * of the old theme over the background of the new one. The mark goes after two frames.
 */
export function withoutColorTransitions(change: () => void, root: HTMLElement | null = typeof document === 'undefined' ? null : document.documentElement) {
  if (!root || typeof requestAnimationFrame !== 'function') {
    change();
    return;
  }
  for (const frame of frames) cancelAnimationFrame(frame);
  root.setAttribute('data-theme-switching', '');
  change();
  const outer = requestAnimationFrame(() => {
    const inner = requestAnimationFrame(() => {
      root.removeAttribute('data-theme-switching');
      frames = [];
    });
    frames = [inner];
  });
  frames = [outer];
}
