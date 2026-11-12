import { useEffect, useState } from 'react';

/**
 * What the keyboard of a phone covers (specs/2026-11-05-drawing-one-screen.md D11.3; specs/2026-11-11-ux5-phone.md PH2.1):
 * the visual viewport is lower than the page, so what stands at the bottom of a workplace — the field of an edit of a
 * drawing, the menu of a selection of «Письмо» — lifts above the keyboard. Zero on a wide screen and without a keyboard.
 */
export function useKeyboardInset(phone: boolean) {
  const [inset, setInset] = useState(0);
  useEffect(() => {
    const viewport = window.visualViewport;
    if (!phone || !viewport) return undefined;
    const update = () => setInset(Math.max(0, Math.round(window.innerHeight - viewport.height - viewport.offsetTop)));
    update();
    viewport.addEventListener('resize', update);
    viewport.addEventListener('scroll', update);
    return () => {
      viewport.removeEventListener('resize', update);
      viewport.removeEventListener('scroll', update);
      setInset(0);
    };
  }, [phone]);
  return inset;
}
