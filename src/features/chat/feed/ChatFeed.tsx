import { useLayoutEffect, useMemo, useRef } from 'react';
import type { ChatFeedProps } from './chat-feed-props';
import { buildFeedTurns, type FeedTurn } from './feed-model';
import { TurnView } from './TurnView';
import styles from './ChatFeed.module.css';

export type { ChatFeedProps } from './chat-feed-props';

/**
 * The chat as the author reads it — and the feeds of the helpers of «Письмо» and «Рисование»: each question, the work
 * of the helper in its own voice (its remarks and what it did after them) and the answer, in the order of the server.
 * A finished turn folds its work behind «Как я искал»; a running one pins its question and status over its last lines
 * (specs/2026-11-10-ux4-chat-waiting.md).
 */
export function ChatFeed({
  belowAnswer,
  className,
  emptyMessage,
  error,
  errorActions,
  errorLabel = 'Связь прервалась',
  events = [],
  footer,
  header,
  jobStatus,
  liveNote,
  notice,
  onAskSteer,
  onOpenSource,
  pendingQuestion,
  pendingPressed = false,
  pendingSteers = [],
  pressedLine,
  queuePosition,
  renderActionDrawing,
  renderActionRemembered,
  renderAssistantActions,
  renderImage,
  renderQuestionImages,
  renderQuestionAttachment,
  renderSourceLink,
  renderSteerImages,
  renderWorkshopLink,
  startedAt,
  stop,
  notify = false,
  helper = false,
  onRepeatQuestion,
  turnEnd,
  viewportClassName,
}: ChatFeedProps) {
  const active = jobStatus === 'queued' || jobStatus === 'running' || jobStatus === 'canceling';
  const turns = useMemo(() => {
    // The continuations of an answer are the chat's: a helper of «Письмо» or «Рисование» keeps its text whole (AA1.2).
    const built = buildFeedTurns(events, active, jobStatus === 'failed' || jobStatus === 'canceled' ? jobStatus : undefined, { continuations: !helper });
    // A running Job whose turn has not reached the timeline yet still shows that work started.
    const waiting = active && !built.at(-1)?.live && (built.length === 0 || Boolean(pendingQuestion));
    return waiting ? [...built, pendingTurn(pendingQuestion, pendingPressed)] : built;
  }, [active, events, helper, jobStatus, pendingQuestion, pendingPressed]);
  const viewportRef = useRef<HTMLDivElement>(null);
  const lastTurnRef = useRef<HTMLElement>(null);
  const followBottom = useRef(true);
  const positioned = useRef<{ key: string; live: boolean } | null>(null);
  const lastTurn = turns.at(-1);

  useLayoutEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport || !lastTurn) return;
    const previous = positioned.current;
    positioned.current = { key: lastTurn.key, live: lastTurn.live };
    if (lastTurn.live) {
      if (previous?.key !== lastTurn.key || followBottom.current) viewport.scrollTop = viewport.scrollHeight;
      return;
    }
    // A chat opens on the question of its last turn; a turn that just finished while the author
    // followed it keeps its answer in view instead of jumping to the end of a long answer.
    const opened = previous?.key !== lastTurn.key;
    const finished = previous?.key === lastTurn.key && previous.live;
    if (!opened && !(finished && followBottom.current)) return;
    const target = finished ? lastTurnRef.current?.querySelector<HTMLElement>('[data-answer]') ?? lastTurnRef.current : lastTurnRef.current;
    if (target) viewport.scrollTop = Math.max(0, target.offsetTop - viewport.offsetTop - 16);
  }, [lastTurn]);

  useLayoutEffect(() => {
    const viewport = viewportRef.current;
    if (viewport && lastTurn?.live && followBottom.current) viewport.scrollTop = viewport.scrollHeight;
  }, [error, events, lastTurn?.live, notice, pendingSteers]);

  return (
    <div className={joinClassNames(styles.feed, className)}>
      <div
        className={joinClassNames(styles.viewport, viewportClassName)}
        data-state={lastTurn?.live ? 'streaming' : 'terminal'}
        onScroll={(event) => {
          const viewport = event.currentTarget;
          followBottom.current = viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight <= 24;
        }}
        ref={viewportRef}
      >
        <div className={styles.column}>
          {header}
          {turns.length === 0 && emptyMessage ? <p className={styles.empty}>{emptyMessage}</p> : null}
          {turns.map((turn, index) => (
            <TurnView
              belowAnswer={belowAnswer}
              // The last turn knows how its Job ended; the page may tell it with its own block (CW2.8).
              end={index === turns.length - 1 ? turnEnd : undefined}
              ending={index === turns.length - 1 ? (jobStatus === 'canceled' ? 'stopped' : jobStatus === 'failed' ? 'failed' : undefined) : undefined}
              helper={helper}
              liveNote={index === turns.length - 1 ? liveNote : undefined}
              // What to ask next stands under the last answer only: an earlier one was followed already (AA1.2).
              continuations={index === turns.length - 1 ? turn.continuations : undefined}
              key={turn.key}
              notify={notify}
              onAskSteer={onAskSteer}
              onOpenSource={onOpenSource}
              onRepeatQuestion={onRepeatQuestion}
              pendingSteers={index === turns.length - 1 && turn.live ? pendingSteers : []}
              queuePosition={queuePosition}
              ref={index === turns.length - 1 ? lastTurnRef : undefined}
              renderAssistantActions={renderAssistantActions}
              renderers={{ renderImage, renderActionDrawing, renderActionRemembered, renderQuestionImages, renderQuestionAttachment, renderSteerImages, renderWorkshopLink, pressedLine }}
              renderSourceLink={renderSourceLink}
              startedAt={startedAt}
              stop={stop}
              turn={turn}
            />
          ))}
          {/* A Job that failed before its turn reached the timeline still says so, under the last question. */}
          {turnEnd && !turns.at(-1)?.unfinished ? turnEnd : null}
          {footer}
          {notice ? <p className={styles.notice} role="status">{notice}</p> : null}
          {error ? (
            <section aria-label={errorLabel} className={styles.error} role="alert">
              {error}
              {errorActions ? <div className={styles.errorActions}>{errorActions}</div> : null}
            </section>
          ) : null}
        </div>
      </div>
    </div>
  );
}

function pendingTurn(question: string | undefined, pressed = false): FeedTurn {
  return {
    key: 'pending',
    steps: [],
    lateSteers: [],
    turnIds: [],
    live: true,
    activity: 'starting',
    ...(question ? {
      question: {
        cursor: 'pending', id: 'pending', jobId: '', kind: 'user', logicalSection: 0, role: 'main', text: question, ...(pressed ? { button: true } : {}),
      },
    } : {}),
  };
}

function joinClassNames(...names: Array<string | undefined>) {
  return names.filter(Boolean).join(' ');
}
