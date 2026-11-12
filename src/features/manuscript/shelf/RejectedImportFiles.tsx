import { FileUp, X } from 'lucide-react';
import { IconButton } from '../../../ui/controls/IconButton';
import { Alert } from '../../../ui/feedback/Alert';
import styles from './RejectedImportFiles.module.css';

export type RejectedImportFileView = Readonly<{ fileName: string; message: string }>;

/**
 * «Эти файлы не подошли» — the files refused before any transfer, each with its reason, and «Выбрать другой» at once
 * (specs/2026-11-11-ux7-upload.md UP1.2; L-22).
 */
export function RejectedImportFiles({ files, onChoose, onDismiss }: {
  files: readonly RejectedImportFileView[];
  onChoose?: () => void;
  onDismiss?: () => void;
}) {
  if (files.length === 0) return null;
  const title = files.length === 1 ? 'Этот файл не подошёл' : `Эти файлы не подошли: ${files.length}`;
  return (
    <Alert aria-label={title} className={styles.rejected} tone="warning">
      <div className={styles.body}>
        <h3>{title}</h3>
        <ul>
          {files.map((file, index) => (
            <li key={`${index}:${file.fileName}`}><strong>{file.fileName}</strong> — {file.message}</li>
          ))}
        </ul>
        {onChoose ? (
          <button className={styles.choose} onClick={onChoose} type="button">
            <FileUp aria-hidden="true" size={15} />{files.length === 1 ? 'Выбрать другой' : 'Выбрать другие'}
          </button>
        ) : null}
      </div>
      {onDismiss ? (
        <IconButton aria-label="Скрыть причины" className={styles.dismiss} onClick={onDismiss}>
          <X aria-hidden="true" size={16} />
        </IconButton>
      ) : null}
    </Alert>
  );
}
