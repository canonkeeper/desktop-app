import type { HTMLAttributes, KeyboardEvent } from 'react';
import styles from './navigation.module.css';

export type SegmentedOption<T extends string> = {
  disabled?: boolean;
  label: string;
  value: T;
};

export interface SegmentedControlProps<T extends string> extends Omit<HTMLAttributes<HTMLDivElement>, 'onChange'> {
  size?: 'md' | 'touch';
  fullWidth?: boolean;
  label: string;
  onValueChange: (value: T) => void;
  options: readonly SegmentedOption<T>[];
  value: T;
}

export function SegmentedControl<T extends string>({
  className,
  label,
  onValueChange,
  options,
  value,
  size = 'md',
  fullWidth = false,
  ...props
}: SegmentedControlProps<T>) {
  return (
    <div
      {...props}
      aria-label={label}
      className={`${styles.segmented}${className ? ` ${className}` : ''}`}
      role="radiogroup"
      data-size={size}
      data-full-width={fullWidth || undefined}
    >
      {options.map((option) => (
        <button
          aria-checked={option.value === value}
          className={styles.segment}
          disabled={option.disabled}
          key={option.value}
          onClick={() => onValueChange(option.value)}
          onKeyDown={moveSegmentFocus}
          role="radio"
          tabIndex={option.value === value ? 0 : -1}
          type="button"
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}

function moveSegmentFocus(event: KeyboardEvent<HTMLButtonElement>) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
  const segments = [...(event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="radio"]') ?? [])]
    .filter((segment) => !segment.disabled);
  const current = segments.indexOf(event.currentTarget);
  const target = event.key === 'Home'
    ? segments[0]
    : event.key === 'End'
      ? segments.at(-1)
      : segments[(current + (event.key === 'ArrowRight' ? 1 : -1) + segments.length) % segments.length];
  if (!target) return;
  event.preventDefault();
  target.focus();
  target.click();
}
