import { Copy, Lock, Palette, Search, StickyNote } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef } from 'react';
import { Button } from './controls/Button';
import { Portal } from './overlays/Portal';
import styles from './TextSelectionMenu.module.css';

/** Floating selection actions never participate in the reading layout. */
export function TextSelectionMenu({ rect, onAddNote, onCopy, onDraw, drawLocked = false, onFind, onDismiss }: {
  rect: Pick<DOMRect, 'left' | 'top' | 'bottom'>;
  onAddNote?: () => void;
  /** «Нарисовать эту сцену» (illustrations R8.9); locked, it leads to the page of the feature. */
  onDraw?: () => void;
  drawLocked?: boolean;
  onCopy: () => void;
  onFind?: () => void;
  onDismiss: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const dismiss = useRef(onDismiss);
  dismiss.current = onDismiss;
  useLayoutEffect(() => {
    const menu = ref.current;
    if (!menu) return;
    const width = document.documentElement.clientWidth || window.innerWidth;
    const height = document.documentElement.clientHeight || window.innerHeight;
    const above = rect.top - menu.offsetHeight - 8;
    Object.assign(menu.style, {
      left: `${Math.max(8, Math.min(rect.left, width - menu.offsetWidth - 8))}px`,
      top: `${Math.max(8, Math.min(above >= 8 ? above : rect.bottom + 8, height - menu.offsetHeight - 8))}px`,
    });
  }, [rect]);
  useEffect(() => {
    const trigger = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const close = () => dismiss.current();
    const scroll = (event: Event) => { if (!(event.target instanceof Node) || !ref.current?.contains(event.target)) close(); };
    const outside = (event: PointerEvent) => {
      if (event.target instanceof Node && !ref.current?.contains(event.target)) close();
    };
    const key = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close(); trigger?.focus({ preventScroll: true }); }
      if (event.key !== 'Tab') return;
      const buttons = [...(ref.current?.querySelectorAll<HTMLButtonElement>('button') ?? [])];
      if (!ref.current?.contains(document.activeElement)) {
        event.preventDefault(); (event.shiftKey ? buttons.at(-1) : buttons[0])?.focus();
      } else if ((!event.shiftKey && document.activeElement === buttons.at(-1))
        || (event.shiftKey && document.activeElement === buttons[0])) {
        close();
      }
    };
    window.addEventListener('resize', close);
    window.addEventListener('scroll', scroll, true);
    document.addEventListener('pointerdown', outside);
    document.addEventListener('keydown', key);
    return () => {
      window.removeEventListener('resize', close);
      window.removeEventListener('scroll', scroll, true);
      document.removeEventListener('pointerdown', outside);
      document.removeEventListener('keydown', key);
    };
  }, []);
  return <Portal><div aria-label="Действия с выделением" className={styles.menu}
    onPointerDown={(event) => { if (event.target instanceof Element && event.target.closest('button')) event.preventDefault(); }} ref={ref} role="toolbar">
    {onDraw ? <Button onClick={onDraw} size="touch" variant="ghost">
      {drawLocked ? <Lock aria-hidden="true" size={15} /> : <Palette aria-hidden="true" size={15} />}Нарисовать эту сцену
    </Button> : null}
    {onAddNote ? <Button onClick={onAddNote} size="touch" variant="ghost"><StickyNote aria-hidden="true" size={15} />В заметки</Button> : null}
    {onFind ? <Button onClick={onFind} size="touch" variant="ghost"><Search aria-hidden="true" size={15} />Найти в книге</Button> : null}
    <Button onClick={onCopy} size="touch" variant="ghost"><Copy aria-hidden="true" size={15} />Копировать</Button>
  </div></Portal>;
}
