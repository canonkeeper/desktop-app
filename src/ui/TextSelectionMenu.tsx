import { Brush, Copy, Lock, MessageCircleQuestion, Search, StickyNote } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, type ReactNode } from 'react';
import { Portal } from './overlays/Portal';
import { useTextBackgroundMark } from './text-background';
import styles from './TextSelectionMenu.module.css';

/**
 * The actions of a selection (specs/2026-11-10-ux7-reader.md RD3.1; Pattern/Text Selection/Menu): one row over the
 * selection — under it when there is no room above, over the bottom bar of a phone — of words under their icons; it never
 * covers the selected text and never takes part in the layout of the reading. The room above starts under the `ceiling` a
 * page names: the reader keeps its line and the title of the chapter free (specs/2026-11-11-ux2-polish.md PL4.1). The reader
 * names «Спросить · Заметка · Найти · Копировать · Нарисовать», the chat «В заметки · Копировать»: each page gives its own
 * items.
 */
export function TextSelectionMenu({ ceiling, rect, onAddNote, noteWord = 'В заметки', onAsk, onCopy, onDraw, drawLocked = false, onFind, onDismiss }: {
  rect: Pick<DOMRect, 'left' | 'top' | 'bottom'> & Partial<Pick<DOMRect, 'right'>>;
  /** Where the room over the selection starts on the screen, read when the row stands; the top of the window without it. */
  ceiling?: () => number;
  /** «Спросить»: a question of the chat about the words selected. */
  onAsk?: () => void;
  onAddNote?: () => void;
  /** The word of the note: «Заметка» in the reader, «В заметки» in the chat. */
  noteWord?: string;
  /** «Нарисовать» (illustrations R8.9); locked, it leads to the page of the feature. */
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
    // A phone keeps its bottom bar of the reader (and the keyboard) free: the row stands over it.
    const bottomBar = document.querySelector<HTMLElement>('[data-reader-phone-bar]');
    const floor = bottomBar && bottomBar.offsetParent !== null ? bottomBar.getBoundingClientRect().top : height;
    const roof = Math.max(0, ceiling?.() ?? 0) + 8;
    const above = rect.top - menu.offsetHeight - 8;
    const below = rect.bottom + 8;
    const center = rect.right === undefined ? rect.left : (rect.left + rect.right) / 2 - menu.offsetWidth / 2;
    const top = above >= roof ? above : below + menu.offsetHeight <= floor - 8 ? below : Math.max(8, floor - menu.offsetHeight - 8);
    Object.assign(menu.style, {
      left: `${Math.max(8, Math.min(center, width - menu.offsetWidth - 8))}px`,
      top: `${Math.max(8, top)}px`,
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
    document.addEventListener('keydown', key, true);
    return () => {
      window.removeEventListener('resize', close);
      window.removeEventListener('scroll', scroll, true);
      document.removeEventListener('pointerdown', outside);
      document.removeEventListener('keydown', key, true);
    };
  }, []);
  // Over the text of the reader the row follows the background of the text (specs/2026-11-12-ux11-dark-core.md TH1.4).
  const background = useTextBackgroundMark();
  const item = (label: string, icon: ReactNode, run: () => void) => (
    <button className={styles.item} key={label} onClick={run} type="button">{icon}<span>{label}</span></button>
  );
  return <Portal><div aria-label="Действия с выделением" className={styles.menu} {...background}
    onPointerDown={(event) => { if (event.target instanceof Element && event.target.closest('button')) event.preventDefault(); }} ref={ref} role="toolbar">
    {onAsk ? item('Спросить', <MessageCircleQuestion aria-hidden="true" size={18} />, onAsk) : null}
    {onAddNote ? item(noteWord, <StickyNote aria-hidden="true" size={18} />, onAddNote) : null}
    {onFind ? item('Найти', <Search aria-hidden="true" size={18} />, onFind) : null}
    {item('Копировать', <Copy aria-hidden="true" size={18} />, onCopy)}
    {onDraw ? item('Нарисовать', drawLocked ? <Lock aria-hidden="true" size={18} /> : <Brush aria-hidden="true" size={18} />, onDraw) : null}
  </div></Portal>;
}
