import { CircleAlert, RotateCw } from 'lucide-react';
import type { ReactNode } from 'react';
import { Button } from '../controls/Button';
import styles from './feedback.module.css';
import { SupportReference } from './SupportReference';

/** What every error of a section says of the author's work (specs/2026-11-10-ux2-patterns-refusals.md PR3.1; П-21). */
export const keptWords = 'Ваши книги и тексты на месте.';

export interface ErrorBlockProps {
  /** What did not open, in the words of the author: «Не открылась карта саги». */
  title: string;
  /** Why, without technical words: «Нет связи с сервером. Проверьте интернет.»; '' when the reason is not known. */
  cause?: string | undefined;
  /** The code support finds the failure by. */
  code?: string | null | undefined;
  /** «Повторить»; without it the block offers `action`. */
  onRetry?: (() => void) | undefined;
  retrying?: boolean | undefined;
  /** The one way on of a page that is not there: «К книгам» (PR3.5). */
  action?: { label: string; onClick: () => void } | undefined;
  /** What stays safe, when the place says it closer: «Ваш текст на месте». */
  kept?: string | undefined;
  className?: string | undefined;
  children?: ReactNode;
}

/**
 * The one block of an error of a section or a window (specs/2026-11-10-ux2-patterns-refusals.md PR3.1–PR3.3, PR3.5; board
 * 15·05, `Pattern/Common/Error Block`): what did not open · «Ваши книги и тексты на месте» · «Повторить» · the code for
 * support and «Написать нам». It takes the place of the content: the shell, the title of the section and the name of the
 * saga stay. It is announced as an error.
 */
export function ErrorBlock({ action, cause, children, className, code, kept = keptWords, onRetry, retrying = false, title }: ErrorBlockProps) {
  return (
    <div aria-label={title} className={`${styles.errorBlock}${className ? ` ${className}` : ''}`} role="alert">
      <CircleAlert aria-hidden="true" className={styles.errorIcon} size={16} />
      <div className={styles.errorBody}>
        <strong className={styles.errorTitle}>{title}</strong>
        <p>{cause ? `${cause} ` : ''}{kept}</p>
        {children}
        <div className={styles.errorActions}>
          {onRetry ? <Button loading={retrying} onClick={onRetry} size="sm" variant="outline"><RotateCw aria-hidden="true" size={14} />Повторить</Button> : null}
          {!onRetry && action ? <Button onClick={action.onClick} size="sm" variant="outline">{action.label}</Button> : null}
          {onRetry ? <SupportReference code={code} /> : null}
        </div>
      </div>
    </div>
  );
}
