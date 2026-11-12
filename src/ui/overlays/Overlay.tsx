import {
  type HTMLAttributes,
  type KeyboardEvent,
  type ReactNode,
  type RefObject,
  useEffect,
  useRef,
} from 'react';
import { useOverlayHistory } from './overlay-history';
import type { PresenceStatus } from './use-presence';
import styles from './overlays.module.css';

export type OverlayKind = 'dialog' | 'drawer' | 'sheet';

const focusableSelector = 'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [contenteditable="true"], [tabindex]:not([tabindex="-1"])';

interface OverlayScope {
  root: HTMLDivElement;
  modal: RefObject<boolean>;
  returnFocus: () => void;
}

const overlayScopes: OverlayScope[] = [];
// Исходное состояние общее для всех перекрывающихся modal до последнего cleanup.
const originalInert = new Map<HTMLElement, boolean>();
let originalOverflow = '';

function activeModal() {
  return overlayScopes.filter((scope) => scope.modal.current).at(-1);
}

function updateModalEnvironment() {
  const top = activeModal();
  if (top) {
    if (!originalInert.size) originalOverflow = document.body.style.overflow;
    originalInert.forEach((inert, node) => { node.inert = inert; });
    // Inline responsive panels need the same isolation as portalled dialogs.
    for (let ancestor = top.root.parentElement; ancestor; ancestor = ancestor.parentElement) {
      for (const node of ancestor.children) {
        if (!(node instanceof HTMLElement) || node.matches('script, style, link') || node.contains(top.root)) continue;
        if (!originalInert.has(node)) originalInert.set(node, Boolean(node.inert));
        node.inert = true;
      }
      if (ancestor === document.body) break;
    }
    document.body.style.overflow = 'hidden';
  } else if (originalInert.size) {
    originalInert.forEach((inert, node) => { node.inert = inert; });
    originalInert.clear();
    document.body.style.overflow = originalOverflow;
  }
}

export interface OverlayProps extends Omit<HTMLAttributes<HTMLDivElement>, 'aria-label' | 'children' | 'role'> {
  canDismiss?: boolean;
  children: ReactNode;
  /**
   * «Назад» of the browser closes it first (specs/2026-11-10-ux2-patterns-actions.md UC4). `false` for what the address
   * opens itself — its own «Назад» closes it.
   */
  history?: boolean;
  initialFocusRef?: RefObject<HTMLElement | null>;
  kind: OverlayKind;
  label: string;
  modal?: boolean;
  onDismiss: () => void;
  returnFocusRef?: RefObject<HTMLElement | null>;
  state?: PresenceStatus;
}

export function Overlay({
  canDismiss = true,
  children,
  className,
  history = true,
  initialFocusRef,
  kind,
  label,
  modal = true,
  onDismiss,
  onKeyDown,
  returnFocusRef,
  state = 'open',
  ...props
}: OverlayProps) {
  const rootRef = useRef<HTMLDivElement>(null);
  const modalRef = useRef(modal);
  modalRef.current = modal;
  // An overlay over the page holds a step of the history: «Назад» is its «×», and Esc a busy one does not take either.
  useOverlayHistory(modal && history && state !== 'closing', () => {
    if (canDismiss) onDismiss();
  });

  useEffect(() => {
    const root = rootRef.current;
    if (!root) return;

    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const fallbackFocus = previousFocus?.closest<HTMLElement>('[role="listbox"], [role="menu"], [data-focus-return]') ?? null;
    const previousModal = activeModal();
    const scope: OverlayScope = {
      root,
      modal: modalRef,
      returnFocus() {
        const activeRoot = activeModal()?.root;
        // При StrictMode cleanup DOM закрытого modal ещё может быть подключён.
        const previousModalClosed = previousModal?.modal.current
          && !overlayScopes.some((item) => item.root === previousModal.root);
        const returnTarget = [returnFocusRef?.current, previousFocus, fallbackFocus].find((target) =>
          target?.isConnected
          && (!activeRoot || activeRoot.contains(target))
          && (!previousModalClosed || !previousModal.root.contains(target)));
        // The focus comes back where the author was without scrolling the page to it: closing a note keeps the place of reading.
        if (returnTarget) returnTarget.focus({ preventScroll: true });
        else if (previousModal) previousModal.returnFocus();
        else activeRoot?.focus();
      },
    };
    overlayScopes.push(scope);

    return () => {
      const wasTop = activeModal() === scope;
      overlayScopes.splice(overlayScopes.indexOf(scope), 1);
      updateModalEnvironment();
      if (wasTop && modalRef.current) scope.returnFocus();
    };
  }, []);

  useEffect(() => {
    updateModalEnvironment();
    if (!modal || activeModal()?.root !== rootRef.current) return;

    const focusable = [...(rootRef.current?.querySelectorAll<HTMLElement>(focusableSelector) ?? [])].filter((node) => !node.closest('[hidden], [inert]'));
    const firstNonDestructive = focusable.find((node) => !node.closest('[data-variant="danger"], [data-destructive="true"]'));
    const heading = rootRef.current?.querySelector<HTMLElement>('[data-overlay-heading], h1, h2, h3, h4, h5, h6');
    const destructiveConfirmation = rootRef.current?.querySelector('[data-variant="danger"], [data-destructive="true"]');
    if ((destructiveConfirmation || !firstNonDestructive) && heading && !heading.hasAttribute('tabindex')) heading.tabIndex = -1;
    (initialFocusRef?.current
      ?? (destructiveConfirmation ? heading : firstNonDestructive)
      ?? heading
      ?? rootRef.current)?.focus();
  }, [modal]);

  // The focused part of a window may leave the page — a line of a list gives way to what it opened — or the author clicks
  // beside the window: the focus falls to <body>, and Escape and Tab would go past the window, which then cannot be
  // closed by the keys. The top window takes its keys back from there. A menu of the window keeps its own focus and Escape.
  const dismissRef = useRef({ canDismiss, onDismiss });
  dismissRef.current = { canDismiss, onDismiss };
  useEffect(() => {
    if (!modal) return undefined;
    function takeKeysBack(event: globalThis.KeyboardEvent) {
      const root = rootRef.current;
      if (!root || activeModal()?.root !== root) return;
      const active = document.activeElement;
      if (active && active !== document.body && active !== document.documentElement) return;
      if (event.key === 'Escape') {
        event.stopPropagation();
        if (dismissRef.current.canDismiss) dismissRef.current.onDismiss();
      } else if (event.key === 'Tab') {
        event.preventDefault();
        const focusable = [...root.querySelectorAll<HTMLElement>(focusableSelector)].filter((node) => !node.closest('[hidden], [inert]'));
        ((event.shiftKey ? focusable.at(-1) : focusable[0]) ?? root).focus();
      }
    }
    document.addEventListener('keydown', takeKeysBack);
    return () => document.removeEventListener('keydown', takeKeysBack);
  }, [modal]);

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (!modal || activeModal()?.root !== rootRef.current) return;
    if (event.key === 'Escape') {
      // A menu of this window closes first, by its own Escape (Popover): the window stays.
      if (event.target instanceof Element && event.target.closest('[data-pop]')) return;
      event.stopPropagation();
      if (canDismiss) onDismiss();
      return;
    }
    if (event.key !== 'Tab' || !rootRef.current) {
      return;
    }

    const focusable = [...rootRef.current.querySelectorAll<HTMLElement>(focusableSelector)].filter((node) => !node.closest('[hidden], [inert]'));
    const first = focusable[0] ?? rootRef.current;
    const last = focusable.at(-1) ?? rootRef.current;
    const heading = rootRef.current.querySelector<HTMLElement>('[data-overlay-heading], h1, h2, h3, h4, h5, h6');
    if (event.shiftKey && (document.activeElement === first
      || document.activeElement === heading
      || document.activeElement === rootRef.current)) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  return (
    <div
      {...props}
      aria-label={label}
      aria-modal={modal ? 'true' : undefined}
      className={`${styles.overlay}${className ? ` ${className}` : ''}`}
      data-kind={kind}
      data-state={state}
      onKeyDown={(event) => {
        onKeyDown?.(event);
        if (!event.defaultPrevented) handleKeyDown(event);
      }}
      ref={rootRef}
      role="dialog"
      tabIndex={-1}
    >
      {children}
    </div>
  );
}
