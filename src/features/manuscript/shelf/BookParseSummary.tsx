import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, BookOpen, Pencil, X } from 'lucide-react';
import { useState } from 'react';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import { manuscriptQueryKeys, type Book, type ManuscriptApiClient } from '../../../entities/manuscript/api';
import { bookChaptersQueryKey, useBookChaptersQuery } from '../api';
import { Button } from '../../../ui/controls/Button';
import { formatCount } from '../../../ui/copy';
import { Dialog } from '../../../ui/overlays/Dialog';
import type { Toasts } from '../../../ui/feedback/useToastQueue';
import { looksLikeNoChapter, namedByFile } from './parse-summary';
import styles from './BookParseSummary.module.css';

type SummaryClient = Pick<ManuscriptApiClient, 'deleteChapter' | 'listChapters' | 'restoreChapter'>;

export interface BookParseSummaryProps {
  api: SummaryClient;
  books: readonly Book[];
  /** The file each book came from, when the upload names it. */
  sourceNames: Readonly<Record<string, string>>;
  onClose: () => void;
  onRead: (book: Book) => void;
  onRename: (book: Book) => void;
  toasts: Toasts;
}

/**
 * «Как разобралась книга» (specs/2026-11-11-ux7-upload.md UP4.2; board 35·27): how many chapters the file gave and their
 * titles, what looks like no chapter with «Убрать из книги», a book named by its file with «Переименовать». Several books —
 * first their list. The data is what the server knows of the books and their chapters; the actions are the operations the
 * shelf already has: a chapter goes to «Недавно удалённые» and comes back by «Вернуть».
 */
export function BookParseSummary({ api, books, onClose, onRead, onRename, sourceNames, toasts }: BookParseSummaryProps) {
  const [chosen, setChosen] = useState<string | null>(books.length === 1 ? books[0]!.id : null);
  const book = books.find((each) => each.id === chosen) ?? null;
  const title = book ? `Как разобралась книга «${book.title}»` : 'Как разобрались книги';
  return (
    <Dialog dismissOnScrim label={title} onDismiss={onClose} panelClassName={styles.panel} surface="content" width={600}>
      <section className={styles.window}>
        <header className={styles.header}>
          <h2 data-overlay-heading="">{title}</h2>
          <button aria-label="Закрыть" className={styles.close} onClick={onClose} type="button"><X aria-hidden="true" size={20} /></button>
        </header>
        <div className={styles.body}>
          {book ? (
            <>
              {books.length > 1 ? (
                <Button className={styles.back} onClick={() => setChosen(null)} size="sm" variant="ghost">
                  <ArrowLeft aria-hidden="true" size={14} />Все книги загрузки
                </Button>
              ) : null}
              <OneBook api={api} book={book} onRename={() => { onClose(); onRename(book); }} sourceName={sourceNames[book.id] ?? null} toasts={toasts} />
            </>
          ) : (
            <ul aria-label="Книги загрузки" className={styles.books}>
              {books.map((each) => (
                <li key={each.id}>
                  <span>Книга {each.place} «{each.title}» · {formatCount(each.chapterCount, 'глава', 'главы', 'глав')}</span>
                  <Button onClick={() => setChosen(each.id)} size="sm" variant="ghost">Посмотреть</Button>
                </li>
              ))}
            </ul>
          )}
        </div>
        <footer className={styles.footer}>
          <Button onClick={onClose} variant="outline">Закрыть</Button>
          {book ? <Button onClick={() => onRead(book)} variant="primary"><BookOpen aria-hidden="true" size={16} />Читать книгу</Button> : null}
        </footer>
      </section>
    </Dialog>
  );
}

function OneBook({ api, book, onRename, sourceName, toasts }: {
  api: SummaryClient;
  book: Book;
  onRename: () => void;
  sourceName: string | null;
  toasts: Toasts;
}) {
  const queryClient = useQueryClient();
  const chapters = useBookChaptersQuery(api, book.id);
  const refresh = () => Promise.all([
    queryClient.invalidateQueries({ queryKey: bookChaptersQueryKey(book.id), exact: true }),
    queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.shelf({ projectId: book.projectId }), exact: true }),
    queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.recentlyDeleted(), exact: false }),
  ]);
  const remove = useMutation({
    mutationFn: (chapter: { id: string; title: string }) => api.deleteChapter(chapter.id),
    onSuccess: async (_, chapter) => {
      await refresh();
      toasts.push({
        message: `«${chapter.title}» убрана из книги`, tone: 'success',
        action: <Button onClick={() => void api.restoreChapter(chapter.id).then(refresh)} size="sm" variant="ghost">Вернуть</Button>,
      });
    },
    onError: (error) => toasts.push({
      message: 'Не удалось убрать главу', detail: publicApiErrorMessage(error, 'Попробуйте ещё раз.'), tone: 'error',
    }),
  });
  const list = chapters.data?.data ?? [];
  const noChapters = list.filter((chapter) => looksLikeNoChapter(chapter.title));
  return (
    <>
      <p className={styles.count}>
        {chapters.isPending ? 'Читаю главы книги…'
          : `${formatCount(list.length, 'глава', 'главы', 'глав')} — так вы будете читать книгу и так её читает помощник.`}
      </p>
      {noChapters.map((chapter) => (
        <div className={styles.notChapter} key={chapter.id}>
          <p>Похоже, это не глава: «{chapter.title}».</p>
          <Button loading={remove.isPending && remove.variables?.id === chapter.id} onClick={() => remove.mutate(chapter)} size="sm" variant="outline">
            <X aria-hidden="true" size={14} />Убрать из книги
          </Button>
        </div>
      ))}
      {namedByFile(book.title, sourceName) ? (
        <div className={styles.fileName}>
          <p>Книга названа по имени файла: «{book.title}».</p>
          <Button onClick={onRename} size="sm" variant="ghost"><Pencil aria-hidden="true" size={14} />Переименовать</Button>
        </div>
      ) : null}
      {chapters.isError ? <p className={styles.count} role="alert">Не удалось прочитать главы книги.</p> : null}
      <ol aria-label={`Главы «${book.title}»`} className={styles.chapters}>
        {list.map((chapter) => <li key={chapter.id}>{chapter.title}</li>)}
      </ol>
    </>
  );
}
