import { Pencil, Plus, Trash2 } from 'lucide-react';
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { isApiStatusFailure, publicApiErrorMessage } from '../../entities/api-errors';
import {
  authorMemoryKindLabel, authorMemoryKinds, createIdempotencyKey, useAuthorMemoryMutation, useAuthorMemoryQuery,
  type AuthorMemoryApiClient, type AuthorMemoryDraft, type AuthorMemoryKind, type AuthorMemoryRecord,
} from '../../entities/author-memory/api';
import type { AppRoute, RouteNavigate } from '../../shared/navigation/app-route';
import { Button } from '../../ui/controls/Button';
import { Field } from '../../ui/controls/Field';
import { IconButton } from '../../ui/controls/IconButton';
import { SearchField } from '../../ui/controls/SearchField';
import { Select } from '../../ui/controls/Select';
import { Textarea } from '../../ui/controls/Textarea';
import { pluralize } from '../../ui/copy';
import { Alert } from '../../ui/feedback/Alert';
import { Badge } from '../../ui/feedback/Badge';
import { EmptyState } from '../../ui/feedback/EmptyState';
import { Skeleton } from '../../ui/feedback/Skeleton';
import { Dialog } from '../../ui/overlays/Dialog';
import styles from './AuthorMemory.module.css';

const maxTextLength = 8000;
const allKinds = '';

export interface AuthorMemoryProps {
  api: AuthorMemoryApiClient;
  /** An editor of the saga: may add, change and remove records (R4.5). */
  canEdit: boolean;
  navigate: RouteNavigate;
  projectId: string;
  route: AppRoute;
}

const kindTones: Record<AuthorMemoryKind, 'info' | 'warning' | 'neutral' | 'error'> = {
  plan: 'info', correction: 'warning', style_rule: 'neutral', rejected: 'error',
};

const saveMessages = {
  codes: { 'author_memory.no_chapter': 'В саге нет такой главы. Укажите её номер, например 481.' },
  forbidden: 'Менять память может редактор саги.',
  conflict: 'Запись уже изменили. Закройте окно и откройте её снова.',
};

function recordDate(value: string) {
  return new Date(value).toLocaleDateString('ru-RU', { day: 'numeric', month: 'long', year: 'numeric' });
}

/** «Память» of the saga (specs/2026-11-01-writer-assistant-index.md, R4.5): the filters live in the URL. */
export function AuthorMemory({ api, canEdit, navigate, projectId, route }: AuthorMemoryProps) {
  const rawKind = route.query.get('kind');
  const kind = authorMemoryKinds.some((item) => item.value === rawKind) ? rawKind as AuthorMemoryKind : undefined;
  const urlQuery = route.query.get('q') ?? '';
  const [searchDraft, setSearchDraft] = useState(urlQuery);
  const [dialog, setDialog] = useState<{ type: 'add' } | { type: 'edit' | 'delete'; record: AuthorMemoryRecord } | null>(null);
  const filters = { ...(kind ? { kind } : {}), ...(urlQuery ? { q: urlQuery } : {}) };
  const memory = useAuthorMemoryQuery(api, projectId, filters);
  const records = memory.data?.pages.flatMap((page) => page.data) ?? [];
  const total = memory.data?.pages[0]?.meta.totalCount ?? 0;
  const filtered = Boolean(kind || urlQuery);
  const goTo = (next: { kind?: string | undefined; q?: string | undefined }) => navigate('/memory', {
    projectId, kind: 'kind' in next ? next.kind : kind, q: 'q' in next ? next.q : urlQuery || undefined,
  }, { replace: true });

  useEffect(() => { setSearchDraft(urlQuery); }, [urlQuery]);

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <div>
          <h1 className={styles.title}>Память</h1>
          <p className={styles.lead}>То, что вы сказали или одобрили и чего нет в тексте. Помощник читает это в каждом чате этой саги, перед тем как писать или планировать главу.</p>
        </div>
        {canEdit ? <Button onClick={() => setDialog({ type: 'add' })} variant="primary"><Plus aria-hidden="true" size={16} />Добавить запись</Button> : null}
      </header>
      <form className={styles.filters} onSubmit={(event) => { event.preventDefault(); goTo({ q: searchDraft.trim() || undefined }); }} role="search">
        <div className={styles.kindFilter}>
          <Select<AuthorMemoryKind | typeof allKinds> label="Вид записи" onValueChange={(value) => goTo({ kind: value || undefined })}
            options={[{ value: allKinds, label: 'Все виды' }, ...authorMemoryKinds.map(({ value, label }) => ({ value, label }))]} value={kind ?? allKinds} />
        </div>
        <SearchField className={styles.search} label="Поиск по записям" maxLength={200}
          onChange={(event) => setSearchDraft(event.target.value)}
          onClear={() => { setSearchDraft(''); goTo({ q: undefined }); }} value={searchDraft} />
      </form>
      {memory.isPending ? <Skeleton aria-label="Загружаем память" className={styles.skeleton} />
        : memory.isError ? (
          <Alert tone="error"><p>Не удалось загрузить память.</p>
            <Button onClick={() => void memory.refetch()} variant="outline">Повторить</Button></Alert>
        ) : records.length === 0 ? (
          filtered
            ? <EmptyState description="Попробуйте другой вид или другие слова." title="Ничего не найдено" />
            : <EmptyState description={canEdit
              ? 'Скажите помощнику в чате: «запомни», и запись появится здесь. Или добавьте её сами.'
              : 'Записи появятся, когда редактор саги их добавит.'} title="Пока ничего нет" />
        ) : (
          <>
            <p className={styles.count} role="status">{total} {pluralize(total, 'запись', 'записи', 'записей')}</p>
            <ul aria-label="Записи памяти" className={styles.list}>
              {records.map((record) => (
                <RecordItem canEdit={canEdit} key={record.id} onDelete={() => setDialog({ type: 'delete', record })}
                  onEdit={() => setDialog({ type: 'edit', record })} record={record} />
              ))}
            </ul>
            {memory.hasNextPage ? (
              <Button disabled={memory.isFetchingNextPage} onClick={() => void memory.fetchNextPage()} variant="outline">Показать ещё</Button>
            ) : null}
            {memory.isFetchNextPageError ? <Alert tone="error">Не удалось загрузить ещё.</Alert> : null}
          </>
        )}
      {dialog?.type === 'add' || dialog?.type === 'edit' ? (
        <RecordDialog api={api} onClose={() => setDialog(null)} projectId={projectId} record={dialog.type === 'edit' ? dialog.record : undefined} />
      ) : null}
      {dialog?.type === 'delete' ? <DeleteDialog api={api} onClose={() => setDialog(null)} projectId={projectId} record={dialog.record} /> : null}
    </div>
  );
}

function RecordItem({ canEdit, onDelete, onEdit, record }: { canEdit: boolean; onDelete: () => void; onEdit: () => void; record: AuthorMemoryRecord }) {
  return (
    <li className={styles.record}>
      <div className={styles.recordHead}>
        <Badge tone={kindTones[record.kind]}>{authorMemoryKindLabel(record.kind)}</Badge>
        {record.chapter ? <span className={styles.link}>{record.chapter.title}</span> : null}
        {record.name ? <span className={styles.link}>{record.name}</span> : null}
        {canEdit ? (
          <span className={styles.actions}>
            <IconButton aria-label="Изменить запись" onClick={onEdit} size="sm" title="Изменить"><Pencil aria-hidden="true" size={15} /></IconButton>
            <IconButton aria-label="Удалить запись" onClick={onDelete} size="sm" title="Удалить"><Trash2 aria-hidden="true" size={15} /></IconButton>
          </span>
        ) : null}
      </div>
      <p className={styles.text}>{record.text}</p>
      <p className={styles.meta}>{record.author}, {recordDate(record.createdAt)}</p>
    </li>
  );
}

function RecordDialog({ api, onClose, projectId, record }: { api: AuthorMemoryApiClient; onClose: () => void; projectId: string; record?: AuthorMemoryRecord }) {
  const [kind, setKind] = useState<AuthorMemoryKind>(record?.kind ?? 'plan');
  const [text, setText] = useState(record?.text ?? '');
  const [chapter, setChapter] = useState(record?.chapter?.key ?? '');
  const [name, setName] = useState(record?.name ?? '');
  const createAttempt = useRef<{ fingerprint: string; key: string } | null>(null);
  const save = useAuthorMemoryMutation(projectId, (draft: AuthorMemoryDraft) => {
    if (record) return api.updateAuthorMemory(projectId, record.id, { ...draft, expectedRevision: record.revision });
    const fingerprint = JSON.stringify(draft);
    if (createAttempt.current?.fingerprint !== fingerprint) {
      createAttempt.current = { fingerprint, key: createIdempotencyKey('createAuthorMemory') };
    }
    return api.createAuthorMemory(projectId, draft, { idempotencyKey: createAttempt.current.key });
  });
  const tooLong = Array.from(text).length > maxTextLength;
  const ready = text.trim().length > 0 && !tooLong;
  const hint = authorMemoryKinds.find((item) => item.value === kind)?.hint;

  function submit(event: FormEvent) {
    event.preventDefault();
    if (!ready || save.isPending) return;
    const key = chapter.trim();
    const label = name.trim();
    save.mutate({ kind, text: text.trim(), ...(key ? { chapter: key } : {}), ...(label ? { name: label } : {}) }, { onSuccess: onClose });
  }

  return (
    <Dialog width={560} canDismiss={!save.isPending} label={record ? 'Изменить запись' : 'Новая запись'} onDismiss={onClose}>
      <form className={styles.form} onSubmit={submit}>
        <h2 className={styles.formTitle}>{record ? 'Изменить запись' : 'Новая запись'}</h2>
        <Select<AuthorMemoryKind> label="Вид записи" onValueChange={setKind}
          options={authorMemoryKinds.map(({ value, label }) => ({ value, label }))} value={kind} />
        {hint ? <p className={styles.hint}>{hint}</p> : null}
        <Textarea error={tooLong ? `Не больше ${maxTextLength} знаков.` : undefined} label="Текст" onChange={(event) => setText(event.target.value)} rows={6} value={text} />
        <div className={styles.formRow}>
          <Field hint="Необязательно. Номер главы, например 481." label="Глава" maxLength={32} onChange={(event) => setChapter(event.target.value)} value={chapter} />
          <Field hint="Необязательно. Герой или название, о котором запись." label="Имя" maxLength={200} onChange={(event) => setName(event.target.value)} value={name} />
        </div>
        {save.isError ? <Alert tone="error">{publicApiErrorMessage(save.error, 'Не удалось сохранить запись.', saveMessages)}</Alert> : null}
        <div className={styles.formActions}>
          <Button disabled={save.isPending} onClick={onClose} type="button" variant="ghost">Отмена</Button>
          <Button disabled={!ready || save.isPending} type="submit" variant="primary">Сохранить</Button>
        </div>
      </form>
    </Dialog>
  );
}

function DeleteDialog({ api, onClose, projectId, record }: { api: AuthorMemoryApiClient; onClose: () => void; projectId: string; record: AuthorMemoryRecord }) {
  const remove = useAuthorMemoryMutation(projectId, () => api.deleteAuthorMemory(projectId, record.id, record.revision));
  const gone = isApiStatusFailure(remove.error) && remove.error.status === 404;
  return (
    <Dialog width={560} canDismiss={!remove.isPending} label="Удалить запись" onDismiss={onClose}>
      <div className={styles.form}>
        <h2 className={styles.formTitle}>Удалить запись?</h2>
        <p className={styles.deleted}>{record.text}</p>
        <p className={styles.hint}>Помощник перестанет её учитывать. Отменить удаление нельзя.</p>
        {remove.isError ? <Alert tone="error">{gone ? 'Этой записи уже нет.' : publicApiErrorMessage(remove.error, 'Не удалось удалить запись.', saveMessages)}</Alert> : null}
        <div className={styles.formActions}>
          <Button disabled={remove.isPending} onClick={onClose} variant="ghost">Отмена</Button>
          <Button disabled={remove.isPending} onClick={() => remove.mutate(undefined, { onSuccess: onClose })} variant="primary">Удалить</Button>
        </div>
      </div>
    </Dialog>
  );
}
