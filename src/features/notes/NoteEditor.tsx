import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useBlocker } from '@tanstack/react-router';
import { ArrowLeft, BookOpen, ChevronDown, ChevronUp, MessageSquare, X } from 'lucide-react';
import { useEffect, useId, useImperativeHandle, useLayoutEffect, useRef, useState, type FormEvent, type Ref } from 'react';
import { isApiStatusFailure, publicApiErrorMessage } from '../../entities/api-errors';
import {
  createIdempotencyKey, noteLine, noteQueryKeys, notesApiFromMeta, notesMeta, useNoteSource,
  type NotesApiClient, type PersonalNote, type PersonalNoteSource, type NoteResolvedSource,
} from '../../entities/notes/api';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { SourceLink } from '../../ui/controls/SourceLink';
import { Textarea } from '../../ui/controls/Textarea';
import { Field } from '../../ui/controls/Field';
import { Alert } from '../../ui/feedback/Alert';
import { Confirm, deletionLines } from '../../ui/overlays/Confirm';
import { MoreMenu } from '../../ui/overlays/MoreMenu';
import type { NoteCapture } from '../../shared/notes/notes-context';
import styles from './NotesPanel.module.css';

export interface NoteEditorHandle { dismiss: () => void; leave: (run: () => void) => void }

/** The way to the place of a note: its place and the line the way back names the note by (PS4.2). */
export type NoteSourceStep = (source: NoteResolvedSource, noteId: string, name: string) => void;

/**
 * A note of three parts (specs/2026-11-11-ux7-notes.md NT1.1; board 24·19): its place — one kind of link to a place of the
 * book, «Книга 2, гл. 12 „Белый порт“» in the words of the server, or «К ответу в чате»; the cited words, up to three lines on a
 * grey bar, not edited; and «Ваша мысль». A note of its own is the field alone. An older note's title keeps its field.
 */
export function NoteEditor({ api, accountId, beside = false, capture, note, onBack, onClose, onSaved, onSource, dismissRef, embedded = false, thoughtRef }: {
  api: NotesApiClient;
  accountId: string;
  /** Beside the text of the reader: the panel closes itself, the note has no close of its own (NT2.2). */
  beside?: boolean;
  capture?: NoteCapture;
  note?: PersonalNote;
  onBack?: () => void;
  onClose: () => void;
  onSaved: (outcome: 'saved' | 'deleted', saved?: PersonalNote) => void;
  onSource: NoteSourceStep;
  dismissRef?: Ref<NoteEditorHandle>;
  embedded?: boolean;
  /** The field «Ваша мысль»: a window of a new note of a place puts the focus there (NT1.2). */
  thoughtRef?: Ref<HTMLTextAreaElement>;
}) {
  const source = note?.source ?? capture?.source ?? null;
  const [base, setBase] = useState(() => ({ title: note?.title ?? '', body: note?.body ?? '', revision: note?.revision }));
  const [title, setTitle] = useState(note?.title ?? '');
  // A note of a place starts without a thought: the selected words are its cited part (NT1.2).
  const [body, setBody] = useState(note?.body ?? (source ? '' : capture?.body ?? ''));
  const [confirmation, setConfirmation] = useState<{ kind: 'discard' | 'delete'; run: () => void } | null>(null);
  const createAttempt = useRef<{ fingerprint: string; key: string } | null>(null);
  const allowNavigation = useRef(false);
  const titled = useRef(Boolean(note?.title)).current;
  const goneId = useId();
  const queryClient = useQueryClient();
  const dirty = body !== base.body || title !== base.title;
  const tooLong = Array.from(body).length > 100000;
  const titleTooLong = Array.from(title).length > 200;
  // A thought may be left empty only beside cited words; a new note of a place is saved as it is.
  const savable = (note ? dirty : Boolean(source) || dirty) && (Boolean(source) || Boolean(body.trim())) && !tooLong && !titleTooLong;
  const place = useNoteSource(api, accountId, note?.id ?? '', source?.kind === 'chapter');
  const save = useMutation<PersonalNote>({
    mutationFn: () => {
      if (note) return api.updatePersonalNote(note.id, { ...(titled ? { title } : {}), body, expectedRevision: base.revision! });
      const input = { title: '', body, source };
      const fingerprint = JSON.stringify(input);
      if (createAttempt.current?.fingerprint !== fingerprint) createAttempt.current = { fingerprint, key: createIdempotencyKey('createPersonalNote') };
      return api.createPersonalNote(input, { idempotencyKey: createAttempt.current.key });
    },
    onSuccess: (saved) => {
      setBase({ title: saved.title, body: saved.body, revision: saved.revision });
      setTitle(saved.title); setBody(saved.body);
      queryClient.setQueryData(noteQueryKeys.note(accountId, saved.id), saved);
      void queryClient.invalidateQueries({ queryKey: noteQueryKeys.list(accountId) });
      onSaved('saved', saved);
    },
  });
  const remove = useMutation({
    mutationFn: () => api.deletePersonalNote(note!.id, base.revision!),
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: noteQueryKeys.note(accountId, note!.id) });
      void queryClient.invalidateQueries({ queryKey: noteQueryKeys.list(accountId) });
      onSaved('deleted');
    },
  });
  const reload = useMutation({
    mutationFn: () => api.getPersonalNote(note!.id),
    onSuccess: (fresh) => {
      queryClient.setQueryData(noteQueryKeys.note(accountId, fresh.id), fresh);
      setBase({ title: fresh.title, body: fresh.body, revision: fresh.revision });
      setTitle(fresh.title);
      setBody(fresh.body);
      save.reset(); remove.reset();
    },
  });
  // The place is read again by the press: a chat answers by its conversation, the book by its current text.
  const resolve = useMutation({
    mutationFn: () => queryClient.fetchQuery({
      queryKey: noteQueryKeys.source(accountId, note!.id),
      queryFn: ({ meta, signal }) => notesApiFromMeta(meta).resolvePersonalNoteSource(note!.id, { signal }),
      meta: notesMeta(api),
      staleTime: 0,
      retry: false,
    }),
    onSuccess: (result) => {
      if (result.state !== 'unavailable' && result.source) navigateToSource(result.source);
    },
  });
  const busy = save.isPending || remove.isPending || reload.isPending || resolve.isPending;
  // A way out of the page with unsaved words asks the one question «Не сохранять?» (specs/2026-11-10-ux2-patterns-actions.md PA2.2).
  const leaving = useBlocker({
    disabled: !dirty && !busy,
    enableBeforeUnload: dirty || busy,
    shouldBlockFn: ({ current, next }) => {
      if (allowNavigation.current) return false;
      // A step of a window over the page keeps the address: it is «Назад» of the window, not a way out of the page (UC4).
      if (current.pathname === next.pathname && JSON.stringify(current.search) === JSON.stringify(next.search)) return false;
      // The filters of the catalog change around the open note; another note or none in the address leaves it (NT2.1).
      if (embedded && current.pathname === '/notes' && next.pathname === '/notes') {
        return (current.search as { note?: string }).note !== (next.search as { note?: string }).note;
      }
      // Beside the text the reader goes on — «Найти», «Аа», the place of the note; the note is left when another one opens
      // or «Заметки» close (NT2.2).
      if (beside && current.pathname === next.pathname) {
        const after = next.search as { contents?: string; note?: string };
        return after.contents !== 'notes' || (current.search as { note?: string }).note !== after.note;
      }
      return true;
    },
    withResolver: true,
  });
  const leavingBlocked = leaving.status === 'blocked';
  // While a note is saved or deleted the page stays where it is, without a question.
  useEffect(() => {
    if (leavingBlocked && busy) leaving.reset?.();
  }, [busy, leaving, leavingBlocked]);

  function navigateToSource(target: NoteResolvedSource) {
    allowNavigation.current = true;
    onSource(target, note!.id, noteLine({ title, thought: body, quote: source?.quote }));
  }

  function requestLeave(run: () => void) {
    if (busy) return;
    if (dirty) setConfirmation({ kind: 'discard', run });
    else run();
  }
  useImperativeHandle(dismissRef, () => ({ dismiss: () => requestLeave(onClose), leave: requestLeave }));
  function submit(event: FormEvent) {
    event.preventDefault();
    if (!busy && savable) save.mutate();
  }
  const error = save.error ?? reload.error;
  const conflict = isApiStatusFailure(error) && error.status === 409;
  const known = place.data ?? resolve.data;
  // A place the book no longer has, or whose words are no longer in its chapter (NT2.4).
  const gone = known?.state === 'unavailable' || (source?.kind === 'chapter' && known?.state === 'changed');
  const heading = note ? 'Заметка' : 'Новая заметка';

  function goToPlace() {
    requestLeave(() => {
      if (source?.kind === 'chapter' && place.data && place.data.state !== 'unavailable' && place.data.source) navigateToSource(place.data.source);
      else resolve.mutate();
    });
  }

  return <>
    <form className={styles.layout} data-beside={beside || undefined} onSubmit={submit}>
      <header className={styles.header}>
        {onBack ? <IconButton aria-label={beside ? 'К заметкам главы' : 'К списку заметок'} disabled={busy} onClick={() => requestLeave(onBack)} size="touch"><ArrowLeft aria-hidden="true" size={16} /></IconButton> : null}
        <h2>{heading}</h2>
        {/* The deletion of a note stands in its menu «⋯», not beside «Сохранить» (PA3.2, W-5G). */}
        {note ? <MoreMenu disabled={busy} label="Ещё" remove={{ label: 'Удалить заметку…', run: () => {
          remove.reset();
          setConfirmation({ kind: 'delete', run: () => remove.mutate() });
        } }} size="touch" /> : null}
        {beside ? null : <IconButton aria-label="Закрыть заметку" disabled={busy} onClick={() => requestLeave(onClose)} size="touch"><X aria-hidden="true" size={16} /></IconButton>}
      </header>
      <div className={styles.body}>
        <p className={styles.muted}>Только для вас</p>
        {source ? <NotePlace busy={busy} capture={capture} gone={gone} goneId={goneId} label={known?.place} onOpen={note ? goToPlace : undefined}
          pending={source.kind === 'chapter' && place.isPending} source={source} /> : null}
        {source ? <NoteQuote text={source.quote} /> : null}
        {gone ? <p className={styles.gone} id={goneId}>{source?.kind === 'chat' ? 'Этого ответа в чате больше нет — цитата сохранена' : 'Этого места в книге больше нет — цитата сохранена'}</p> : null}
        {resolve.isError ? <Alert tone="error">Не удалось открыть место. Заметка сохранена. <Button disabled={busy} onClick={() => requestLeave(() => resolve.mutate())} variant="outline">Повторить</Button></Alert> : null}
        {titled ? <Field disabled={busy} error={titleTooLong ? 'Не больше 200 символов. Название не обрезано.' : undefined} label="Название" onChange={(event) => setTitle(event.target.value)} value={title} /> : null}
        <div className={styles.field}><Textarea autoFocus className={styles.textarea} ref={thoughtRef} disabled={busy} error={tooLong ? 'Заметка может содержать не больше 100 000 символов. Текст не обрезан.' : undefined} label="Ваша мысль" onChange={(event) => setBody(event.target.value)} placeholder="Запишите мысль…" value={body} /></div>
        {error ? <Alert tone={conflict ? 'warning' : 'error'}>{conflict ? 'Заметка изменена в другом окне. Ваш текст остался в поле.' : publicApiErrorMessage(error, 'Не удалось сохранить изменения. Ваш текст остался в форме.')}{conflict && note ? <Button disabled={busy} onClick={() => requestLeave(() => reload.mutate())} variant="outline">Загрузить последнюю</Button> : null}</Alert> : null}
      </div>
      <footer className={styles.footer}>
        <Button disabled={busy} onClick={() => requestLeave(onBack ?? onClose)} size="touch" variant="outline">Отмена</Button>
        <Button disabled={busy || !savable || conflict} loading={save.isPending} size="touch" type="submit" variant="primary">{save.isError && !conflict && !save.isPending ? 'Повторить' : 'Сохранить'}</Button>
      </footer>
    </form>
    {confirmation?.kind === 'delete' ? (
      <Confirm action="Удалить заметку" also="Чат и текст главы, откуда она, останутся." busy={remove.isPending}
        error={remove.error ? publicApiErrorMessage(remove.error, 'Не удалось удалить заметку. Попробуйте ещё раз.') : null} irreversible
        onCancel={() => setConfirmation(null)} onConfirm={confirmation.run} title="Удалить заметку?">
        {deletionLines.gone}
      </Confirm>
    ) : confirmation?.kind === 'discard' || (leavingBlocked && !busy) ? (
      <Confirm action="Не сохранять" irreversible title="Не сохранять?"
        onCancel={() => (confirmation ? setConfirmation(null) : leaving.reset?.())}
        onConfirm={() => {
          allowNavigation.current = true;
          if (!confirmation) {
            leaving.proceed?.();
            return;
          }
          const { run } = confirmation;
          setConfirmation(null);
          run();
        }}>
        Изменения заметки не будут сохранены.
      </Confirm>
    ) : null}
  </>;
}

/**
 * The place of a note (NT1.1, NT2.2, NT2.4): the place of its chapter in the words of the server — the press opens the
 * reader at its paragraph — or «К ответу в чате»; while a note is new, the place of the selection in words. A place the
 * book no longer has is closed, and the line under the cited words says why.
 */
function NotePlace({ busy, capture, gone, goneId, label, onOpen, pending, source }: {
  busy: boolean;
  capture?: NoteCapture | undefined;
  gone: boolean;
  goneId: string;
  label?: string | undefined;
  onOpen?: (() => void) | undefined;
  pending: boolean;
  source: PersonalNoteSource;
}) {
  const Icon = source.kind === 'chat' ? MessageSquare : BookOpen;
  if (!onOpen) {
    return <p className={styles.place}><Icon aria-hidden="true" size={14} />{source.kind === 'chat' ? 'Ответ в чате' : capture?.place || 'Место в книге'}</p>;
  }
  if (source.kind === 'chat') {
    return <p className={styles.place}><SourceLink aria-describedby={gone ? goneId : undefined} disabled={busy || gone} onClick={onOpen}>К ответу в чате</SourceLink></p>;
  }
  const words = label ?? 'Место в книге';
  return (
    <p className={styles.place}>
      <SourceLink aria-busy={pending || undefined} aria-describedby={gone && !label ? goneId : undefined} aria-label={`К месту в книге: ${words}`}
        disabled={busy || (gone && !label)} onClick={onOpen} title="К месту в книге">{words}</SourceLink>
    </p>
  );
}

/** The cited words of a note: up to three lines on a grey bar; «Показать всю» opens it whole. It is not edited (NT1.1). */
function NoteQuote({ text }: { text: string }) {
  const ref = useRef<HTMLQuoteElement>(null);
  const [open, setOpen] = useState(false);
  const [long, setLong] = useState(false);
  useLayoutEffect(() => {
    const quote = ref.current;
    if (quote && !open) setLong(quote.scrollHeight > quote.clientHeight + 1);
  }, [open, text]);
  return <>
    <blockquote aria-label="Цитата" className={styles.quote} data-open={open || undefined} ref={ref}>{text}</blockquote>
    {long || open ? (
      <Button aria-expanded={open} className={styles.quoteToggle} onClick={() => setOpen(!open)} size="sm" variant="ghost">
        {open ? <ChevronUp aria-hidden="true" size={14} /> : <ChevronDown aria-hidden="true" size={14} />}{open ? 'Свернуть' : 'Показать всю'}
      </Button>
    ) : null}
  </>;
}
