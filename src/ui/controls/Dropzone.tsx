import { CloudUpload } from 'lucide-react';
import { useId, useState, type InputHTMLAttributes, type ReactNode } from 'react';
import styles from './controls.module.css';

export interface DropzoneProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'children' | 'type'> {
  description?: ReactNode;
  label: ReactNode;
  onFilesDrop?: (files: File[]) => void;
}

export function Dropzone({
  'aria-describedby': describedByProp,
  description,
  disabled,
  id,
  label,
  onFilesDrop,
  ...props
}: DropzoneProps) {
  const generatedId = useId();
  const inputId = id ?? generatedId;
  const descriptionId = description ? `${inputId}-description` : undefined;
  const describedBy = [describedByProp, descriptionId].filter(Boolean).join(' ') || undefined;
  const [dragging, setDragging] = useState(false);

  return (
    <label
      className={styles.dropzone}
      data-disabled={disabled || undefined}
      data-dragging={dragging || undefined}
      htmlFor={inputId}
      onDragEnter={(event) => {
        event.preventDefault();
        if (!disabled) setDragging(true);
      }}
      onDragLeave={() => setDragging(false)}
      onDragOver={(event) => event.preventDefault()}
      onDrop={(event) => {
        event.preventDefault();
        setDragging(false);
        if (!disabled && event.dataTransfer.files.length > 0) {
          onFilesDrop?.([...event.dataTransfer.files]);
        }
      }}
    >
      <input
        {...props}
        aria-describedby={describedBy}
        className={styles.fileInput}
        disabled={disabled}
        id={inputId}
        type="file"
      />
      <CloudUpload aria-hidden="true" size={28} />
      <strong>{label}</strong>
      {description ? <span id={descriptionId}>{description}</span> : null}
    </label>
  );
}
