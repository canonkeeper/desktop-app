import type { CSSProperties, ReactNode, RefObject } from 'react';
import { Overlay } from './Overlay';
import { Portal, useOverlayScrim } from './Portal';
import type { PresenceStatus } from './use-presence';
import styles from './overlays.module.css';

export interface DialogProps {
  canDismiss?: boolean;
  children: ReactNode;
  dismissOnScrim?: boolean;
  /** `false` for a window the address opens: its own «Назад» closes it (UC4). */
  history?: boolean;
  initialFocusRef?: RefObject<HTMLElement | null>;
  label: string;
  onDismiss: () => void;
  /** The shape of a panel that is not a card — a full-screen lightbox, a side sheet; the panel itself stays. */
  panelClassName?: string;
  returnFocusRef?: RefObject<HTMLElement | null>;
  scrimClassName?: string;
  state?: PresenceStatus;
  /**
   * `card` (the default): the kit draws the panel. `content`: the content is the whole surface — a full-screen
   * lightbox, a side sheet, a sheet on a phone; its root class paints an opaque background (dialog-surface.test.ts).
   */
  surface?: 'card' | 'content';
  /** The width of the card in px, never wider than the window (480 by default). */
  width?: number;
}

export function Dialog({
  canDismiss,
  children,
  dismissOnScrim = false,
  history,
  initialFocusRef,
  label,
  onDismiss,
  panelClassName,
  returnFocusRef,
  scrimClassName,
  state = 'open',
  surface = 'card',
  width,
}: DialogProps) {
  const overlayCanDismiss = canDismiss ?? true;
  const scrimCanDismiss = canDismiss ?? dismissOnScrim;
  const scrim = useOverlayScrim();
  return (
    <Portal>
      <div
        className={`${styles.scrim}${scrimClassName ? ` ${scrimClassName}` : ''}`}
        data-overlay-scrim=""
        data-state={state}
        onPointerDown={(event) => {
          if (scrimCanDismiss && event.target === event.currentTarget) onDismiss();
        }}
        ref={scrim.ref}
      >
        {/* The panel is the kit's: no dialog floats over the page without one (narrow-windows N7.1). */}
        <scrim.Provider value={scrim.scrim}>
          <Overlay
            className={`${styles.dialogPanel}${surface === 'content' ? ` ${styles.contentSurface}` : ''}${panelClassName ? ` ${panelClassName}` : ''}`}
            {...(width ? { style: { '--dialog-width': `${width}px` } as CSSProperties } : {})}
            canDismiss={overlayCanDismiss}
            history={history}
            initialFocusRef={initialFocusRef}
            kind="dialog"
            label={label}
            onDismiss={onDismiss}
            returnFocusRef={returnFocusRef}
            state={state}
          >
            {children}
          </Overlay>
        </scrim.Provider>
      </div>
    </Portal>
  );
}
