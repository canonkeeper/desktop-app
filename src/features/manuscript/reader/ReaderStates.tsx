import { ArrowLeft, BookOpen, Info, RotateCcw, X } from 'lucide-react';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { EmptyState } from '../../../ui/feedback/EmptyState';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { Skeleton } from '../../../ui/feedback/Skeleton';
import type { ReaderSourceProblem } from './reader-locator';
import styles from './ReaderStates.module.css';

/** The one name of the way back from every state of the reader (RD3.5). */
export const backWord = 'К книгам';

export function ReaderLoadingState() {
  return (
    // The form of a chapter, announced as busy, not by words (specs/2026-11-10-ux2-patterns-states.md PS3.1).
    <section aria-busy="true" aria-label="Глава" className={styles.loadingState} data-loading="">
      <Skeleton className={styles.loadingTitle} />
      <Skeleton className={styles.loadingLine} />
      <Skeleton className={styles.loadingLine} />
      <Skeleton className={styles.loadingLineShort} />
    </section>
  );
}

/** A link that no longer leads to its place (RD3.4): what the line over the text says; the chapter opens all the same. */
export type ReaderStaleLink = Exclude<ReaderSourceProblem, 'unavailable'> | 'unavailable';

const staleWords: Readonly<Record<ReaderStaleLink, string>> = {
  version: 'Глава изменилась после правки — показываю с начала. Абзац, на который вела ссылка, мог сдвинуться',
  paragraph: 'Этого абзаца в главе больше нет — показываю главу с начала',
  invalid: 'Не получилось открыть место по ссылке — показываю начало главы',
  unavailable: 'Главы, на которую вела ссылка, в книге больше нет — показываю первую главу',
};

/**
 * The line over the text of a link that went stale (specs/2026-11-10-ux7-reader.md RD3.4; Pattern/Reader/Stale Line): the
 * chapter stands open, the line says why it starts from the top, and «×» takes it away with the old place of the link.
 */
export function StaleLinkLine({ kind, onClose }: { kind: ReaderStaleLink; onClose: () => void }) {
  return (
    <p className={styles.stale} role="status">
      <Info aria-hidden="true" size={16} />
      <span>{staleWords[kind]}</span>
      <IconButton aria-label="Закрыть строку" onClick={onClose} size="touch"><X aria-hidden="true" size={16} /></IconButton>
    </p>
  );
}

/** A book without chapters (RD3.5): the deleted ones come back from «Недавно удалённые», the way back is «К книгам». */
export function ReaderEmptyState({ onBack, onRestore }: { onBack: () => void; onRestore?: (() => void) | undefined }) {
  return (
    <section aria-label="Глав нет" className={styles.workspaceState}>
      <EmptyState
        action={onRestore ? <Button onClick={onRestore} variant="primary"><RotateCcw aria-hidden="true" size={16} />Вернуть удалённые главы</Button> : undefined}
        icon={<BookOpen size={28} />}
        need={onRestore ? undefined : 'Главы добавляет владелец саги или её редактор.'}
        title="В этой книге пока нет глав"
        why={onRestore ? 'Удалённые главы хранятся в «Недавно удалённых» — их можно вернуть.' : undefined}
      />
      <Button onClick={onBack} variant="ghost"><ArrowLeft aria-hidden="true" size={16} />{backWord}</Button>
    </section>
  );
}

/** No right to the chapter (RD3.5): the one block of errors, the way back is «К книгам». */
export function ReaderForbiddenState({ message, onBack }: { message: string; onBack: () => void }) {
  return (
    <section aria-label="Нет доступа" className={styles.workspaceState}>
      <ErrorBlock action={{ label: backWord, onClick: onBack }} cause={message || 'У вас нет доступа к этой саге или книге.'} kept="" title="Нет доступа к книге" />
    </section>
  );
}

export function ReaderErrorState({ message, onRetry, supportCode }: { message: string; onRetry: () => void; supportCode?: string | undefined }) {
  return (
    <section aria-label="Ошибка" className={styles.workspaceState}>
      <ErrorBlock cause={message} code={supportCode} onRetry={onRetry} title="Не открылась глава" />
    </section>
  );
}
