import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useBlocker } from '@tanstack/react-router';
import { ArrowLeft, BookOpen, MessageSquare, Trash2, X } from 'lucide-react';
import { useImperativeHandle, useRef, useState, type FormEvent, type Ref } from 'react';
import { isApiStatusFailure, publicApiErrorMessage } from '../../entities/api-errors';
import { createIdempotencyKey, noteQueryKeys, notesApiFromMeta, notesMeta, type NotesApiClient, type PersonalNote, type NoteResolvedSource } from '../../entities/notes/api';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { Textarea } from '../../ui/controls/Textarea';
import { Field } from '../../ui/controls/Field';
import { Alert } from '../../ui/feedback/Alert';
import { Dialog } from '../../ui/overlays/Dialog';
import type { NoteCapture } from '../../shared/notes/notes-context';
import styles from './NotesPanel.module.css';

export interface NoteEditorHandle { dismiss: () => void; leave: (run: () => void) => void }

export function NoteEditor({ api, accountId, capture, note, onBack, onClose, onSaved, onSource, dismissRef, embedded = false }: {
  api: NotesApiClient;
  accountId: string;
  capture?: NoteCapture;
  note?: PersonalNote;
  onBack?: () => void;
  onClose: () => void;
  onSaved: (outcome: 'saved' | 'deleted') => void;
  onSource: (source: NoteResolvedSource, noteId: string) => void;
  dismissRef?: Ref<NoteEditorHandle>;
  embedded?: boolean;
}) {
  const [base, setBase] = useState(() => ({ title: note?.title ?? '', body: note?.body ?? '', revision: note?.revision }));
  const [title, setTitle] = useState(note?.title ?? '');
  const [body, setBody] = useState(note?.body ?? capture?.body ?? '');
  const [confirmation, setConfirmation] = useState<{ kind: 'discard' | 'delete'; run: () => void } | null>(null);
  const createAttempt = useRef<{ fingerprint: string; key: string } | null>(null);
  const allowNavigation = useRef(false);
  const queryClient = useQueryClient();
  const source = note?.source ?? capture?.source;
  const dirty = body !== base.body || title !== base.title;
  const tooLong = Array.from(body).length > 100000;
  const titleTooLong = Array.from(title).length > 200;
  const save = useMutation<PersonalNote>({
    mutationFn: () => {
      if (note) return api.updatePersonalNote(note.id, { title, body, expectedRevision: base.revision! });
      const input = { title, body, source: source ?? null };
      const fingerprint = JSON.stringify(input);
      if (createAttempt.current?.fingerprint !== fingerprint) createAttempt.current = { fingerprint, key: createIdempotencyKey('createPersonalNote') };
      return api.createPersonalNote(input, { idempotencyKey: createAttempt.current.key });
    },
    onSuccess: (saved) => {
      setBase({ title: saved.title, body: saved.body, revision: saved.revision });
      setTitle(saved.title); setBody(saved.body);
      queryClient.setQueryData(noteQueryKeys.note(accountId, saved.id), saved);
      void queryClient.invalidateQueries({ queryKey: noteQueryKeys.list(accountId) });
      onSaved('saved');
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
  const resolve = useMutation({
    mutationFn: () => queryClient.fetchQuery({
      queryKey: noteQueryKeys.source(accountId, note!.id),
      queryFn: ({ meta, signal }) => notesApiFromMeta(meta).resolvePersonalNoteSource(note!.id, { signal }),
      meta: notesMeta(api),
      staleTime: 0,
      retry: false,
    }),
    onSuccess: (result) => {
      if (result.state === 'current' && result.source) navigateToSource(result.source);
    },
  });
  const busy = save.isPending || remove.isPending || reload.isPending || resolve.isPending;
  useBlocker({
    disabled: !dirty && !busy,
    enableBeforeUnload: dirty || busy,
    shouldBlockFn: ({ current, next }) => {
      if (embedded && current.pathname === '/notes' && next.pathname === '/notes') return false;
      return !allowNavigation.current && (busy || !window.confirm('Изменения заметки не сохранены. Перейти без сохранения?'));
    },
  });

  function navigateToSource(target: NoteResolvedSource) {
    allowNavigation.current = true;
    onSource(target, note!.id);
  }

  function requestLeave(run: () => void) {
    if (busy) return;
    if (dirty) setConfirmation({ kind: 'discard', run });
    else run();
  }
  useImperativeHandle(dismissRef, () => ({ dismiss: () => requestLeave(onClose), leave: requestLeave }));
  function submit(event: FormEvent) {
    event.preventDefault();
    if (!busy && body.trim() && !tooLong && !titleTooLong && dirty) save.mutate();
  }
  const error = save.error ?? reload.error;
  const conflict = isApiStatusFailure(error) && error.status === 409;
  const result = resolve.data;
  const sourceLabel = source?.kind === 'chat' ? 'Из чата · текст на момент сохранения' : 'Из главы · сохранённый фрагмент';
  const heading = note ? 'Заметка' : 'Новая заметка';

  return <>
    <form className={styles.layout} onSubmit={submit}>
      <header className={styles.header}>
        {onBack ? <IconButton aria-label="К списку заметок" disabled={busy} onClick={() => requestLeave(onBack)} size="touch"><ArrowLeft aria-hidden="true" size={16} /></IconButton> : null}
        <h2>{heading}</h2>
        <IconButton aria-label="Закрыть заметку" disabled={busy} onClick={() => requestLeave(onClose)} size="touch"><X aria-hidden="true" size={16} /></IconButton>
      </header>
      <div className={styles.body}>
        <p className={styles.muted}>Только для вас</p>
        <Field disabled={busy} error={titleTooLong ? 'Не больше 200 символов. Название не обрезано.' : undefined} label="Название · необязательно" onChange={(event) => setTitle(event.target.value)} placeholder="Название заметки" value={title} />
        <div className={styles.field}><Textarea autoFocus className={styles.textarea} disabled={busy} error={tooLong ? 'Заметка может содержать не больше 100 000 символов. Текст не обрезан.' : undefined} label="Текст заметки" onChange={(event) => setBody(event.target.value)} placeholder="Запишите мысль…" value={body} /></div>
        {source ? <div className={styles.source}>
          <p className={styles.muted}>{sourceLabel}</p>
          {note && result?.state !== 'unavailable' ? <Button disabled={busy} onClick={() => requestLeave(() => {
            if (result?.state === 'changed' && result.source) navigateToSource(result.source);
            else resolve.mutate();
          })} size="touch" variant="ghost">{source.kind === 'chat' ? <MessageSquare aria-hidden="true" size={16} /> : <BookOpen aria-hidden="true" size={16} />}{result?.state === 'changed' ? 'Открыть главу' : source.kind === 'chat' ? 'К сообщению' : 'К фрагменту главы'}</Button> : null}
        </div> : null}
        {result?.state === 'changed' ? <Alert tone="warning">Исходный текст изменён. Заметка сохранена. Можно открыть актуальную главу без прежнего выделения.</Alert> : null}
        {result?.state === 'unavailable' ? <Alert tone="warning">Источник недоступен. Заметка остаётся у вас.</Alert> : null}
        {resolve.isError ? <Alert tone="error">Не удалось открыть источник. Заметка сохранена. <Button disabled={busy} onClick={() => requestLeave(() => resolve.mutate())} variant="outline">Повторить</Button></Alert> : null}
        {error ? <Alert tone={conflict ? 'warning' : 'error'}>{conflict ? 'Заметка изменена в другом окне. Ваш текст остался в поле.' : publicApiErrorMessage(error, 'Не удалось сохранить изменения. Ваш текст остался в форме.')}{conflict && note ? <Button disabled={busy} onClick={() => requestLeave(() => reload.mutate())} variant="outline">Загрузить последнюю</Button> : null}</Alert> : null}
      </div>
      <footer className={styles.footer}>
        {note ? <IconButton aria-label="Удалить заметку" className={styles.delete} disabled={busy} onClick={() => { remove.reset(); setConfirmation({ kind: 'delete', run: () => remove.mutate() }); }} size="touch"><Trash2 aria-hidden="true" size={16} /></IconButton> : null}
        <Button disabled={busy} onClick={() => requestLeave(onBack ?? onClose)} size="touch" variant="outline">Отмена</Button>
        <Button disabled={busy || !dirty || !body.trim() || tooLong || titleTooLong || conflict} loading={save.isPending} size="touch" type="submit" variant="primary">{save.isPending ? 'Сохраняем…' : save.isError && !conflict ? 'Повторить' : 'Сохранить'}</Button>
      </footer>
    </form>
    {confirmation ? <Dialog width={440} canDismiss={!busy} label={confirmation.kind === 'delete' ? 'Удалить заметку?' : 'Не сохранять?'} onDismiss={() => setConfirmation(null)}>
      <section className={styles.confirm}><h3>{confirmation.kind === 'delete' ? 'Удалить заметку?' : 'Не сохранять?'}</h3><p>{confirmation.kind === 'delete' ? 'Удалится только эта заметка. Исходный чат и текст главы останутся без изменений.' : 'Изменения заметки не будут сохранены.'}</p>
        {confirmation.kind === 'delete' && remove.error ? <Alert tone="error">{publicApiErrorMessage(remove.error, 'Не удалось удалить заметку. Попробуйте ещё раз.')}</Alert> : null}
        <div className={styles.confirmActions}>
        <Button disabled={busy} onClick={() => setConfirmation(null)} variant="outline">Назад</Button><Button disabled={busy} loading={remove.isPending} onClick={() => { const run = confirmation.run; if (confirmation.kind === 'discard') setConfirmation(null); run(); }} variant="danger">{confirmation.kind === 'delete' ? remove.isPending ? 'Удаляем…' : remove.isError ? 'Повторить' : 'Удалить' : 'Не сохранять'}</Button>
      </div></section>
    </Dialog> : null}
  </>;
}
