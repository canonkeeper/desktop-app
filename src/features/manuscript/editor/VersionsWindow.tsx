import { ArrowLeft, History, X } from 'lucide-react';
import { useState } from 'react';
import { loadFailureWords } from '../../../entities/api-errors';
import {
  useEditedDraftVersionQuery, useEditedDraftVersionsQuery, type ChapterEditApiClient,
} from '../../../entities/manuscript/api';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { Alert } from '../../../ui/feedback/Alert';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { SkeletonArea } from '../../../ui/feedback/Skeleton';
import { FaceEditor } from '../../../ui/FaceEditor';
import { Dialog } from '../../../ui/overlays/Dialog';
import { editorWords, versionTime, versionWords } from './editor-words';
import styles from './PlainEditor.module.css';

interface VersionsWindowProps {
  api: ChapterEditApiClient;
  projectId: string;
  draftId: string;
  /** Whether the author may give a version back; a reader of the saga only reads them. */
  canEdit: boolean;
  /** «Вернуть эту версию»: the version whole in place of the text, the window closes after it. */
  onTake: (number: number, when: string, onTaken: () => void) => void;
  taking: boolean;
  onClose: () => void;
}

/**
 * «Версии текста» (specs/2026-11-13-desk-editor.md ED1.4; board 44·20): the versions of the text, the latest first, each
 * by when it was kept and what it holds; one opened shows its text and gives itself back whole — the text now stays in
 * the versions. «Закрыть», Esc and «Назад» close the window.
 */
export function VersionsWindow({ api, canEdit, draftId, onClose, onTake, projectId, taking }: VersionsWindowProps) {
  const [open, setOpen] = useState<number | null>(null);
  const list = useEditedDraftVersionsQuery(api, projectId, draftId, true);
  const version = useEditedDraftVersionQuery(api, projectId, draftId, open);
  const versions = list.data?.versions.filter((item) => item.words > 0) ?? [];
  const opened = versions.find((item) => item.number === open) ?? null;

  function body() {
    if (open !== null) {
      return (
        <div className={styles.versionOpen}>
          <Button onClick={() => setOpen(null)} size="sm" variant="ghost"><ArrowLeft aria-hidden="true" size={14} />{editorWords.allVersions}</Button>
          {opened ? <p className={styles.versionLine}><b>{versionTime(opened.createdAt)}</b> · {versionWords(opened)}</p> : null}
          {version.isPending ? <SkeletonArea count={4} shape="lines" /> : null}
          {version.isError ? <Alert tone="error">{editorWords.versionFailed}</Alert> : null}
          {version.data ? (
            <div className={styles.versionText}>
              <FaceEditor className={styles.versionField} label={`${editorWords.text}: ${opened ? versionTime(opened.createdAt) : ''}`.trim()} onChange={() => undefined}
                readOnly value={version.data.text} />
            </div>
          ) : null}
        </div>
      );
    }
    if (list.isPending) return <SkeletonArea count={3} shape="rows" />;
    if (list.isError) {
      return <ErrorBlock {...loadFailureWords(list.error)} onRetry={() => void list.refetch()} retrying={list.isRefetching} title={editorWords.versionsFailed} />;
    }
    if (!versions.length) return <p className={styles.versionEmpty}>{editorWords.noVersions}</p>;
    return (
      <ul aria-label={editorWords.versions} className={styles.versionList}>
        {versions.map((item) => (
          <li key={item.number}>
            <button className={styles.versionRow} onClick={() => setOpen(item.number)} type="button">
              <History aria-hidden="true" size={16} />
              <span><b>{versionTime(item.createdAt)}</b><small>{versionWords(item)}</small></span>
            </button>
          </li>
        ))}
      </ul>
    );
  }

  return (
    <Dialog label={editorWords.versions} onDismiss={onClose} width={560}>
      <div className={styles.window}>
        <div className={styles.windowHead}>
          <h2 className={styles.windowTitle}>{editorWords.versions}</h2>
          <IconButton aria-label={`${editorWords.close} ${editorWords.versions.toLowerCase()}`} onClick={onClose} size="sm"><X aria-hidden="true" size={16} /></IconButton>
        </div>
        {body()}
        <div className={styles.windowActions}>
          {open !== null && canEdit && version.data ? (
            <span className={styles.take}>
              <small>{editorWords.takeKeeps}</small>
              <Button loading={taking} onClick={() => onTake(open, opened ? versionTime(opened.createdAt) : '', onClose)} variant="primary">{editorWords.takeVersion}</Button>
            </span>
          ) : null}
          <Button onClick={onClose} variant="outline">{editorWords.close}</Button>
        </div>
      </div>
    </Dialog>
  );
}
