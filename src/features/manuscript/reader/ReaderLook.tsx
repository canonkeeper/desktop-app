import { X } from 'lucide-react';
import { useEffect, useRef, type ReactNode, type RefObject } from 'react';
import { IconButton } from '../../../ui/controls/IconButton';
import { Overlay } from '../../../ui/overlays/Overlay';
import { Portal } from '../../../ui/overlays/Portal';
import type { PresenceStatus } from '../../../ui/overlays/use-presence';
import { useTextBackgroundMark } from '../../../ui/text-background';
import styles from './ReaderLook.module.css';

/**
 * The window «Аа» (specs/2026-11-10-ux7-reader.md RD1.4; Pattern/Reader/Appearance, board 40·27): under its button on a wide
 * screen, a sheet from below on a phone. It lives in the address (`view=open`): Esc, «Назад» and a press outside close it.
 */
export function ReaderLook({ children, onClose, returnFocusRef, sheet, state }: {
  children: ReactNode;
  onClose: () => void;
  /** The button «Аа»: a press on it is not a press outside. */
  returnFocusRef: RefObject<HTMLButtonElement | null>;
  sheet: boolean;
  state: PresenceStatus;
}) {
  const ref = useRef<HTMLDivElement>(null);
  // A sheet of a phone stands outside the reader: it follows the background of the text (specs/2026-11-12-ux11-dark-core.md TH1.4).
  const background = useTextBackgroundMark();
  const close = useRef(onClose);
  close.current = onClose;
  useEffect(() => {
    if (sheet) return undefined;
    ref.current?.querySelector<HTMLElement>('button, [role="radio"][aria-checked="true"]')?.focus({ preventScroll: true });
    const outside = (event: PointerEvent) => {
      if (!(event.target instanceof Node) || ref.current?.contains(event.target) || returnFocusRef.current?.contains(event.target)) return;
      close.current();
    };
    document.addEventListener('pointerdown', outside);
    return () => document.removeEventListener('pointerdown', outside);
  }, [returnFocusRef, sheet]);
  const head = (
    <div className={styles.head}>
      <h2 className={styles.title}>Вид текста</h2>
      <IconButton aria-label="Закрыть вид текста" onClick={onClose} size="touch"><X aria-hidden="true" size={18} /></IconButton>
    </div>
  );
  if (!sheet) {
    return <div aria-label="Вид текста" className={styles.popover} data-state={state} ref={ref} role="dialog">{head}{children}</div>;
  }
  return (
    <Portal>
      <div className={styles.sheetLayer} data-overlay-scrim="" {...background} data-state={state} onClick={(event) => event.target === event.currentTarget && onClose()}>
        <Overlay className={styles.sheet} history={false} kind="sheet" label="Вид текста" onDismiss={onClose} returnFocusRef={returnFocusRef} state={state}>
          {head}{children}
        </Overlay>
      </div>
    </Portal>
  );
}
