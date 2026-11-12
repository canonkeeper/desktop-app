import type { OverlayProps } from './Overlay';
import { Overlay } from './Overlay';
import { Portal, useOverlayScrim } from './Portal';
import styles from './overlays.module.css';

export interface DrawerProps extends Omit<OverlayProps, 'canDismiss' | 'kind'> {
  canDismiss: boolean;
  side?: 'left' | 'right';
  size?: 'default' | 'reading';
}

export function Drawer({ canDismiss, className, onDismiss, state = 'open', side = 'left', size = 'default', ...props }: DrawerProps) {
  const scrim = useOverlayScrim();
  return (
    <Portal>
      <div
        className={`${styles.scrim} ${styles.drawerScrim}`}
        data-overlay-scrim=""
        data-side={side}
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
            className={`${styles.drawer}${className ? ` ${className}` : ''}`}
            kind="drawer"
            data-size={size}
            onDismiss={onDismiss}
            state={state}
          />
        </scrim.Provider>
      </div>
    </Portal>
  );
}
