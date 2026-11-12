import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type FocusEvent, type KeyboardEvent, type MouseEvent, type ReactNode, type Ref } from 'react';
import styles from './controls.module.css';

/**
 * Why a button is closed now and the way out (specs/2026-11-10-ux2-patterns-refusals.md PR1.1; П-5): «Сначала напишите
 * вопрос», «Глава с помощником на „Бесплатно“ — одна…» with «Тариф». The page gives the words; the button says them.
 */
export interface DisabledReason {
  text: string;
  /** The one way out beside the reason: «Тариф», «Подтвердить почту», «Открыть замысел». */
  action?: { label: string; onClick: () => void };
  /**
   * The id of a bar that already says this reason where the author works, before he presses — the bar of «Рисование»
   * (specs/2026-11-10-ux1-drawing.md DX2.4): the button is described by it and a press lights it (`onRefusedPress`)
   * instead of a plate of its own.
   */
  at?: string;
  /**
   * The page's own place of messages says the reason by a press — the workplace of «Рисование», whose messages stand
   * beside the drawing and cover neither it nor the field of the helper (specs/2026-11-05-drawing-one-screen.md D5.7):
   * no plate of the button's own.
   */
  say?: (text: string) => void;
}

/** A reason in words, with its way out, or none: the button is open. */
export type DisabledReasonValue = string | DisabledReason | null | undefined;

interface ReasonHandlers<T extends HTMLElement> {
  'aria-describedby'?: string | undefined;
  onBlur?: ((event: FocusEvent<T>) => void) | undefined;
  onClick?: ((event: MouseEvent<T>) => void) | undefined;
  onFocus?: ((event: FocusEvent<T>) => void) | undefined;
  onKeyDown?: ((event: KeyboardEvent<T>) => void) | undefined;
}

/** The gap between the button and its plate, and the side gutter of a narrow window. */
const gap = 6;
const gutter = 16;
/** Below this width the reason is a line across the window under the button (the board 15·04, 390). */
const narrow = 600;

/** One reason stands at a time: a press of another closed button takes the last plate away. */
let closeShown: (() => void) | null = null;

function keyboardFocus(element: Element) {
  try {
    return element.matches(':focus-visible');
  } catch {
    return true;
  }
}

/**
 * The closed state of a button (PR1.1): `aria-disabled` instead of `disabled`, so the focus and the press reach it; a
 * press, Enter and the focus of the keyboard show the reason beside it, linked by `aria-describedby`; the action of the
 * button does not run and its form is not sent. On a narrow window the reason stands across it under the button.
 */
export function useDisabledReason<T extends HTMLElement>(reason: DisabledReasonValue, onRefusedPress: (() => void) | undefined, handlers: ReasonHandlers<T>, ref?: Ref<T>) {
  const value = typeof reason === 'string' ? { text: reason } : reason ?? null;
  const id = useId();
  const [open, setOpen] = useState(false);
  const anchor = useRef<T | null>(null);
  const plate = useRef<HTMLSpanElement | null>(null);
  // The press itself may move the focus to where the author mends it (the field of the question): that blur is not his
  // leaving, the plate stays until he presses elsewhere or the reason goes.
  const pressing = useRef(false);
  const shown = Boolean(value && open);

  const closed = Boolean(value);
  useEffect(() => {
    if (!closed) setOpen(false);
  }, [closed]);

  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    closeShown?.();
    closeShown = close;
    return () => {
      if (closeShown === close) closeShown = null;
    };
  }, [open]);

  useLayoutEffect(() => {
    const element = plate.current;
    const button = anchor.current;
    if (!shown || !element || !button) return;
    // The top layer keeps the plate over a sheet, a scroller that clips and a transformed parent.
    if (typeof element.showPopover === 'function' && !element.matches(':popover-open')) element.showPopover();
    const place = () => {
      const box = button.getBoundingClientRect();
      const wide = window.innerWidth >= narrow;
      const width = wide ? Math.min(320, window.innerWidth - 2 * gutter) : window.innerWidth - 2 * gutter;
      element.style.width = `${width}px`;
      const left = wide ? Math.min(Math.max(box.left, gutter), window.innerWidth - width - gutter) : gutter;
      const below = box.bottom + gap;
      const height = element.offsetHeight;
      const top = below + height > window.innerHeight - gap && box.top - gap - height > gap ? box.top - gap - height : below;
      element.style.left = `${left}px`;
      element.style.top = `${top}px`;
    };
    place();
    const close = (event: Event) => {
      if (event.target instanceof Node && (button.contains(event.target) || element.contains(event.target))) return;
      setOpen(false);
    };
    window.addEventListener('scroll', place, true);
    window.addEventListener('resize', place);
    document.addEventListener('pointerdown', close);
    return () => {
      window.removeEventListener('scroll', place, true);
      window.removeEventListener('resize', place);
      document.removeEventListener('pointerdown', close);
      if (typeof element.hidePopover === 'function' && element.matches(':popover-open')) element.hidePopover();
    };
  }, [shown]);

  // The page's own ref still gets the button: «Поиск по книгам» gives the focus back through it.
  const setAnchor = useCallback((node: T | null) => {
    anchor.current = node;
    if (typeof ref === 'function') ref(node);
    else if (ref) ref.current = node;
  }, [ref]);

  const own = value && !value.at && !value.say;
  // The words of the reason are the description of the button itself: they stand once on the page, in the plate when it
  // is shown; a bar that already says them describes the button by its id.
  const describedBy = [...new Set([handlers['aria-describedby'], value?.at].filter(Boolean))].join(' ') || undefined;
  // Without a reason the page's own attributes stand as it gave them.
  const closedProps = value ? { 'aria-description': value.at ? undefined : value.text, 'aria-disabled': true, 'data-refused': '' } : {};
  const props = {
    ...closedProps,
    ref: setAnchor,
    'aria-describedby': describedBy,
    onBlur: (event: FocusEvent<T>) => {
      handlers.onBlur?.(event);
      if (pressing.current) return;
      if (!(event.relatedTarget instanceof Node && plate.current?.contains(event.relatedTarget))) setOpen(false);
    },
    onClick: (event: MouseEvent<T>) => {
      if (!value) {
        handlers.onClick?.(event);
        return;
      }
      // A closed submit button sends nothing: the press only says why.
      event.preventDefault();
      if (own) setOpen(true);
      value.say?.(value.text);
      pressing.current = true;
      try {
        onRefusedPress?.();
      } finally {
        pressing.current = false;
      }
    },
    onFocus: (event: FocusEvent<T>) => {
      handlers.onFocus?.(event);
      if (own && keyboardFocus(event.currentTarget)) setOpen(true);
    },
    onKeyDown: (event: KeyboardEvent<T>) => {
      handlers.onKeyDown?.(event);
      if (value && open && event.key === 'Escape') {
        event.stopPropagation();
        setOpen(false);
      }
    },
  };

  const reasonPlate: ReactNode = own && shown ? (
    <span className={styles.reason} data-reason="" id={id} popover="manual" ref={plate}
      onKeyDown={(event) => {
        if (event.key !== 'Escape') return;
        event.stopPropagation();
        setOpen(false);
        anchor.current?.focus();
      }}>
      <span>{value.text}</span>
      {value.action ? (
        <button className={styles.reasonAction} onClick={() => {
          setOpen(false);
          value.action?.onClick();
        }} type="button">{value.action.label}</button>
      ) : null}
    </span>
  ) : null;

  return { props, plate: reasonPlate };
}
