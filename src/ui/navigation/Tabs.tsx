import type { ButtonHTMLAttributes, HTMLAttributes, KeyboardEvent } from 'react';
import styles from './navigation.module.css';

interface TabsProps extends Omit<HTMLAttributes<HTMLDivElement>, 'role'> {
  label: string;
}

interface TabProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'role'> {
  selected: boolean;
}

export function Tabs({ children, className, label, ...props }: TabsProps) {
  return (
    <div {...props} aria-label={label} className={`${styles.tabs}${className ? ` ${className}` : ''}`} role="tablist">
      {children}
    </div>
  );
}

export function Tab({ children, className, onKeyDown, selected, ...props }: TabProps) {
  return (
    <button
      {...props}
      aria-selected={selected}
      className={`${styles.tab}${className ? ` ${className}` : ''}`}
      onKeyDown={(event) => {
        onKeyDown?.(event);
        if (!event.defaultPrevented) moveTabFocus(event);
      }}
      role="tab"
      tabIndex={selected ? 0 : -1}
      type="button"
    >
      {children}
    </button>
  );
}

function moveTabFocus(event: KeyboardEvent<HTMLButtonElement>) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
  const tabs = [...(event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="tab"]') ?? [])]
    .filter((tab) => !tab.disabled);
  const current = tabs.indexOf(event.currentTarget);
  const target = event.key === 'Home'
    ? tabs[0]
    : event.key === 'End'
      ? tabs.at(-1)
      : tabs[(current + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length];
  if (!target) return;
  event.preventDefault();
  target.focus();
  target.click();
}
