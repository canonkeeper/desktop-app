import { useRef } from 'react';
import type { OverlayProps } from './Overlay';
import { Overlay } from './Overlay';
import { Portal } from './Portal';
import styles from './overlays.module.css';

export interface SheetProps extends Omit<OverlayProps, 'canDismiss' | 'kind'> {
  canDismiss: boolean;
}

export function Sheet({ canDismiss, className, onDismiss, state = 'open', ...props }: SheetProps) {
  const pointerStart = useRef<number | null>(null);
  return (
    <Portal>
      <div
        className={`${styles.scrim} ${styles.sheetScrim}`}
        data-overlay-scrim=""
        data-state={state}
        onPointerDown={(event) => {
          if (canDismiss && event.target === event.currentTarget) onDismiss();
        }}
      >
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
      </div>
    </Portal>
  );
}
