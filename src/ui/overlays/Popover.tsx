import { useEffect, useRef, type HTMLAttributes, type ReactNode } from 'react';
import { Portal } from './Portal';
import { usePresence } from './use-presence';
import styles from './overlays.module.css';

export interface PopoverProps extends Omit<HTMLAttributes<HTMLDivElement>, 'aria-label' | 'children' | 'role'> {
  canDismiss?: boolean;
  children: ReactNode;
  /** Stands where it is rendered, not on top of the page: inside a sheet, whose page is inert. */
  inPlace?: boolean;
  label: string;
  onDismiss: () => void;
  open: boolean;
  role?: 'dialog' | 'menu';
}

export function Popover({ canDismiss = true, children, className, inPlace = false, label, onDismiss, open, role = 'dialog', ...props }: PopoverProps) {
  const presence = usePresence(open);
  const popoverRef = useRef<HTMLDivElement>(null);
  const onDismissRef = useRef(onDismiss);
  onDismissRef.current = onDismiss;

  useEffect(() => {
    if (!open) return;
    const trigger = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const enabledItems = () => [...(popoverRef.current?.querySelectorAll<HTMLElement>('[role="menuitem"]:not([aria-disabled="true"])') ?? [])];
    if (role === 'menu') enabledItems()[0]?.focus();

    function handlePointerDown(event: PointerEvent) {
      if (canDismiss && event.target instanceof Node && !popoverRef.current?.contains(event.target)) onDismissRef.current();
    }

    function handleKeyDown(event: KeyboardEvent) {
      const items = enabledItems();
      const current = document.activeElement instanceof HTMLElement ? items.indexOf(document.activeElement) : -1;
      if (role === 'menu' && current >= 0 && ['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
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
  }, [canDismiss, open, role]);

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
  return inPlace ? box : <Portal>{box}</Portal>;
}
