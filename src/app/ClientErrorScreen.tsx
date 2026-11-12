import { RotateCw } from 'lucide-react';
import { useEffect, useState } from 'react';
import { ButtonLink, Button } from '../ui/controls/Button';
import { SupportReference } from '../ui/feedback/SupportReference';
import { clientErrorCode, isChunkLoadError, reportClientError, shouldReloadAfterChunkFailure } from './client-errors';
import styles from './ClientErrorScreen.module.css';

interface ClientErrorScreenProps {
  error: unknown;
  reload?: () => void;
  /** The page failed inside the shell: the card takes the place of the page, the sections stay beside it. */
  inShell?: boolean;
}

function reloadPage() {
  window.location.reload();
}

function sessionStore() {
  try {
    return window.sessionStorage;
  } catch {
    return undefined;
  }
}

/**
 * Экран ошибки рендера (R5.1). Отказ загрузки кода после выпуска один раз перезагружает страницу (R5.2); повтор в течение
 * минуты оставляет экран. Не помогло «Обновить» — «К книгам» и «Написать нам» с кодом для поддержки; код стоит строкой под
 * кнопками (specs/2026-11-11-ux9-account.md AC3.2). Внутри оболочки «Написать нам» открывает окно сайта с кодом в
 * «Приложим», без оболочки — письмо с кодом.
 */
export function ClientErrorScreen({ error, inShell = false, reload = reloadPage }: ClientErrorScreenProps) {
  const [code] = useState(() => clientErrorCode(error));
  useEffect(() => {
    const chunk = isChunkLoadError(error);
    reportClientError(chunk ? 'chunk' : 'render', error);
    if (chunk && shouldReloadAfterChunkFailure(sessionStore())) reload();
  }, [error, reload]);

  const card = (
    <section aria-labelledby="client-error-title" className={styles.card} role="alert">
      {inShell
        ? <h2 className={styles.title} id="client-error-title">Что-то пошло не так</h2>
        : <h1 className={styles.title} id="client-error-title">Что-то пошло не так</h1>}
      <p className={styles.message}>
        Страница не смогла открыться. Ваши книги, заметки и чаты сохранены — обновите страницу.
      </p>
      <div className={styles.actions}>
        <Button onClick={reload} variant="primary"><RotateCw aria-hidden="true" size={15} />Обновить</Button>
        <ButtonLink href="/manuscript/books" variant="outline">К книгам</ButtonLink>
      </div>
      <p className={styles.more}>Не помогло — напишите нам, код ошибки приложим сами.</p>
      <SupportReference className={styles.code} code={code} />
    </section>
  );
  return inShell ? <div className={styles.inShell}>{card}</div> : <main className={styles.page}>{card}</main>;
}
