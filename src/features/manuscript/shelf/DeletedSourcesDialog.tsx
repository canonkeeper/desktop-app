import { useQueryClient } from '@tanstack/react-query';
import { CircleAlert, CircleCheck, RotateCcw, X } from 'lucide-react';
import { useRef, useState } from 'react';
import { invalidateAccountQuota, quotaFailureCode, useAccountQuota } from '../../../entities/account-quota/api';
import { isApiStatusFailure } from '../../../entities/api-errors';
import type {
  DeletedBook,
  DeletedChapter,
  DeletedProject,
  ManuscriptApiClient,
  RestoredSource,
} from '../../../entities/manuscript/api';
import {
  manuscriptQueryKeys,
} from '../../../entities/manuscript/api';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { pluralize } from '../../../ui/copy';
import { Dialog } from '../../../ui/overlays/Dialog';
import {
  useDeletedBooksQuery,
  useDeletedChaptersQuery,
  useDeletedProjectsQuery,
  useRestoreDeletedSourceMutation,
} from '../api';
import styles from './DeletedSourcesDialog.module.css';
import { sourceQuotaMessage } from './quota-messages';

type DeletedSourceKind = RestoredSource['kind'];
type DeletedSource = DeletedProject | DeletedBook | DeletedChapter;
type RestoreNotice = {
  detail?: string;
  title: string;
  tone: 'error' | 'success';
};

interface DeletedSourcesDialogProps {
  api: ManuscriptApiClient;
  bookId: string | null;
  onClose: () => void;
  onRestored: (source: RestoredSource) => void;
  projectId: string | null;
}

const dateFormatter = new Intl.DateTimeFormat('ru-RU', {
  dateStyle: 'medium',
  timeStyle: 'short',
});

export function DeletedSourcesDialog({ api, bookId, onClose, onRestored, projectId }: DeletedSourcesDialogProps) {
  const queryClient = useQueryClient();
  const projects = useDeletedProjectsQuery(api, true);
  const books = useDeletedBooksQuery(api, projectId, true);
  const chapters = useDeletedChaptersQuery(api, bookId, true);
  const restore = useRestoreDeletedSourceMutation(api);
  const quota = useAccountQuota(api).data;
  const [pendingKey, setPendingKey] = useState('');
  const [retryKey, setRetryKey] = useState('');
  const [notice, setNotice] = useState<RestoreNotice | null>(null);
  const pendingKeyRef = useRef('');

  async function restoreSource(kind: DeletedSourceKind, source: DeletedSource) {
    const key = `${kind}:${source.id}`;
    // Восстанавливаем только один источник за раз, чтобы повторный клик не отправил второй запрос.
    if (pendingKeyRef.current) return;
    pendingKeyRef.current = key;
    setPendingKey(key);
    setRetryKey('');
    setNotice(null);
    try {
      const restored = await restore.mutateAsync({ id: source.id, kind });
      onRestored(restored);
      setNotice({ title: 'Источник восстановлен.', tone: 'success' });
    } catch (error) {
      const quotaCode = quotaFailureCode(error);
      if (quotaCode) {
        // Over a quota the source stays in «Недавно удалённые»: the text says why (R3.4, R4.2).
        void invalidateAccountQuota(queryClient);
        setNotice({ title: 'Не удалось восстановить.', detail: sourceQuotaMessage(quotaCode, quota), tone: 'error' });
      } else if (isApiStatusFailure(error) && error.status === 409) {
        await queryClient.invalidateQueries({ queryKey: deletedQueryKey(kind, projectId, bookId), exact: true });
        setNotice(Date.parse(source.restoreUntil) <= Date.now()
          ? { detail: 'Обновите список удалённых источников.', title: 'Срок восстановления истёк', tone: 'error' }
          : { detail: 'Список обновлён.', title: 'Источник уже восстановлен или удалён другим участником.', tone: 'error' });
      } else if (isApiStatusFailure(error) && error.status === 401) {
        setNotice({ title: 'Время входа истекло. Войдите снова.', tone: 'error' });
      } else if (isApiStatusFailure(error) && (error.status === 403 || error.status === 404)) {
        await queryClient.invalidateQueries({ queryKey: deletedQueryKey(kind, projectId, bookId), exact: true });
        setNotice({ title: 'Источник недоступен.', tone: 'error' });
      } else {
        setRetryKey(key);
        setNotice({ detail: 'Попробуйте снова.', title: 'Не удалось восстановить источник.', tone: 'error' });
      }
    } finally {
      pendingKeyRef.current = '';
      setPendingKey('');
    }
  }

  return (
    <Dialog surface="content" dismissOnScrim label="Недавно удалённые" onDismiss={onClose} scrimClassName={styles.backdrop}>
      <section className={styles.dialog}>
        <div aria-hidden="true" className={styles.sheetHandle} />
        <header className={styles.header}>
          <div>
            <h2>Недавно удалённые</h2>
            <p>Материалы можно восстановить, пока не истёк срок хранения.</p>
          </div>
          <IconButton aria-label="Закрыть недавно удалённые" onClick={onClose}>
            <X aria-hidden="true" size={20} />
          </IconButton>
        </header>

        {notice ? <RestoreNoticeMessage notice={notice} /> : null}

        <div aria-busy={projects.isLoading || books.isLoading || chapters.isLoading} className={styles.sections}>
          <DeletedSection
            empty="Нет удалённых саг."
            error={projects.error}
            kind="project"
            label="Сага"
            loading={projects.isLoading}
            onRetry={() => void projects.refetch()}
            onRestore={restoreSource}
            pendingKey={pendingKey}
            retryKey={retryKey}
            sources={projects.data?.data ?? []}
            title="Саги"
          />
          <DeletedSection
            empty={projectId ? 'В этой саге нет удалённых книг.' : 'Выберите сагу.'}
            error={books.error}
            kind="book"
            label="Книга"
            loading={books.isLoading}
            onRetry={() => void books.refetch()}
            onRestore={restoreSource}
            pendingKey={pendingKey}
            retryKey={retryKey}
            sources={books.data?.data ?? []}
            title="Книги"
          />
          <DeletedSection
            empty={bookId ? 'В этой книге нет удалённых глав.' : 'Выберите активную книгу.'}
            error={chapters.error}
            kind="chapter"
            label="Глава"
            loading={chapters.isLoading}
            onRetry={() => void chapters.refetch()}
            onRestore={restoreSource}
            pendingKey={pendingKey}
            retryKey={retryKey}
            sources={chapters.data?.data ?? []}
            title="Главы"
          />
        </div>
      </section>
    </Dialog>
  );
}

function DeletedSection({
  empty,
  error,
  kind,
  label,
  loading,
  onRetry,
  onRestore,
  pendingKey,
  retryKey,
  sources,
  title,
}: {
  empty: string;
  error: unknown;
  kind: DeletedSourceKind;
  label: string;
  loading: boolean;
  onRetry: () => void;
  onRestore: (kind: DeletedSourceKind, source: DeletedSource) => void;
  pendingKey: string;
  retryKey: string;
  sources: DeletedSource[];
  title: string;
}) {
  const retryLabel = kind === 'project' ? 'саг' : kind === 'book' ? 'книг' : 'глав';
  return (
    <section aria-label={title} className={styles.section}>
      <h3 className={styles.visuallyHidden}>{title}</h3>
      {loading ? <p className={styles.muted}>Загружаем…</p> : null}
      {!loading && error ? <div className={styles.sectionError} role="alert">
        <span>{sectionError(error)}</span>
        <Button aria-label={`Повторить загрузку ${retryLabel}`} onClick={onRetry} variant="ghost">Повторить</Button>
      </div> : null}
      {!loading && !error && sources.length === 0 ? <p className={styles.muted}>{empty}</p> : null}
      {!loading && !error && sources.length > 0 ? (
        <ul className={styles.list}>
          {sources.map((source) => {
            const key = `${kind}:${source.id}`;
            const restoreUntil = formatDate(source.restoreUntil);
            const remainingDays = remainingRetentionDays(source.restoreUntil);
            const remainingText = remainingDays > 0
              ? `осталось ${remainingDays} ${pluralize(remainingDays, 'день', 'дня', 'дней')}`
              : 'срок истёк';
            return (
              <li className={styles.row} key={source.id}>
                <div className={styles.sourceCopy}>
                  <span className={styles.sourceKind}>{label}</span>
                  <strong>{source.title}</strong>
                  <span className={styles.sourceMeta}>
                    Удалена {formatDate(source.deletedAt)} · {remainingText}
                  </span>
                </div>
                <Button
                  aria-label={`${retryKey === key ? 'Повторить' : 'Восстановить'} ${source.title} до ${restoreUntil} · ${remainingText}`}
                  className={styles.restoreButton}
                  disabled={Boolean(pendingKey)}
                  loading={pendingKey === key}
                  onClick={() => void onRestore(kind, source)}
                >
                  {pendingKey === key ? null : <RotateCcw aria-hidden="true" size={16} />}
                  {pendingKey === key ? 'Восстанавливаем…' : retryKey === key ? 'Повторить' : 'Восстановить'}
                </Button>
              </li>
            );
          })}
        </ul>
      ) : null}
    </section>
  );
}

function RestoreNoticeMessage({ notice }: { notice: RestoreNotice }) {
  const Icon = notice.tone === 'success' ? CircleCheck : CircleAlert;
  return (
    <div
      aria-live="polite"
      className={`${styles.notice} ${styles[notice.tone]}`}
      role={notice.tone === 'error' ? 'alert' : 'status'}
    >
      <Icon aria-hidden="true" size={18} />
      <div>
        <strong>{notice.title}</strong>
        {notice.detail ? <span>{notice.detail}</span> : null}
      </div>
    </div>
  );
}

function sectionError(error: unknown) {
  if (isApiStatusFailure(error)) {
    if (error.status === 401) return 'Время входа истекло. Войдите снова.';
    if (error.status === 403 || error.status === 404) return 'Недоступно.';
  }
  return 'Не удалось загрузить раздел.';
}

function deletedQueryKey(kind: DeletedSourceKind, projectId: string | null, bookId: string | null) {
  switch (kind) {
    case 'project':
      return manuscriptQueryKeys.deletedProjects();
    case 'book':
      return manuscriptQueryKeys.deletedBooks(projectId);
    case 'chapter':
      return manuscriptQueryKeys.deletedChapters(bookId);
  }
}

function formatDate(value: string) {
  const parsed = new Date(value);
  return Number.isNaN(parsed.valueOf()) ? 'указанного срока' : dateFormatter.format(parsed);
}

function remainingRetentionDays(value: string, now = Date.now()) {
  const end = Date.parse(value);
  if (Number.isNaN(end)) return 0;
  return Math.max(0, Math.ceil((end - now) / 86_400_000));
}
