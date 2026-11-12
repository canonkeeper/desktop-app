import { useRef } from 'react';
import type { OverlayProps } from './Overlay';
import { Overlay } from './Overlay';
import { Portal, useOverlayScrim } from './Portal';
import { useTextBackgroundMark } from '../text-background';
import styles from './overlays.module.css';

export interface SheetProps extends Omit<OverlayProps, 'canDismiss' | 'kind'> {
  canDismiss: boolean;
  /**
   * A window of the sheet of text («Вид текста» of «Письмо»): painted in the background of the text, not the theme of
   * the site (specs/2026-11-12-ux11-dark-core.md TH1.4).
   */
  ofText?: boolean;
}

export function Sheet({ canDismiss, className, ofText = false, onDismiss, state = 'open', ...props }: SheetProps) {
  const pointerStart = useRef<number | null>(null);
  const scrim = useOverlayScrim();
  const background = useTextBackgroundMark();
  return (
    <Portal>
      <div
        className={`${styles.scrim} ${styles.sheetScrim}`}
        data-overlay-scrim=""
        {...(ofText ? background : {})}
        data-state={state}
        onPointerDown={(event) => {
          if (canDismiss && event.target === event.currentTarget) onDismiss();
        }}
        ref={scrim.ref}
      >
        <scrim.Provider value={scrim.scrim}>
          <Overlay
            {...props}
            canDismiss={canDismiss}
            className={`${styles.sheet}${className ? ` ${className}` : ''}`}
            kind="sheet"
            onDismiss={onDismiss}
            onPointerDown={(event) => { pointerStart.current = event.clientY; }}
            onPointerUp={(event) => {
              if (canDismiss && pointerStart.current !== null && event.clientY - pointerStart.current >= 64) onDismiss();
              pointerStart.current = null;
            }}
            state={state}
          />
        </scrim.Provider>
      </div>
    </Portal>
  );
}
