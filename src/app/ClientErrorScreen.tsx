import { useEffect } from 'react';
import { Button } from '../ui/controls/Button';
import { isChunkLoadError, reportClientError, shouldReloadAfterChunkFailure } from './client-errors';
import styles from './ClientErrorScreen.module.css';

interface ClientErrorScreenProps {
  error: unknown;
  reload?: () => void;
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
 * Экран ошибки рендера (R5.1). Отказ загрузки кода после выпуска один раз перезагружает
 * страницу (R5.2); повтор в течение минуты оставляет экран с кнопкой «Обновить».
 */
export function ClientErrorScreen({ error, reload = reloadPage }: ClientErrorScreenProps) {
  useEffect(() => {
    const chunk = isChunkLoadError(error);
    reportClientError(chunk ? 'chunk' : 'render', error);
    if (chunk && shouldReloadAfterChunkFailure(sessionStore())) reload();
  }, [error, reload]);

  return (
    <main className={styles.page}>
      <section className={styles.card} role="alert">
        <h1 className={styles.title}>Что-то пошло не так</h1>
        <p className={styles.message}>
          Страница не смогла открыться. Ваши книги, заметки и чаты сохранены — обновите страницу.
        </p>
        <Button onClick={reload} variant="primary">Обновить</Button>
      </section>
    </main>
  );
}
