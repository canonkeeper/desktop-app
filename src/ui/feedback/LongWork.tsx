import type { ReactNode } from 'react';
import { Progress } from './Progress';
import styles from './LongWork.module.css';

/** The step of a work, from the server: «Сцена 2 из 4», «3 из 12». */
export interface LongWorkStep {
  done: number;
  of: number;
  /** The word of a step, when there is one: «Сцена», «Глава». */
  word?: string;
}

/**
 * The second line of a long work from the data of the server (specs/2026-11-10-ux2-patterns-states.md PS3.3): «Сцена 2 из
 * 4 · обычно 3–5 минут · можно уйти — написанное останется в главе». No step and no term the server did not give.
 */
export function longWorkLine({ leave, step, term }: { leave?: string | undefined; step?: LongWorkStep | undefined; term?: string | undefined }) {
  return [
    step ? `${step.word ? `${step.word} ` : ''}${step.done} из ${step.of}` : null,
    term?.trim() || null,
    leave?.trim() ? `можно уйти — ${leave.trim()}` : null,
  ].filter(Boolean).join(' · ');
}

export interface LongWorkProps {
  /** The name of the block for a screen reader. */
  label: string;
  /** What goes on, in one line: the work, how far, how long, what may be done meanwhile. */
  title: ReactNode;
  /** The second line: that the page may be closed, what is ready, why it stopped (`longWorkLine`). */
  detail?: ReactNode;
  /** The one action of the work: the main button of the line, «Остановить» of a work that may stop. */
  action?: ReactNode;
  /** The part done, 0..1, by the numbers of the server; none — no bar. */
  progress?: number | null;
  /** `work` while it goes on, `done` at its end, `stalled` when it stopped. */
  tone?: 'work' | 'done' | 'stalled';
  /** What a screen reader hears once per state of the work, not at every count. */
  announcement?: string;
  /** The place of the line in its page (the map lays it over the canvas). */
  className?: string;
  /** What stands under the line: a reason of a refusal, the code for support. */
  children?: ReactNode;
}

/**
 * «Долгая работа» (specs/2026-11-10-ux2-shell.md S6.8, specs/2026-11-10-ux2-patterns-states.md PS3.3, master
 * `Pattern/Common/Long Work` of board 15·08): one line on the place of a work — what goes on · how far · how long · that one
 * may leave — with its one action and its bar. Its words and numbers are the server's; one work is one block.
 */
export function LongWork({ action, announcement, children, className, detail, label, progress, title, tone = 'work' }: LongWorkProps) {
  return (
    <section aria-label={label} className={`${styles.longWork}${className ? ` ${className}` : ''}`} data-long-work="" data-tone={tone}>
      <div className={styles.line}>
        <div className={styles.now}>
          <p className={styles.what}>{title}</p>
          {detail ? <p className={styles.howLong}>{detail}</p> : null}
        </div>
        {action ? <div className={styles.action}>{action}</div> : null}
      </div>
      {progress !== null && progress !== undefined ? (
        <Progress aria-label={label} className={styles.progress} value={Math.min(1, Math.max(0, progress))} />
      ) : null}
      {children}
      <p aria-atomic="true" aria-live="polite" className={styles.announcement}>{announcement}</p>
    </section>
  );
}
