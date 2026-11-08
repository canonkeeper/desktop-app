import { useId, type ComponentPropsWithRef, type ReactNode } from 'react';
import styles from './controls.module.css';

export interface TextareaProps extends Omit<ComponentPropsWithRef<'textarea'>, 'children'> {
  error?: ReactNode;
  hint?: ReactNode;
  label: ReactNode;
}

export function Textarea({
  'aria-describedby': describedByProp,
  'aria-invalid': invalidProp,
  className,
  error,
  hint,
  id,
  label,
  ...props
}: TextareaProps) {
  const generatedId = useId();
  const textareaId = id ?? generatedId;
  const hintId = hint ? `${textareaId}-hint` : undefined;
  const errorId = error ? `${textareaId}-error` : undefined;
  const describedBy = [describedByProp, hintId, errorId].filter(Boolean).join(' ') || undefined;

  return (
    <div className={styles.control}>
      <label className={styles.label} htmlFor={textareaId}>{label}</label>
      <textarea
        {...props}
        aria-describedby={describedBy}
        aria-invalid={invalidProp ?? (error ? true : undefined)}
        className={`${styles.input} ${styles.textarea}${className ? ` ${className}` : ''}`}
        id={textareaId}
      />
      {hint ? <span className={styles.hint} id={hintId}>{hint}</span> : null}
      {error ? <span className={styles.error} id={errorId} role="alert">{error}</span> : null}
    </div>
  );
}
