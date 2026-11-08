import { X } from 'lucide-react';
import { IconButton } from '../../../ui/controls/IconButton';
import { Alert } from '../../../ui/feedback/Alert';
import styles from './ImportQueueDialog.module.css';

export type RejectedImportFileView = Readonly<{ fileName: string; message: string }>;

/** «Не добавлены: N» — the files refused before any transfer and why (UC3.1). */
export function RejectedImportFiles({ className, files, onDismiss }: {
  className?: string;
  files: readonly RejectedImportFileView[];
  onDismiss?: () => void;
}) {
  if (files.length === 0) return null;
  const title = `Не добавлены: ${files.length}`;
  return (
    <Alert aria-label={title} className={`${styles.rejected}${className ? ` ${className}` : ''}`} tone="warning">
      <div className={styles.rejectedBody}>
        <h3>{title}</h3>
        <ul>
          {files.map((file, index) => (
            <li key={`${index}:${file.fileName}`}><strong>{file.fileName}</strong> — {file.message}</li>
          ))}
        </ul>
      </div>
      {onDismiss ? (
        <IconButton aria-label="Скрыть причины" className={styles.rejectedDismiss} onClick={onDismiss}>
          <X aria-hidden="true" size={16} />
        </IconButton>
      ) : null}
    </Alert>
  );
}
