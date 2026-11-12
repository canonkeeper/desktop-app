import { CloudOff, WifiOff } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { ApiStatusError } from '../shared/api';
import { Button } from '../ui/controls/Button';
import { SupportReference } from '../ui/feedback/SupportReference';
import styles from '../App.module.css';

interface ServiceUnavailableProps {
  attempt: number;
  fullPage?: boolean;
  requestId?: string;
  retrying: boolean;
  onRetry(): void;
  /**
   * The place of the notices above the open page (ui/feedback/notice-slot.ts): the strip stands there, the page under it
   * stays readable and its text can be copied (specs/2026-11-11-ux9-account.md AC3.3).
   */
  slot?: HTMLElement | null;
}

/** The pause before the next check by itself: 5 s, then twice as long each time, not over 30 s. */
export function nextCheckSeconds(attempt: number) {
  return Math.min(30, 5 * 2 ** Math.max(0, attempt - 1));
}

/** Whether this device has a network at all; the browser says it by `navigator.onLine` and its events. */
function useOnline() {
  const [online, setOnline] = useState(() => typeof navigator === 'undefined' || navigator.onLine !== false);
  useEffect(() => {
    const update = () => setOnline(navigator.onLine !== false);
    window.addEventListener('online', update);
    window.addEventListener('offline', update);
    return () => {
      window.removeEventListener('online', update);
      window.removeEventListener('offline', update);
    };
  }, []);
  return online;
}

/**
 * The service does not answer (specs/2026-11-11-ux9-account.md AC3.3): «Нет интернета на этом устройстве» when the device
 * has no network, «Сервис временно недоступен» when the service does not answer; the screen checks by itself — at once
 * when the network comes back, and after a pause that grows — and says «Проверяю… попытка N» in the words of the page.
 * Before the product it is a page; over an open page it is a strip above it, and the page stays readable.
 */
export function ServiceUnavailable({ attempt, fullPage = false, onRetry, requestId, retrying, slot }: ServiceUnavailableProps) {
  const online = useOnline();
  const retry = useRef(onRetry);
  retry.current = onRetry;
  const [left, setLeft] = useState(() => nextCheckSeconds(attempt));

  // The next check by itself: counted down by the second, started again after each attempt.
  useEffect(() => {
    if (retrying) return undefined;
    let seconds = nextCheckSeconds(attempt);
    setLeft(seconds);
    const timer = window.setInterval(() => {
      seconds -= 1;
      setLeft(seconds);
      if (seconds <= 0) {
        window.clearInterval(timer);
        retry.current();
      }
    }, 1_000);
    return () => window.clearInterval(timer);
  }, [attempt, retrying]);

  // The network of the device came back: check at once.
  useEffect(() => {
    const now = () => retry.current();
    window.addEventListener('online', now);
    return () => window.removeEventListener('online', now);
  }, []);

  const title = online ? 'Сервис временно недоступен' : 'Нет интернета на этом устройстве';
  const Icon = online ? CloudOff : WifiOff;
  const checking = retrying ? `Проверяю… попытка ${attempt}` : `Проверю снова через ${Math.max(1, left)} с`;

  if (fullPage) {
    return (
      <main className={styles.outagePage}>
        <section className={styles.outageCard} role="alert">
          <div className={styles.outageCopy}>
            <h1 className={styles.outageTitle}>{title}</h1>
            <p className={styles.outageMessage}>
              {online
                ? 'Не удалось связаться с CanonKeeper. Ваши данные в безопасности — проверяю связь сам и открою страницу, как только сервис ответит.'
                : 'Проверьте подключение к интернету — страница откроется сама, как только связь появится.'}
            </p>
          </div>
          <p className={styles.outageMeta} role="status">{checking}</p>
          {requestId ? <SupportReference code={requestId} /> : null}
          <Button loading={retrying} onClick={onRetry} variant="primary">Повторить</Button>
        </section>
      </main>
    );
  }

  const strip = (
    <section aria-atomic="true" aria-label={title} className={styles.outageStrip} role="alert">
      <Icon aria-hidden="true" className={styles.outageIcon} size={18} />
      <p className={styles.outageStripText}>
        <strong>{title}.</strong>{' '}
        Набранный текст на месте — отправить его можно будет, когда связь вернётся.
      </p>
      <span className={styles.outageMeta}>{checking}</span>
      {requestId ? <SupportReference code={requestId} /> : null}
      <Button loading={retrying} onClick={onRetry} size="sm" variant="outline">Повторить</Button>
    </section>
  );
  return slot ? createPortal(strip, slot) : strip;
}

export function requestIdFrom(error: unknown) {
  return error instanceof ApiStatusError ? error.problem.requestId : undefined;
}
