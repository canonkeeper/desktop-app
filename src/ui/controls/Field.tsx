import { Eye, EyeOff } from 'lucide-react';
import { useId, useState, type ComponentPropsWithRef, type ReactNode } from 'react';
import styles from './controls.module.css';

/** The props of the input, its ref too: a window may start its focus in a field (React 19 passes the ref as a prop). */
export interface FieldProps extends Omit<ComponentPropsWithRef<'input'>, 'children'> {
  /** A control at the right edge of the input. */
  action?: ReactNode;
  error?: ReactNode;
  hint?: ReactNode;
  label: ReactNode;
}

export function Field({
  'aria-describedby': describedByProp,
  'aria-invalid': invalidProp,
  action,
  className,
  error,
  hint,
  id,
  label,
  ...props
}: FieldProps) {
  const generatedId = useId();
  const inputId = id ?? generatedId;
  const hintId = hint ? `${inputId}-hint` : undefined;
  const errorId = error ? `${inputId}-error` : undefined;
  const describedBy = [describedByProp, hintId, errorId].filter(Boolean).join(' ') || undefined;
  const input = (
    <input
      {...props}
      aria-describedby={describedBy}
      aria-invalid={invalidProp ?? (error ? true : undefined)}
      className={`${styles.input}${className ? ` ${className}` : ''}`}
      id={inputId}
    />
  );

  return (
    <div className={styles.control}>
      <label className={styles.label} htmlFor={inputId}>{label}</label>
      {action ? <div className={styles.inputShell}>{input}{action}</div> : input}
      {hint ? <span className={styles.hint} id={hintId}>{hint}</span> : null}
      {error ? <span className={styles.error} id={errorId} role="alert">{error}</span> : null}
    </div>
  );
}

/** The «Пароль» field with «Показать пароль» / «Скрыть пароль» (R5.1.2). */
export function PasswordField(props: Omit<FieldProps, 'action' | 'type'>) {
  const [visible, setVisible] = useState(false);
  // The name comes from the button text, not aria-label: «Пароль» keeps naming only the input.
  const toggle = visible ? 'Скрыть пароль' : 'Показать пароль';
  return (
    <Field
      {...props}
      action={(
        <button className={styles.inputAction} disabled={props.disabled} onClick={() => setVisible((shown) => !shown)} title={toggle} type="button">
          {visible ? <EyeOff aria-hidden="true" size={16} /> : <Eye aria-hidden="true" size={16} />}
          <span className={styles.labelHidden}>{toggle}</span>
        </button>
      )}
      type={visible ? 'text' : 'password'}
    />
  );
}
