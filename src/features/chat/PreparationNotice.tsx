import {
  textSourceView,
  useIndexingStatusQuery,
  type IndexingStatus,
  type ManuscriptApiClient,
} from '../../entities/manuscript/api';
import { Button } from '../../ui/controls/Button';
import { Alert } from '../../ui/feedback/Alert';
import styles from './ResearchChatPanel.module.css';

type PreparationContext = NonNullable<IndexingStatus['published']['context']>;

interface PreparationNoticeProps {
  api: Pick<ManuscriptApiClient, 'getIndexingStatus'>;
  /** The preparation of the chat's text version; a ready one shows nothing. */
  context: PreparationContext;
  onOpenBooks: () => void;
  projectId: string;
}

/**
 * UC2.3: while the saga is still being prepared, the chat says it answers from the prepared
 * part and which book it is on (R2.3.3). The cards of the current book are followed until the
 * preparation ends; the notice goes away with it.
 */
export function PreparationNotice({ api, context, onOpenBooks, projectId }: PreparationNoticeProps) {
  const book = context.state === 'processing' ? context.currentBook : null;
  const bookStatus = useIndexingStatusQuery(api, projectId, {
    bookId: book?.bookId ?? '', enabled: Boolean(book), follow: true, sourceView: textSourceView,
  });
  if (context.state === 'ready') return null;
  const cards = book ? bookStatus.data?.steps.find((step) => step.kind === 'cards')?.counters : undefined;
  const progress = book
    ? `: книга ${book.position} из ${book.total}${cards?.totalChapters ? `, карточки глав ${cards.indexedChapters ?? 0} из ${cards.totalChapters}` : ''}`
    : '';
  // Advice, not an alarm: its counters change every few seconds and are not announced.
  return (
    <Alert aria-label="Подготовка саги" className={styles.preparation} role="note" tone="info">
      {context.state === 'processing' ? (
        <>
          <span>Книги ещё готовятся{progress}. Пока отвечаю по готовой части — ответ может быть неполным.</span>
          <Button onClick={onOpenBooks} size="sm" variant="ghost">Подробнее на полке</Button>
        </>
      ) : (
        <>
          <span>Часть глав не подготовлена. Ответы — по готовой части.</span>
          <Button onClick={onOpenBooks} size="sm" variant="ghost">К книгам</Button>
        </>
      )}
    </Alert>
  );
}
