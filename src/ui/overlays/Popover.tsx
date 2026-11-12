import { useEffect, useRef, type HTMLAttributes, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react';
import { PopoverPortal } from './Portal';
import { usePresence } from './use-presence';
import styles from './overlays.module.css';

export interface PopoverProps extends Omit<HTMLAttributes<HTMLDivElement>, 'aria-label' | 'children' | 'role'> {
  /**
   * A window of actions, as the menu of the account (specs/2026-11-11-ux9-account.md AC3.1): its buttons and links are
   * walked as the items of a menu — the first takes the focus on opening, ↑↓, Home and End move between them. A menu is.
   */
  actions?: boolean;
  canDismiss?: boolean;
  children: ReactNode;
  /** Stands where it is rendered, not on top of the page: inside a sheet, whose page is inert. */
  inPlace?: boolean;
  label: string;
  onDismiss: () => void;
  open: boolean;
  role?: 'dialog' | 'menu';
}

/**
 * The items of a menu; the controls of a window of actions. A choice of one among several is one stop, its chosen
 * button: ←→ move inside it, as in any group of radio buttons («Тема» of the menu of the account).
 */
const menuItems = ':is([role="menuitem"], [role="menuitemcheckbox"]):not([aria-disabled="true"])';
const actionItems = ':is(button, a[href], [role="menuitem"], [role="menuitemcheckbox"]):not(:disabled):not([aria-disabled="true"])';
/** A button of a choice that is not the chosen one: ←→ of the group reach it, ↑↓ of the window pass it. */
const unchosen = (item: HTMLElement) => item.getAttribute('role') === 'radio' && item.getAttribute('aria-checked') !== 'true';

/**
 * ↓ and ↑ on the button of a menu open it, as Enter and Space do (AC3.1, the pattern of a menu button): the menu then puts
 * the focus on its first item. One handler for the buttons of «⋯» and of the account.
 */
export function onMenuButtonKey(open: boolean, show: () => void) {
  return (event: ReactKeyboardEvent<HTMLElement>) => {
    if (open || (event.key !== 'ArrowDown' && event.key !== 'ArrowUp')) return;
    event.preventDefault();
    show();
  };
}

/**
 * A window next to its button (a menu, the menu of the account, a rule): Esc and a press outside close it and give the
 * focus back; Tab goes on from its button and closes it. A menu and a window of actions are walked by the keys (AC3.1).
 */
export function Popover({ actions = false, canDismiss = true, children, className, inPlace = false, label, onDismiss, open, role = 'dialog', ...props }: PopoverProps) {
  const presence = usePresence(open);
  const popoverRef = useRef<HTMLDivElement>(null);
  const onDismissRef = useRef(onDismiss);
  onDismissRef.current = onDismiss;

  useEffect(() => {
    if (!open) return;
    const trigger = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const walked = role === 'menu' || actions;
    const enabledItems = () => [...(popoverRef.current?.querySelectorAll<HTMLElement>(role === 'menu' ? menuItems : actionItems) ?? [])]
      .filter((item) => !unchosen(item));
    if (walked) enabledItems()[0]?.focus();

    function handlePointerDown(event: PointerEvent) {
      if (canDismiss && event.target instanceof Node && !popoverRef.current?.contains(event.target)) onDismissRef.current();
    }

    function handleKeyDown(event: KeyboardEvent) {
      const items = enabledItems();
      const current = document.activeElement instanceof HTMLElement ? items.indexOf(document.activeElement) : -1;
      if (walked && current >= 0 && ['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
        event.preventDefault();
        const target = event.key === 'Home'
          ? items[0]
          : event.key === 'End'
            ? items.at(-1)
            : items[(current + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length];
        target?.focus();
        return;
      }
      const focusInside = popoverRef.current?.contains(document.activeElement) ?? false;
      if (event.key === 'Tab' && canDismiss && (focusInside || document.activeElement === trigger)) {
        if (focusInside) trigger?.focus();
        onDismissRef.current();
        return;
      }
      if (event.key === 'Escape' && canDismiss) {
        event.stopPropagation();
        onDismissRef.current();
        trigger?.focus();
      }
    }

    document.addEventListener('pointerdown', handlePointerDown);
    document.addEventListener('keydown', handleKeyDown);
    return () => {
      document.removeEventListener('pointerdown', handlePointerDown);
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [actions, canDismiss, open, role]);

  if (!presence.mounted) return null;

  const box = (
    <div
      {...props}
      aria-label={label}
      className={`${styles.popover}${className ? ` ${className}` : ''}`}
      data-pop=""
      data-state={presence.status}
      ref={popoverRef}
      role={role}
    >
      {children}
    </div>
  );
  return inPlace ? box : <PopoverPortal>{box}</PopoverPortal>;
}
