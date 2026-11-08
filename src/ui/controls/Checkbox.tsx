import { Check } from 'lucide-react';
import { useEffect, useRef, type InputHTMLAttributes, type ReactNode } from 'react';
import styles from './controls.module.css';

export interface CheckboxProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'children' | 'type'> {
  /** Some of what this box stands for is checked: a browser says «mixed» to a screen reader. */
  indeterminate?: boolean;
  label: ReactNode;
}

export function Checkbox({ className, disabled, indeterminate = false, label, ...props }: CheckboxProps) {
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (input.current) input.current.indeterminate = indeterminate;
  }, [indeterminate]);
  return (
    <label className={styles.checkbox} data-disabled={disabled || undefined}>
      <input {...props} className={className} disabled={disabled} ref={input} type="checkbox" />
      <span aria-hidden="true" className={styles.checkboxBox}>
        <Check aria-hidden="true" color="var(--ck-primary-foreground)" size={12} />
      </span>
      <span>{label}</span>
    </label>
  );
}
