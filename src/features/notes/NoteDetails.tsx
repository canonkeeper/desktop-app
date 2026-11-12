import type { Ref } from 'react';
import { usePersonalNote, type NotesApiClient, type PersonalNote } from '../../entities/notes/api';
import { Skeleton } from '../../ui/feedback/Skeleton';
import { NoteEditor, type NoteEditorHandle, type NoteSourceStep } from './NoteEditor';
import { NoteReadFailure } from './NotesList';
import styles from './NotesPanel.module.css';

export function NoteDetails({ api, accountId, beside = false, noteId, onBack, onClose, onSaved, onSource, editorRef, embedded = false }: {
  api: NotesApiClient;
  accountId: string;
  beside?: boolean;
  noteId: string;
  onBack?: () => void;
  onClose: () => void;
  onSaved: (outcome: 'saved' | 'deleted', saved?: PersonalNote) => void;
  onSource: NoteSourceStep;
  editorRef?: Ref<NoteEditorHandle>;
  embedded?: boolean;
}) {
  const query = usePersonalNote(api, accountId, noteId);
  if (!query.data) return query.isPending
    ? <div className={styles.body} role="status" aria-label="Загрузка заметки"><Skeleton style={{ height: 200 }} /></div>
    : <NoteReadFailure onBack={onBack ?? onClose} onRetry={() => void query.refetch()} />;
  return <NoteEditor api={api} accountId={accountId} beside={beside} note={query.data} onBack={onBack} onClose={onClose} onSaved={onSaved} onSource={onSource} dismissRef={editorRef} embedded={embedded} />;
}
