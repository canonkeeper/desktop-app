import { Square } from 'lucide-react';
import { useEffect, useState } from 'react';
import { serverNow } from '../../../shared/clock/server-clock';
import { Button } from '../../../ui/controls/Button';
import { BrowserNoticeToggle } from '../BrowserNoticeToggle';
import { feedStatus, type FeedTurn } from './feed-model';
import styles from './ChatFeed.module.css';

/** «Остановить» of the running turn: it stands in the status of the turn, with its word, at any width (CW2.4). */
export interface TurnStop {
  canStop: boolean;
  stopping: boolean;
  onStop: () => void;
}

/**
 * The words of the wait (specs/2026-11-10-ux4-chat-waiting.md CW2.2): constants of the page — the server does not
 * estimate how long a turn takes, so «обычно» is a word of the page, not a promise. From the third minute the line says
 * the question is a big one.
 */
/** How long a question takes, one term for the empty chat and the line of a running turn (specs/2026-11-11-ux9-shelf-chat.md SC3.3). */
export const usualMinutes = '1–2 минуты';
export const longMinutes = 'до 15 минут';
export const usualWait = `обычно ${usualMinutes}`;
export const longWaitAfterMs = 3 * 60_000;
const longWait = { chat: `вопрос большой: такое занимает ${longMinutes}`, helper: `работа большая: такое занимает ${longMinutes}` } as const;
const mayLeave = 'можно уйти — ответ сохранится';
/**
 * A turn that is neither in the queue nor at its first step after a minute waits for the models, not for the book: the
 * line says so instead of «обычно 1–2 минуты» (the incident of 11.11: five minutes of «Начинаю» over the usual words).
 */
export const slowStartAfterMs = 60_000;
const slowStart = 'помощник ещё не начал: сервис отвечает медленнее обычного';
/** A clock of the browser that runs behind or far ahead of the server shows no time (CW2.2). */
const longestTurnMs = 24 * 60 * 60_000;

/** The time since the turn began, or nothing when the clocks disagree. */
export function waitLine(elapsedMs: number | undefined, { queued, helper, starting = false }: { queued: boolean; helper: boolean; starting?: boolean }) {
  const valid = elapsedMs !== undefined && elapsedMs >= 0 && elapsedMs <= longestTurnMs;
  const time = valid ? formatElapsed(elapsedMs) : undefined;
  const kind = helper ? 'helper' : 'chat';
  // A question in the queue has no usual time: the server does not count the wait (CW2.5).
  const usual = queued ? undefined
    : valid && starting && elapsedMs >= slowStartAfterMs ? slowStart
      : valid && elapsedMs >= longWaitAfterMs ? longWait[kind] : usualWait;
  return [time, usual, mayLeave].filter(Boolean).join(' · ');
}

/**
 * The status of a running turn in two lines (CW2.2): what the helper does now with «Остановить», and the time with how
 * long such a turn usually takes, that the author may leave and «Сообщить, когда будет готово». Only the activity is
 * announced; the clock beside it changes every second.
 */
export function LiveStatus({ helper = false, notify = false, queuePosition, startedAt, stop, turn }: {
  helper?: boolean;
  notify?: boolean;
  queuePosition?: number | null | undefined;
  startedAt?: string | undefined;
  stop?: TurnStop | undefined;
  turn: FeedTurn;
}) {
  const elapsed = useElapsed(startedAt);
  const stopping = Boolean(stop?.stopping);
  const queued = turn.activity === 'starting' && typeof queuePosition === 'number';
  return (
    <div className={styles.status}>
      <p className={styles.statusLine}>
        <span aria-hidden="true" className={styles.pulse} />
        <strong aria-live="polite">{feedStatus(turn, queuePosition, stopping)}</strong>
        {stop ? (
          <Button className={styles.statusStop} disabled={!stop.canStop} loading={stopping} onClick={stop.onStop} size="sm" variant="outline">
            <Square aria-hidden="true" size={13} />Остановить
          </Button>
        ) : null}
      </p>
      {stopping ? null : (
        <p className={styles.statusLine}>
          <span className={styles.statusMeta}>{waitLine(elapsed, { queued, helper, starting: turn.activity === 'starting' })}</span>
          {notify ? <BrowserNoticeToggle /> : null}
        </p>
      )}
    </div>
  );
}

/**
 * The time since the turn began by the clock of the server (specs/2026-11-11-ux9-shelf-chat.md SC3.8, J-80): the start is the
 * server's, so is now — a clock of the device an hour ahead does not make the first second an hour.
 */
function useElapsed(startedAt: string | undefined) {
  const started = startedAt ? Date.parse(startedAt) : Number.NaN;
  const [now, setNow] = useState(() => serverNow());
  useEffect(() => {
    if (Number.isNaN(started)) return undefined;
    setNow(serverNow());
    const timer = window.setInterval(() => setNow(serverNow()), 1_000);
    return () => window.clearInterval(timer);
  }, [started]);
  return Number.isNaN(started) ? undefined : now - started;
}

export function formatElapsed(milliseconds: number) {
  const seconds = Math.max(0, Math.floor(milliseconds / 1_000));
  const hours = Math.floor(seconds / 3_600);
  const minutes = Math.floor((seconds % 3_600) / 60);
  const rest = String(seconds % 60).padStart(2, '0');
  return hours ? `${hours}:${String(minutes).padStart(2, '0')}:${rest}` : `${minutes}:${rest}`;
}
