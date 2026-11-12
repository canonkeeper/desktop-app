import { useQueryClient } from '@tanstack/react-query';
import { ArrowRight, CircleAlert, RotateCcw, Trash2, X } from 'lucide-react';
import { useRef, useState } from 'react';
import { invalidateAccountQuota, useAccountQuota } from '../../entities/account-quota/api';
import type { RefusalWords } from '../../entities/account-quota/refusal-words';
import { QuotaRefusal } from '../../entities/account-quota/ui/QuotaRefusal';
import { isApiStatusFailure, loadFailureWords } from '../../entities/api-errors';
import { manuscriptQueryKeys, type ManuscriptApiClient } from '../../entities/manuscript/api';
import { useRecentlyDeletedQuery, useRestoreDeletedSourceMutation } from '../../features/manuscript/api';
import { quotaRefusal } from '../../features/manuscript/shelf/restore-words';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { EmptyState } from '../../ui/feedback/EmptyState';
import { ErrorBlock } from '../../ui/feedback/ErrorBlock';
import { SkeletonArea } from '../../ui/feedback/Skeleton';
import type { ToastInput } from '../../ui/feedback/useToastQueue';
import { useHostToasts } from '../../ui/feedback/toast-host';
import { Dialog } from '../../ui/overlays/Dialog';
import { useDrawingReturn } from './drawing-return';
import {
  bookUpdateMeta, bookUpdateTitle, notReturnedWords, returnedPlace, returnedWords, rowsOf, trashGroups, trashMeta, trashTitle,
  type BookUpdateRow, type TrashRow,
} from './recently-deleted-words';
import styles from './RecentlyDeleted.module.css';

type Notice = { title: string; detail?: string; quotaCode?: string; words?: RefusalWords | null; waits?: string };

interface RecentlyDeletedProps {
  api: ManuscriptApiClient;
  onClose: () => void;
  /** «Открыть» of the line after «Вернуть»: the window steps aside for the place of what came back. */
  onOpen: (place: { path: string; query: Record<string, string> }) => void;
  /** The one area of the toasts of the root. */
  notify: (toast: ToastInput) => number;
  /**
   * «Вернуть…» of a book updated from a file (specs/2026-11-12-ux10-book-revert.md BR3.3): the one question of the return of
   * the shell, over the window.
   */
  onRevertBook: (book: BookUpdateRow) => void;
  /** The book whose question of the return is being read. */
  revertAsking: string | null;
}

const keyOf = (row: TrashRow) => `${row.kind}:${row.item.id}`;

/**
 * «Недавно удалённые» (specs/2026-11-11-ux7-trash.md TB1; board 32·23–32·24): one list of what the author deleted and may
 * return in every saga of his, by groups «Саги», «Книги», «Главы», «Рисунки» — what it is, where it was, when it goes for
 * good, «Вернуть». A return answers with the line of the root «… возвращена · Открыть» and the row leaves the list; a
 * return over the tier says why in the one block of refusals and the row stays.
 */
export function RecentlyDeleted({ api, notify, onClose, onOpen, onRevertBook, revertAsking }: RecentlyDeletedProps) {
  const queryClient = useQueryClient();
  const list = useRecentlyDeletedQuery(api, true);
  const restoreSource = useRestoreDeletedSourceMutation(api);
  const drawings = useDrawingReturn();
  const quota = useAccountQuota(api).data;
  const [pendingKey, setPendingKey] = useState('');
  const [retryKey, setRetryKey] = useState('');
  const [notice, setNotice] = useState<Notice | null>(null);
  const pending = useRef('');
  // The line after «Вернуть» stands in the window while it is open, in sight and in reach of the keyboard (PA1.1); the line
  // of a deletion made before stays out of it — its row with its own «Вернуть» is in the list (specs/2026-11-11-ux2-dictionary.md).
  const [toastHost, setToastHost] = useState<HTMLDivElement | null>(null);
  useHostToasts(toastHost, { onlyNew: true });

  async function giveBack(row: TrashRow) {
    // One return at a time: a second click sends no second request.
    if (pending.current) return;
    const key = keyOf(row);
    pending.current = key;
    setPendingKey(key);
    setRetryKey('');
    setNotice(null);
    try {
      if (row.kind === 'drawing') {
        if (!drawings) return;
        await drawings.restore(row.item.projectId, row.item.id);
        await queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.recentlyDeleted(), exact: true });
      } else {
        await restoreSource.mutateAsync({ id: row.item.id, kind: row.kind === 'saga' ? 'project' : row.kind });
      }
      notify({
        ...returnedWords(row),
        tone: 'success',
        action: <Button onClick={() => onOpen(returnedPlace(row))} size="sm" variant="ghost">Открыть<ArrowRight aria-hidden="true" size={14} /></Button>,
      });
    } catch (error) {
      const refusal = quotaRefusal(error, quota);
      if (refusal) {
        // Over the tier the row stays where it is; the block says why (TB1.3).
        void invalidateAccountQuota(queryClient);
        setNotice({ ...refusal, ...notReturnedWords(row) });
      } else if (isApiStatusFailure(error) && error.status === 409) {
        await queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.recentlyDeleted(), exact: true });
        setNotice(Date.parse(row.item.restoreUntil) <= Date.now()
          ? { title: 'Срок вышел — вернуть уже нельзя.', detail: 'Список обновлён.' }
          : { title: 'Уже возвращено или удалено другим участником.', detail: 'Список обновлён.' });
      } else if (isApiStatusFailure(error) && error.status === 401) {
        setNotice({ title: 'Время входа истекло. Войдите снова.' });
      } else if (isApiStatusFailure(error) && (error.status === 403 || error.status === 404)) {
        await queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.recentlyDeleted(), exact: true });
        setNotice({ title: 'Вернуть может владелец саги или автор.', detail: 'Список обновлён.' });
      } else {
        setRetryKey(key);
        setNotice({ title: 'Не удалось вернуть.', detail: 'Попробуйте снова.' });
      }
    } finally {
      pending.current = '';
      setPendingKey('');
    }
  }

  function renderGroup(group: { kind: TrashRow['kind']; heading: string; rows: TrashRow[]; total: number }) {
    return (
      <section aria-labelledby={`deleted-${group.kind}`} className={styles.section} key={group.kind}>
        <h3 id={`deleted-${group.kind}`}>{group.heading}</h3>
        <ul className={styles.list}>
          {group.rows.map((row) => {
            const key = keyOf(row);
            const title = trashTitle(row);
            const again = retryKey === key;
            const unavailable = row.kind === 'drawing' && !drawings;
            return (
              <li className={styles.row} key={key}>
                <div className={styles.copy}>
                  <strong>{title}</strong>
                  <span>{trashMeta(row)}</span>
                </div>
                <Button aria-label={`${again ? 'Повторить' : 'Вернуть'}: ${title}`} className={styles.restore}
                  data-voice-consent="no" disabled={Boolean(pendingKey) || unavailable} loading={pendingKey === key}
                  onClick={() => void giveBack(row)} variant="outline">
                  {/* A pressed button shows its own `loading` and keeps its word (PS3.2). */}
                  {pendingKey === key ? null : <RotateCcw aria-hidden="true" size={16} />}
                  {again ? 'Повторить' : 'Вернуть'}
                </Button>
              </li>
            );
          })}
        </ul>
        {group.total > group.rows.length ? (
          <p className={styles.more}>Показаны {group.rows.length} из {group.total} — сначала новые.</p>
        ) : null}
      </section>
    );
  }

  const data = list.data;
  const groups = data ? trashGroups.map((group) => ({ ...group, ...rowsOf(data, group.kind) })).filter((group) => group.rows.length > 0) : [];
  const updates = data?.bookUpdates ?? { items: [], total: 0 };
  return (
    <Dialog surface="content" dismissOnScrim label="Недавно удалённые" onDismiss={onClose} scrimClassName={styles.backdrop}>
      <section className={styles.dialog}>
        <div aria-hidden="true" className={styles.sheetHandle} />
        <header className={styles.header}>
          <div>
            <h2>Недавно удалённые</h2>
            <p>Можно вернуть 7 дней. Потом удалённое исчезнет совсем.</p>
          </div>
          <IconButton aria-label="Закрыть недавно удалённые" onClick={onClose}>
            <X aria-hidden="true" size={20} />
          </IconButton>
        </header>
        <div className={styles.toasts} ref={setToastHost} />

        {notice?.quotaCode && notice.words ? (
          <QuotaRefusal announce code={notice.quotaCode} place="shelf" words={notice.words}>
            <p>{notice.title} {notice.waits}</p>
          </QuotaRefusal>
        ) : notice ? (
          <div aria-live="polite" className={styles.notice} role="alert">
            <CircleAlert aria-hidden="true" size={18} />
            <div>
              <strong>{notice.title}</strong>
              {notice.detail ? <span>{notice.detail}</span> : null}
            </div>
          </div>
        ) : null}

        {list.isPending ? <SkeletonArea count={4} shape="rows" /> : null}
        {list.isError ? (
          <ErrorBlock {...loadFailureWords(list.error)} onRetry={() => void list.refetch()} retrying={list.isRefetching}
            title="Не открылся список удалённого" />
        ) : null}
        {data && groups.length === 0 && updates.items.length === 0 ? (
          <EmptyState icon={<Trash2 size={28} />} title="Здесь будет то, что вы удалите"
            why="Удалённую сагу, книгу, главу или рисунок можно вернуть 7 дней." />
        ) : null}
        {groups.length || updates.items.length ? (
          <div className={styles.sections}>
            {groups.filter((group) => group.kind !== 'drawing').map(renderGroup)}
            {updates.items.length ? (
              // BR3.3: the books updated from a file whose version before the update can still be returned, before «Рисунки».
              <section aria-labelledby="deleted-book-updates" className={styles.section}>
                <h3 id="deleted-book-updates">Обновления книг</h3>
                <ul className={styles.list}>
                  {updates.items.map((item) => {
                    const title = bookUpdateTitle(item);
                    return (
                      <li className={styles.row} key={`update:${item.id}`}>
                        <div className={styles.copy}>
                          <strong>{title}</strong>
                          <span>{bookUpdateMeta(item)}</span>
                        </div>
                        {/* The question of the return asks the author's «да» itself (BR2.5). */}
                        <Button aria-label={`Вернуть прежнюю версию: ${title}`} className={styles.restore} data-voice-consent="no"
                          disabled={Boolean(pendingKey) || (Boolean(revertAsking) && revertAsking !== item.id)} loading={revertAsking === item.id}
                          onClick={() => onRevertBook(item)} variant="outline">
                          {revertAsking === item.id ? null : <RotateCcw aria-hidden="true" size={16} />}
                          Вернуть…
                        </Button>
                      </li>
                    );
                  })}
                </ul>
                {updates.total > updates.items.length ? (
                  <p className={styles.more}>Показаны {updates.items.length} из {updates.total} — сначала новые.</p>
                ) : null}
              </section>
            ) : null}
            {groups.filter((group) => group.kind === 'drawing').map(renderGroup)}
          </div>
        ) : null}
      </section>
    </Dialog>
  );
}
