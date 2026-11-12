import { useEffect, useState } from 'react';

function useMedia(query: string) {
  const [matches, setMatches] = useState(() => typeof window.matchMedia === 'function' && window.matchMedia(query).matches);
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return undefined;
    const media = window.matchMedia(query);
    const update = () => setMatches(media.matches);
    update();
    media.addEventListener('change', update);
    return () => media.removeEventListener('change', update);
  }, [query]);
  return matches;
}

/**
 * A screen at most `maxWidth` CSS pixels wide, a phone (≤ 760 px) by default: the helper of a workshop goes to a bar and
 * a bottom sheet (image-workshop IW9.4, writer-workshop W4.6). A scaled desktop screen is narrower in CSS pixels.
 */
export function useNarrowScreen(maxWidth = 760) {
  return useMedia(`(max-width: ${maxWidth}px)`);
}

/**
 * A window at most `maxHeight` CSS pixels high (writer-workshop W4.6): a laptop at a system scale of 125–150 % leaves the
 * browser 500–730 px, and a panel beside the page keeps its room for what matters there.
 */
export function useLowScreen(maxHeight = 760) {
  return useMedia(`(max-height: ${maxHeight}px)`);
}

/**
 * Under a finger or below 900 px every target is 44 px (PR1.4, specs/2026-11-11-ux9-shelf-chat.md SC2.1): the query of the
 * rules of the controls, for a control that places its targets by script — the marks of the scale of the map.
 */
export function useTouchTargets() {
  return useMedia('(pointer: coarse), (max-width: 899px)');
}

/**
 * A mouse and a keyboard at hand (specs/2026-11-11-ux9-shelf-chat.md SC2.3, П-19): only there are the keys of a window
 * named. A phone and a tablet under a finger hear of no Esc.
 */
export function useKeyboardAtHand() {
  return useMedia('(hover: hover) and (pointer: fine)');
}
