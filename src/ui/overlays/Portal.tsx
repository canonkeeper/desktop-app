import { createContext, useContext, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';

export function Portal({ children }: { children: ReactNode }) {
  return createPortal(children, document.body);
}

/** The scrim of the window — a dialog, a drawer, a sheet — its content is rendered in. */
const OverlayScrimContext = createContext<HTMLElement | null>(null);

/**
 * The scrim of a window, told to what the window holds: `ref` goes on the scrim, `Provider` around its content. A menu of
 * the page stands under the scrim of an open window and is not reached (the owner 11.11: «⋯» of a chat in the drawer
 * «Чаты» did nothing); opened from the window it stands in that scrim — over the window, at the same place of the
 * browser's window, since the scrim covers it whole.
 */
export function useOverlayScrim() {
  const [scrim, setScrim] = useState<HTMLElement | null>(null);
  return { ref: setScrim, scrim, Provider: OverlayScrimContext.Provider };
}

/** A popover goes over the window it is opened from, else onto the page. */
export function PopoverPortal({ children }: { children: ReactNode }) {
  const scrim = useContext(OverlayScrimContext);
  return createPortal(children, scrim ?? document.body);
}
