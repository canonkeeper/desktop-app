import { LoaderCircle, Search, X } from 'lucide-react';
import { useId, type InputHTMLAttributes, type ReactNode, type Ref } from 'react';
import { IconButton } from './IconButton';
import styles from './controls.module.css';

export interface SearchFieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'children' | 'type'> {
  error?: ReactNode;
  hint?: ReactNode;
  inputRef?: Ref<HTMLInputElement>;
  label: ReactNode;
  labelHidden?: boolean;
  loading?: boolean;
  onClear?: () => void;
}

export function SearchField({
  'aria-describedby': describedByProp,
  'aria-invalid': invalidProp,
  className,
  error,
  hint,
  id,
  inputRef,
  label,
  labelHidden = false,
  loading = false,
  onClear,
  value,
  ...props
}: SearchFieldProps) {
  const generatedId = useId();
  const inputId = id ?? generatedId;
  const hintId = hint ? `${inputId}-hint` : undefined;
  const errorId = error ? `${inputId}-error` : undefined;
  const describedBy = [describedByProp, hintId, errorId].filter(Boolean).join(' ') || undefined;
  const canClear = onClear && String(value ?? '').length > 0 && !loading;

  return (
    <div className={styles.control}>
      <label className={labelHidden ? styles.labelHidden : styles.label} htmlFor={inputId}>{label}</label>
      <div className={styles.searchShell}>
        {loading
          ? <LoaderCircle aria-hidden="true" className={styles.spinnerIcon} size={16} />
          : <Search aria-hidden="true" size={16} />}
        <input
          {...props}
          aria-busy={loading || undefined}
          aria-describedby={describedBy}
          aria-invalid={invalidProp ?? (error ? true : undefined)}
          className={`${styles.searchInput}${className ? ` ${className}` : ''}`}
          id={inputId}
          ref={inputRef}
          type="search"
          value={value}
        />
        {canClear ? (
          <IconButton aria-label="Очистить поиск" onClick={onClear} size="sm">
            <X aria-hidden="true" size={16} />
          </IconButton>
        ) : null}
      </div>
      {hint ? <span className={styles.hint} id={hintId}>{hint}</span> : null}
      {error ? <span className={styles.error} id={errorId} role="alert">{error}</span> : null}
    </div>
  );
}
