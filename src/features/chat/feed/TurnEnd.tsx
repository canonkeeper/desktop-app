import { RotateCcw, Square } from 'lucide-react';
import { Button } from '../../../ui/controls/Button';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { SupportReference } from '../../../ui/feedback/SupportReference';
import type { FeedTurn } from './feed-model';
import styles from './ChatFeed.module.css';

/** How the last turn of the feed ended, by its Job: the author stopped it or it failed. */
export type TurnEnding = 'stopped' | 'failed';

/**
 * A turn that ended without an answer (specs/2026-11-10-ux4-chat-waiting.md CW2.8): one block under its work that says
 * what happened in the word «помощник» and the count as it is by the rules of the quota
 * (specs/2026-11-09-quota-accounting.md Q2.4) — a stopped question is counted, a failed one is given back; «Повторить
 * вопрос» puts the question into the field. A failure is the common block of an error, with «Написать нам»; a stop is
 * the author's own act and no alarm. An earlier turn of the chat says it in a quiet line.
 */
export function TurnEnd({ ending, helper, onRepeat, turn }: {
  ending?: TurnEnding | undefined;
  helper: boolean;
  onRepeat?: (() => void) | undefined;
  turn: FeedTurn;
}) {
  const stopped = ending === 'stopped' || (!ending && turn.unfinished === 'interrupted');
  if (!ending) {
    return <p className={styles.outcome} role="status">{stopped ? 'Ответ остановлен.' : 'Помощник не смог ответить на этот вопрос.'}</p>;
  }
  const repeat = onRepeat ? <Button onClick={onRepeat} size="sm" variant="outline"><RotateCcw aria-hidden="true" size={14} />Повторить вопрос</Button> : null;
  if (stopped) {
    return (
      <div aria-label="Ответ остановлен" className={styles.stopped} role="status">
        <Square aria-hidden="true" className={styles.stoppedIcon} size={15} />
        <div className={styles.stoppedBody}>
          <strong>Ответ остановлен</strong>
          <p>{helper ? 'Вы остановили ответ. Остановленное сообщение засчитано.' : 'Вы остановили ответ. Остановленный вопрос засчитан.'}</p>
          {repeat}
        </div>
      </div>
    );
  }
  return (
    <ErrorBlock action={onRepeat ? { label: 'Повторить вопрос', onClick: onRepeat } : undefined} cause="Ответ прервался."
      kept={helper ? 'Это сообщение не засчитано.' : 'Этот вопрос не засчитан.'} title="Помощник не смог ответить">
      <SupportReference />
    </ErrorBlock>
  );
}
