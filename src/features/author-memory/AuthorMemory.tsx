import { loadFailureWords } from '../../entities/api-errors';
import { ErrorBlock } from '../../ui/feedback/ErrorBlock';
import { Pencil, Plus } from 'lucide-react';
import { useEffect, useRef, useState, type FormEvent, type MouseEvent, type ReactNode } from 'react';
import { isApiStatusFailure, publicApiErrorMessage } from '../../entities/api-errors';
import {
  authorMemoryKindLabel, authorMemoryKinds, createIdempotencyKey, useAuthorMemoryMutation, useAuthorMemoryQuery,
  type AuthorMemoryApiClient, type AuthorMemoryDraft, type AuthorMemoryKind, type AuthorMemoryRecord,
} from '../../entities/author-memory/api';
import { textSourceView } from '../../entities/manuscript/api';
import type { SagaApiClient } from '../../entities/saga/api';
import { ChapterChoice, type ChosenChapter } from '../../entities/saga/ui/ChapterChoice';
import { toHref, type AppRoute, type RouteNavigate } from '../../shared/navigation/app-route';
import { Button } from '../../ui/controls/Button';
import { SearchField } from '../../ui/controls/SearchField';
import { Select } from '../../ui/controls/Select';
import { Textarea } from '../../ui/controls/Textarea';
import { pluralize } from '../../ui/copy';
import { Alert } from '../../ui/feedback/Alert';
import { Badge } from '../../ui/feedback/Badge';
import { EmptyState } from '../../ui/feedback/EmptyState';
import { SkeletonArea } from '../../ui/feedback/Skeleton';
import { Confirm, deletionLines } from '../../ui/overlays/Confirm';
import { Dialog } from '../../ui/overlays/Dialog';
import { MoreMenu } from '../../ui/overlays/MoreMenu';
import { NameField } from './NameField';
import styles from './AuthorMemory.module.css';

const maxTextLength = 8000;
const allKinds = '';

/** The memory and the contents of the saga, which name the chapter of a record (ux1-names N5.2). */
type MemoryApi = AuthorMemoryApiClient & SagaApiClient;

export interface AuthorMemoryProps {
  api: MemoryApi;
  /** An editor of the saga: may add, change and remove records (R4.5). */
  canEdit: boolean;
  navigate: RouteNavigate;
  projectId: string;
  route: AppRoute;
}

const saveMessages = {
  codes: { 'author_memory.no_chapter': 'Этой главы в саге уже нет. Выберите другую.' },
  forbidden: 'Менять то, что помнит помощник, может редактор саги.',
  conflict: 'Запись уже изменили. Закройте окно и откройте её снова.',
};

/** «10 ноября»; a date of another year says the year too. */
function recordDate(value: string) {
  const date = new Date(value);
  const sameYear = date.getFullYear() === new Date().getFullYear();
  return date.toLocaleDateString('ru-RU', { day: 'numeric', month: 'long', ...(sameYear ? {} : { year: 'numeric' }) });
}

/**
 * «Вы просили помнить», the part of «Что помнит помощник» with the records of the saga (specs/2026-11-10-ux2-memory-page.md
 * MP1.2; specs/2026-11-01-writer-assistant-index.md, R4.5): the filters live in the URL, the heading is the page's.
 */
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
  // MR1.5: the author of a record is named only in a saga of several members.
  const shared = memory.data?.pages[0]?.shared ?? false;
  const filtered = Boolean(kind || urlQuery);
  const goTo = (next: { kind?: string | undefined; q?: string | undefined }) => navigate('/memory', {
    projectId, kind: 'kind' in next ? next.kind : kind, q: 'q' in next ? next.q : urlQuery || undefined,
  }, { replace: true });
  const openChat = () => navigate('/chat', { projectId });

  useEffect(() => { setSearchDraft(urlQuery); }, [urlQuery]);

  return (
    <div className={styles.part}>
      <div className={styles.tools}>
        <form className={styles.filters} onSubmit={(event) => { event.preventDefault(); goTo({ q: searchDraft.trim() || undefined }); }} role="search">
          <div className={styles.kindFilter}>
            {/* The words of the author (specs/2026-11-11-ux9-shelf-chat.md SC2.4, W-24): «Что это», «Всё», «Записать самому». */}
            <Select<AuthorMemoryKind | typeof allKinds> label="Что это" onValueChange={(value) => goTo({ kind: value || undefined })}
              options={[{ value: allKinds, label: 'Всё' }, ...authorMemoryKinds.map(({ value, label }) => ({ value, label }))]} value={kind ?? allKinds} />
          </div>
          <SearchField className={styles.search} label="Найти в записях" maxLength={200}
            onChange={(event) => setSearchDraft(event.target.value)}
            onClear={() => { setSearchDraft(''); goTo({ q: undefined }); }} value={searchDraft} />
        </form>
        {canEdit ? <Button className={styles.add} onClick={() => setDialog({ type: 'add' })} variant="primary"><Plus aria-hidden="true" size={16} />Записать самому</Button> : null}
      </div>
      {memory.isPending ? <SkeletonArea count={3} shape="rows" />
        : memory.isError ? (
          <ErrorBlock {...loadFailureWords(memory.error)} onRetry={() => void memory.refetch()} retrying={memory.isRefetching} title="Не открылось «Что помнит помощник»" />
        ) : records.length === 0 ? (
          filtered
            ? <EmptyState why="Попробуйте другой вид или другие слова." title="Ничего не найдено" />
            : canEdit
              ? <EmptyState action={<Button onClick={openChat} variant="outline">Открыть чат</Button>}
                why="Здесь будут планы, поправки и правила стиля, которые помощник должен держать в уме. Скажите помощнику в чате: «запомни…» — или добавьте запись сами."
                title="Пока ничего нет" />
              // The word of the line of the role under the top bar (specs/2026-11-12-ux12-shell.md SH4.3, W-82).
              : <EmptyState why="Записи появятся, когда владелец саги их добавит." title="Пока ничего нет" />
        ) : (
          <>
            <p className={styles.count} role="status">{total} {pluralize(total, 'запись', 'записи', 'записей')}</p>
            <ul aria-label="Записи" className={styles.list}>
              {records.map((record) => (
                <RecordItem canEdit={canEdit} key={record.id} navigate={navigate} onDelete={() => setDialog({ type: 'delete', record })}
                  onEdit={() => setDialog({ type: 'edit', record })} projectId={projectId} record={record} shared={shared} />
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

interface RecordItemProps {
  canEdit: boolean;
  navigate: RouteNavigate;
  onDelete: () => void;
  onEdit: () => void;
  projectId: string;
  record: AuthorMemoryRecord;
  shared: boolean;
}

function RecordItem({ canEdit, navigate, onDelete, onEdit, projectId, record, shared }: RecordItemProps) {
  const { chapter } = record;
  return (
    <li className={styles.record}>
      <div className={styles.recordHead}>
        {/* MR1.4: the kinds are calm marks of one tone; red and yellow belong to errors and warnings. */}
        <Badge>{authorMemoryKindLabel(record.kind)}</Badge>
        {chapter ? (
          <RouteLink className={styles.link} navigate={navigate} path="/manuscript/books"
            query={{ projectId, bookId: chapter.bookId, chapterId: chapter.chapterId, mode: 'read' }}>{chapter.label}</RouteLink>
        ) : record.chapterGone ? <span className={styles.link}>глава удалена</span> : null}
        {record.name ? <span className={styles.link}>{record.name}</span> : null}
        {canEdit ? (
          <span className={styles.actions}>
            {/* «Изменить» on top, «Удалить запись…» last and apart (PA3.2). */}
            <MoreMenu items={[{ label: 'Изменить', icon: Pencil, run: onEdit }]} label="Действия записи" remove={{ label: 'Удалить запись…', run: onDelete }} />
          </span>
        ) : null}
      </div>
      <p className={styles.text}>{record.text}</p>
      <p className={styles.origin}>
        <Origin navigate={navigate} projectId={projectId} record={record} /> · {recordDate(record.createdAt)}{shared ? ` · ${record.author}` : ''}
      </p>
    </li>
  );
}

/** Where a record came from (MR1.5): the chat by its title while it lives, the helper of a chapter or a drawing, or the page. */
function Origin({ navigate, projectId, record }: { navigate: RouteNavigate; projectId: string; record: AuthorMemoryRecord }) {
  const { origin } = record;
  switch (origin.kind) {
    case 'manual':
      return <>Добавлено вручную</>;
    case 'draft':
      return <>Из разговора с помощником главы{origin.title ? ` „${origin.title}“` : ''}</>;
    case 'drawing':
      return <>Из разговора с помощником рисунка</>;
    default:
      return origin.chatId && origin.title ? (
        <>Из чата <RouteLink className={styles.originLink} navigate={navigate} path="/chat" query={{ projectId, chatId: origin.chatId }}>„{origin.title}“</RouteLink></>
      ) : <>Из чата</>;
  }
}

/** A link of the app: opens in place, and in a new tab by the browser's own means. */
function RouteLink({ children, className, navigate, path, query }: {
  children: ReactNode; className: string | undefined; navigate: RouteNavigate; path: string; query: Record<string, string | undefined>;
}) {
  return (
    <a className={className} href={toHref(path, query)} onClick={(event: MouseEvent<HTMLAnchorElement>) => {
      if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
      event.preventDefault();
      navigate(path, query);
    }}>{children}</a>
  );
}

function RecordDialog({ api, onClose, projectId, record }: { api: MemoryApi; onClose: () => void; projectId: string; record?: AuthorMemoryRecord }) {
  const [kind, setKind] = useState<AuthorMemoryKind>(record?.kind ?? 'plan');
  const [text, setText] = useState(record?.text ?? '');
  // The chapter is chosen among the chapters of the saga by its place: the author neither sees nor types its key (N5.2, MR1.1).
  const [chapter, setChapter] = useState<ChosenChapter | null>(record?.chapter ? { key: record.chapter.key, label: record.chapter.label } : null);
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
    const label = name.trim();
    save.mutate({ kind, text: text.trim(), ...(chapter ? { chapter: chapter.key } : {}), ...(label ? { name: label } : {}) }, { onSuccess: onClose });
  }

  return (
    <Dialog width={560} canDismiss={!save.isPending} label={record ? 'Изменить запись' : 'Новая запись'} onDismiss={onClose}>
      <form className={styles.form} onSubmit={submit}>
        <h2 className={styles.formTitle}>{record ? 'Изменить запись' : 'Новая запись'}</h2>
        <Select<AuthorMemoryKind> label="Что это" onValueChange={setKind}
          options={authorMemoryKinds.map(({ value, label }) => ({ value, label }))} value={kind} />
        {hint ? <p className={styles.hint}>{hint}</p> : null}
        <Textarea error={tooLong ? `Не больше ${maxTextLength} знаков.` : undefined} label="Текст" onChange={(event) => setText(event.target.value)} rows={6} value={text} />
        <ChapterChoice api={api} onChange={setChapter} projectId={projectId} sourceView={textSourceView} value={chapter} />
        <NameField api={api} onChange={setName} projectId={projectId} value={name} />
        {save.isError ? <Alert tone="error">{publicApiErrorMessage(save.error, 'Не удалось сохранить запись.', saveMessages)}</Alert> : null}
        <div className={styles.formActions}>
          <Button disabled={save.isPending} onClick={onClose} type="button" variant="ghost">Отмена</Button>
          <Button disabled={!ready || save.isPending} type="submit" variant="primary">Сохранить</Button>
        </div>
      </form>
    </Dialog>
  );
}

/** «Удалить запись…» of the menu of a record (PA3.1): a record does not come back. */
function DeleteDialog({ api, onClose, projectId, record }: { api: AuthorMemoryApiClient; onClose: () => void; projectId: string; record: AuthorMemoryRecord }) {
  const remove = useAuthorMemoryMutation(projectId, () => api.deleteAuthorMemory(projectId, record.id, record.revision));
  const gone = isApiStatusFailure(remove.error) && remove.error.status === 404;
  return (
    <Confirm action="Удалить запись" also="Помощник перестанет её учитывать." busy={remove.isPending}
      error={remove.isError ? (gone ? 'Этой записи уже нет.' : publicApiErrorMessage(remove.error, 'Не удалось удалить запись.', saveMessages)) : null}
      irreversible onCancel={onClose} onConfirm={() => remove.mutate(undefined, { onSuccess: onClose })} title={`Удалить запись «${clip(record.text)}»?`}>
      {deletionLines.gone}
    </Confirm>
  );
}

/** A record in the question: its first words, so the author knows which one goes. */
function clip(text: string) {
  const words = text.trim().replace(/\s+/gu, ' ');
  return words.length > 60 ? `${words.slice(0, 60).trimEnd()}…` : words;
}
