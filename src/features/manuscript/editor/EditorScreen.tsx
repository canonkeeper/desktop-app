import { ArrowLeft, BookPlus, FileCheck2, History, RotateCw, TriangleAlert, Undo2, X } from 'lucide-react';
import { useEffect, useState, type CSSProperties } from 'react';
import type { Book, ChapterEditApiClient, ChapterListItem, EditedDraft } from '../../../entities/manuscript/api';
import { useReaderPreferencesStore, useTextBackground } from '../../../entities/manuscript/display-preferences-store';
import { usePageTitlePart } from '../../../shared/navigation/page-title';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import { plain } from '../../../shared/text/faces';
import { Button } from '../../../ui/controls/Button';
import { Field } from '../../../ui/controls/Field';
import { Select } from '../../../ui/controls/Select';
import { Alert } from '../../../ui/feedback/Alert';
import { FaceEditor } from '../../../ui/FaceEditor';
import { Confirm, deletionLines } from '../../../ui/overlays/Confirm';
import { MoreMenu, type MoreMenuItem } from '../../../ui/overlays/MoreMenu';
import { TextBackgroundContext } from '../../../ui/text-background';
import { TextBackgroundChoice } from '../../../ui/TextBackgroundChoice';
import { formatReaderChapterTitle } from '../../../ui/copy';
import { chapterWords } from '../reader/ReaderContents';
import { afterChapterWords, clockTime, discardQuestion, editorWords } from './editor-words';
import { useEditActions } from './use-edit-actions';
import { useEditSaves } from './use-edit-saves';
import { VersionsWindow } from './VersionsWindow';
import styles from './PlainEditor.module.css';

export interface EditorScreenProps {
  api: ChapterEditApiClient;
  navigate: RouteNavigate;
  projectId: string;
  /** The author: what he typed and did not save is kept under his name. */
  author: string;
  /** The draft; null — a new chapter not written down yet. */
  draft: EditedDraft | null;
  book: Book;
  /** The chapters of the book in its order: the places of a new chapter and the name of the edited one. */
  chapters: readonly ChapterListItem[];
  canEdit: boolean;
  onCreated: (draft: EditedDraft) => void;
  /** How soon a save follows the author's last key; the tests wait less. */
  saveDelayMs?: number;
}

const atStart = 'start';
type Window = 'versions' | 'revert' | 'discard' | null;

/**
 * The editor of a chapter without the helper (specs/2026-11-13-desk-editor.md; board 44·20, 44·21, master
 * `Pattern/Desktop/Plain Editor`): the way back and the place, the main button and «⋯» in the head, the title as a field, the
 * sheet of text with its italic and bold, «Фон текста» and «Сохранено · 12:40» under it. No action of the helper, no tab,
 * plan, finding or price: the edit of a chapter of the book, or a new chapter of a book.
 */
export function EditorScreen({ api, author, book, canEdit, chapters, draft, navigate, onCreated, projectId, saveDelayMs }: EditorScreenProps) {
  const edit = draft?.edits ?? null;
  const fresh = !edit;
  const places = chapters.length
    ? chapters.map((item) => ({ value: item.id, label: afterChapterWords(item, formatReaderChapterTitle(item.displayNumber, item.title)) }))
    : [{ value: atStart, label: editorWords.atStart }];
  // The place the author chose, else the one of the draft, else after the last chapter of the book (ED2.3).
  const [after, setAfter] = useState<string | null>(null);
  const drafted = draft && !draft.place.afterChapterGone ? draft.place.afterChapterId ?? atStart : null;
  const chosenPlace = [after, drafted].find((value) => value && places.some((option) => option.value === value)) ?? places.at(-1)!.value;
  const placeId = chosenPlace === atStart ? null : chosenPlace;
  const saves = useEditSaves({
    api, author, draft, onCreated, projectId, ...(saveDelayMs === undefined ? {} : { delayMs: saveDelayMs }),
    place: draft ? null : { bookId: book.id, ...(placeId ? { afterChapterId: placeId } : {}) },
  });
  const actions = useEditActions({ api, bookId: book.id, navigate, projectId, saves });
  const [open, setOpen] = useState<Window>(null);
  const prefs = useReaderPreferencesStore();
  const background = useTextBackground();
  const { hydrateReaderPreferences } = prefs;
  useEffect(() => hydrateReaderPreferences(), [hydrateReaderPreferences]);

  const title = saves.edits.title ?? draft?.title ?? '';
  const text = saves.edits.text ?? draft?.text ?? '';
  const empty = !plain(text).text.trim();
  const chapter = edit ? chapters.find((item) => item.id === edit.chapterId) ?? null : null;
  const name = title.trim() || edit?.chapterTitle || '';
  usePageTitlePart('object', name || (fresh ? 'Новая глава' : null));
  const locked = !canEdit || actions.busy !== null;
  const changed = Boolean(edit && (edit.changed || actions.refusedChanged));

  const reason = edit
    ? edit.gone ? editorWords.gone : changed ? editorWords.changed : empty ? editorWords.empty
      : !saves.unsaved && edit.same ? editorWords.same : null
    : empty ? editorWords.empty : null;
  const placeLine = edit && chapter ? `Книга ${book.place}, ${chapterWords(chapter)}` : null;
  const back = () => (edit
    ? navigate('/manuscript/books', { projectId, bookId: book.id, chapterId: edit.chapterId, mode: 'read' })
    : navigate('/manuscript/books', { projectId, bookId: book.id }));
  const savedLine = saves.unsaved ? editorWords.unsaved
    : saves.savedAt ? editorWords.saved(clockTime(saves.savedAt))
      : draft ? editorWords.saved(clockTime(new Date(draft.updatedAt))) : null;

  const choosePlace = (value: string) => {
    setAfter(value);
    const afterChapterId = value === atStart ? null : value;
    if (draft) void saves.flush({ place: { bookId: book.id, ...(afterChapterId ? { afterChapterId } : {}) } }).catch(() => undefined);
  };

  const items: MoreMenuItem[] = edit ? [
    { label: editorWords.versions, icon: History, run: () => setOpen('versions') },
    // «Вернуть как было» gives the book the text before the last replacement: only after one (edits.canRevert).
    ...(edit.canRevert && !edit.changed && canEdit ? [{ label: editorWords.revert, icon: Undo2, run: () => setOpen('revert') }] : []),
  ] : [];
  const style = { '--reader-text-size': `${prefs.textSize}px` } as CSSProperties;

  return (
    <section aria-label={fresh ? 'Новая глава' : 'Правка главы'} className={styles.editor} data-reading-theme={background} style={style}>
      <TextBackgroundContext.Provider value={background}>
        <header className={styles.head}>
          <div className={styles.way}>
            <Button className={styles.back} onClick={back} size="sm" variant="ghost">
              <ArrowLeft aria-hidden="true" size={16} />{edit ? editorWords.toChapter : editorWords.toBook(book.title)}
            </Button>
            {placeLine ? <span className={styles.place}>{placeLine}</span> : null}
          </div>
          <div className={styles.actions}>
            {fresh ? (
              <div className={styles.placeChoice}>
                <Select disabled={locked} label={editorWords.place} onValueChange={choosePlace} options={places} value={chosenPlace} />
              </div>
            ) : null}
            {canEdit ? (
              <Button disabledReason={reason} loading={actions.busy === (edit ? 'replace' : 'add')}
                onClick={() => void (edit ? actions.replace() : actions.add(placeId))} variant="primary">
                {edit ? <FileCheck2 aria-hidden="true" size={16} /> : <BookPlus aria-hidden="true" size={16} />}
                {edit ? editorWords.replace : editorWords.add}
              </Button>
            ) : null}
            {canEdit || items.length ? (
              <MoreMenu items={items} label={editorWords.more} size="md"
                {...(canEdit ? { remove: { label: edit ? editorWords.discardEdit : editorWords.discardNew, icon: X, run: () => setOpen('discard') } } : {})} />
            ) : null}
          </div>
        </header>
        {changed ? (
          <Alert className={styles.changed} tone="warning">
            <TriangleAlert aria-hidden="true" size={16} />
            <strong>{editorWords.changedLine}</strong>
            {canEdit ? (
              <Button loading={actions.busy === 'reopen'} onClick={() => void actions.reopen()} size="sm" variant="outline">
                <RotateCw aria-hidden="true" size={14} />{editorWords.reopen}
              </Button>
            ) : null}
          </Alert>
        ) : null}
        <Field label={editorWords.title} maxLength={180} onChange={(event) => saves.type('title', event.target.value)} readOnly={locked}
          {...(edit?.chapterTitle ? { placeholder: edit.chapterTitle } : {})} value={title} />
        <div className={styles.sheet}>
          <FaceEditor className={styles.text} label={editorWords.text} maxLength={400000} onChange={(value) => saves.type('text', value)}
            placeholder={editorWords.text} readOnly={locked} value={text} />
        </div>
        <footer className={styles.foot}>
          <TextBackgroundChoice labelled onValue={(theme) => theme !== background && prefs.setDisplayTheme(theme)} value={background} />
          {savedLine ? <span className={styles.saved} data-unsaved={saves.unsaved || undefined} role="status">{savedLine}</span> : null}
        </footer>
      </TextBackgroundContext.Provider>
      {open === 'versions' && draft ? (
        <VersionsWindow api={api} canEdit={canEdit} draftId={draft.id} onClose={() => setOpen(null)} onTake={(number, when, onTaken) => void actions.take(number, when, onTaken)}
          projectId={projectId} taking={actions.busy === 'take'} />
      ) : null}
      {open === 'revert' ? (
        <Confirm action={editorWords.revert} busy={actions.busy === 'revert'} onCancel={() => setOpen(null)}
          onConfirm={() => void actions.revert().then(() => setOpen(null))} title={editorWords.revertQuestion}>
          {editorWords.revertSays}
        </Confirm>
      ) : null}
      {open === 'discard' ? (
        <Confirm action={edit ? 'Отказаться от правки' : 'Отказаться от главы'} also={edit ? editorWords.stays : undefined} busy={actions.busy === 'discard'} irreversible
          onCancel={() => setOpen(null)} onConfirm={() => void actions.discard(edit ? 'edit' : 'new', edit?.chapterId ?? null, () => setOpen(null))}
          title={discardQuestion(edit ? 'edit' : 'new', name)}>
          {deletionLines.gone}
        </Confirm>
      ) : null}
    </section>
  );
}

