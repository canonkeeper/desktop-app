import { ApiStatusError } from '../shared/api';
import { Button } from '../ui/controls/Button';
import { SupportReference } from '../ui/feedback/SupportReference';
import { Dialog } from '../ui/overlays/Dialog';
import styles from '../App.module.css';

interface ServiceUnavailableProps {
  attempt: number;
  fullPage?: boolean;
  requestId?: string;
  retrying: boolean;
  onRetry(): void;
}

export function ServiceUnavailable({ attempt, fullPage = false, onRetry, requestId, retrying }: ServiceUnavailableProps) {
  const notice = (
    <section className={styles.outageCard} role="alert">
      <div className={styles.outageCopy}>
        {fullPage
          ? <h1 className={styles.outageTitle}>Сервис временно недоступен</h1>
          : <h2 className={styles.outageTitle}>Сервис временно недоступен</h2>}
        <p className={styles.outageMessage}>
          {fullPage
            ? 'Не удалось связаться с Canon Keeper. Ваши данные в безопасности — повторите попытку через несколько секунд.'
            : 'Страница остаётся на месте, но действия заблокированы, пока соединение не восстановится.'}
        </p>
      </div>
      <p className={styles.outageMeta}>Попытка {attempt}</p>
      {requestId ? <SupportReference code={requestId} /> : null}
      <Button loading={retrying} onClick={onRetry} variant="primary">
        {retrying ? 'Проверяем…' : 'Повторить'}
      </Button>
    </section>
  );

  return fullPage
    ? <main className={styles.outagePage}>{notice}</main>
    : <Dialog surface="content" canDismiss={false} label="Сервис временно недоступен" onDismiss={() => undefined} scrimClassName={styles.outageScrim}>{notice}</Dialog>;
}

export function requestIdFrom(error: unknown) {
  return error instanceof ApiStatusError ? error.problem.requestId : undefined;
}
