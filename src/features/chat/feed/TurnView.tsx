import { memo, useId, useLayoutEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react';
import { BookA, BookOpen, Brain, CalendarClock, ChevronRight, History, ListOrdered, LoaderCircle, MousePointerClick, Palette, ScanText, Search, Sparkles, Spline, UserRound } from 'lucide-react';
import type { PendingSteer } from '../../../entities/chat/steer';
import { plainPlaces, type Places } from '../../../shared/text/place';
import { MarkdownContent, type MarkdownImage, type WorkshopLinkTarget } from '../../../ui/MarkdownContent';
import type { ChatFeedProps } from './chat-feed-props';
import { eventKey, lastLines, workSummary, type FeedAction, type FeedStep, type FeedTurn } from './feed-model';
import { LiveStatus, type TurnStop } from './LiveStatus';
import type { ActionKind } from './saga-actions';
import { AskSteerButton, SteerRow } from './SteerRow';
import { TurnEnd, type TurnEnding } from './TurnEnd';
import type { CanonicalAgentTimelineEvent } from './types';
import styles from './ChatFeed.module.css';

export type RenderSourceLink = ChatFeedProps['renderSourceLink'];
export type Renderers = Pick<ChatFeedProps, 'renderImage' | 'renderActionDrawing' | 'renderActionRemembered' | 'renderQuestionImages' | 'renderQuestionAttachment' | 'renderSteerImages' | 'renderWorkshopLink' | 'pressedLine'>;
type RenderTurnLink = (target: WorkshopLinkTarget, label: ReactNode, scene?: string) => ReactNode;

/** The lines of a running turn shown under its status; the rest opens by «… ещё N раньше» (CW2.3). */
const shownLines = 3;

/**
 * A long question of the author folds to a few lines until the author opens it whole (the owner's snapshot 03.11); the
 * question of a running turn, pinned over its work, to three (specs/2026-11-10-ux4-chat-waiting.md CW2.1).
 */
function QuestionText({ lines, places, text: written }: { lines: 3 | 6; places?: Places | undefined; text: string }) {
  const [open, setOpen] = useState(false);
  const [overflows, setOverflows] = useState(false);
  const paragraph = useRef<HTMLParagraphElement>(null);
  // A link a button of the product wrote into the question reads as the place it names (ux1-names N2.3).
  const text = plainPlaces(written, places);
  const long = lines === 6 ? text.length > 480 || text.split('\n').length > 6 : text.length > 200 || text.split('\n').length > 3;
  useLayoutEffect(() => {
    const node = paragraph.current;
    if (node && !open) setOverflows(node.scrollHeight > node.clientHeight + 1);
  }, [open, text]);
  const folds = long || overflows;
  return (
    <>
      <p className={folds && !open ? styles.folded : undefined} data-lines={lines} ref={paragraph}>{text}</p>
      {folds ? <button className={styles.questionToggle} onClick={() => setOpen(!open)} type="button">{open ? 'Свернуть' : 'Показать полностью'}</button> : null}
    </>
  );
}

/**
 * A `drawing:` link of a turn: the idea of a new drawing is the scene the link carries, else the question of the turn
 * (specs/2026-11-11-ux9-shelf-chat.md SC3.4, W-5A).
 */
function useTurnLink(renderers: Renderers, question: string): RenderTurnLink | undefined {
  const render = renderers.renderWorkshopLink;
  return useMemo(() => render ? (target: WorkshopLinkTarget, label: ReactNode, scene?: string) => render(target, label, scene || question) : undefined, [question, render]);
}

export interface TurnViewProps {
  belowAnswer: ChatFeedProps['belowAnswer'];
  /** The questions the helper offered at the end of the answer of the last turn (AA1.2). */
  continuations?: readonly string[] | undefined;
  /** A quiet line under the status of the running turn (AA2.6). */
  liveNote?: string | undefined;
  /** The feed of a helper of «Письмо» or «Рисование»: its fold is «Как я работал», its unit a message. */
  helper: boolean;
  /** «Сообщить, когда будет готово» beside the time of the running turn (CW2.6). */
  notify: boolean;
  onAskSteer: ChatFeedProps['onAskSteer'];
  onOpenSource: ChatFeedProps['onOpenSource'];
  onRepeatQuestion: ChatFeedProps['onRepeatQuestion'];
  /** How the Job of this turn ended without an answer: the last turn of the feed knows it (CW2.8). */
  ending?: TurnEnding | undefined;
  /** The block of the page for the end of this turn — the failure of its Job with its code — instead of the feed's own. */
  end?: ReactNode;
  pendingSteers: readonly PendingSteer[];
  queuePosition?: number | null | undefined;
  ref?: RefObject<HTMLElement | null> | undefined;
  renderAssistantActions: ChatFeedProps['renderAssistantActions'];
  renderers: Renderers;
  renderSourceLink: RenderSourceLink;
  startedAt?: string | undefined;
  stop?: TurnStop | undefined;
  turn: FeedTurn;
}

/**
 * One question with the work of the helper and its answer. A running turn pins the question and its status over the
 * work and shows its last lines (CW2.1–CW2.4); a finished one folds its work behind «Как я искал», and what changed the
 * product — «Запомнил: …», a correction the helper did not read — stands under the answer outside the fold (CW2.7).
 */
export function TurnView({
  belowAnswer, continuations, end, ending, helper, liveNote, notify, onAskSteer, onOpenSource, onRepeatQuestion, pendingSteers, queuePosition, ref,
  renderAssistantActions, renderers, renderSourceLink, startedAt, stop, turn,
}: TurnViewProps) {
  const unfinished = Boolean(turn.unfinished);
  const turnLink = useTurnLink(renderers, turn.question?.text ?? '');
  // What the turn remembered acts in every chat of the saga from now on: it is seen without opening the work (CH4.1).
  const kept = turn.steps.flatMap((step) => step.actions).filter((action) => action.description.remembered && action.status === 'done');
  // A press of a button of the page is a line of what happened, not the author's bubble (specs/2026-11-11-ux9-writing-b.md WB3.3).
  const pressed = turn.question?.button && renderers.pressedLine ? renderers.pressedLine(turn.question.text ?? '') : null;
  const question = pressed ? (
    <p aria-label="Нажатие кнопки" className={styles.pressed} role="note">
      <MousePointerClick aria-hidden="true" className={styles.actionIcon} size={15} />
      <span>{pressed}</span>
    </p>
  ) : turn.question ? (
    <div aria-label="Вопрос автора" className={styles.question} role="note">
      <QuestionText lines={turn.live ? 3 : 6} places={turn.question.places} text={turn.question.text ?? ''} />
      {turn.question.images?.length ? renderers.renderQuestionImages?.(turn.question.images) : null}
      {turn.question.attachment ? renderers.renderQuestionAttachment?.(turn.question.attachment) : null}
    </div>
  ) : null;
  const summary = workSummary(turn);
  return (
    <article aria-label="Вопрос и ответ" className={styles.turn} data-live={turn.live ? '' : undefined} ref={ref}>
      {turn.live ? (<>
        <section aria-label="Готовим ответ" className={styles.liveHead}>
          {question}
          <LiveStatus helper={helper} notify={notify} queuePosition={queuePosition} startedAt={startedAt} stop={stop} turn={turn} />
          {liveNote ? <p className={styles.liveNote} role="status">{liveNote}</p> : null}
        </section>
        <LiveSteps onOpenSource={onOpenSource} renderers={renderers} renderSourceLink={renderSourceLink} turn={turn} />
      </>) : (<>
        {question}
        {turn.steps.length > 0 ? (
          <details className={styles.work} data-activity-kind="work" open={unfinished && !turn.answer ? true : undefined}>
            <summary>
              <ChevronRight aria-hidden="true" className={styles.chevron} size={16} />
              <span className={styles.workTitle}>{helper ? 'Как я работал' : 'Как я искал'}</span>
              {summary.length ? <span>{` · ${summary.join(' · ')}`}</span> : null}
            </summary>
            <Steps onOpenSource={onOpenSource} renderers={renderers} renderSourceLink={renderSourceLink} steps={turn.steps} turn={turn} />
          </details>
        ) : null}
      </>)}
      {/* One place for the remark a live turn is on, the answer it is writing and the final
          answer, so streaming text keeps its element when the turn finishes. */}
      {turn.answer ?? turn.tail ? (
        <Remark
          answer={Boolean(turn.answer)}
          continuations={continuations}
          current={Boolean(turn.tail)}
          event={(turn.answer ?? turn.tail)!}
          key={eventKey((turn.answer ?? turn.tail)!)}
          // «В заметки» belongs to an answer: a turn that got none keeps its text without it.
          renderAssistantActions={turn.live || unfinished ? undefined : renderAssistantActions}
          renderImage={renderers.renderImage}
          renderSourceLink={renderSourceLink}
          renderWorkshopLink={turnLink}
        />
      ) : null}
      {kept.length ? (
        <ul aria-label="Помощник запомнил" className={styles.remembered}>
          {kept.map((action) => (
            <li key={action.key}>
              <Brain aria-hidden="true" className={styles.actionIcon} size={15} />
              <span className={styles.actionText}>{action.description.label}</span>
              {renderers.renderActionRemembered?.(action, turn)}
            </li>
          ))}
        </ul>
      ) : null}
      {turn.turnIds.length ? belowAnswer?.(turn.turnIds) : null}
      {pendingSteers.map((steer) => (
        <SteerRow
          images={steer.images.length ? renderers.renderSteerImages?.(steer.images, steer.firstPhoto ?? 1) : undefined}
          key={steer.steerId}
          note="Будет учтено на следующем шаге"
          text={steer.text}
        />
      ))}
      {turn.lateSteers.map((steer) => (
        <SteerRow
          action={onAskSteer ? <AskSteerButton onAsk={() => onAskSteer(steer.text ?? '')} /> : undefined}
          images={steer.images?.length ? renderers.renderSteerImages?.(steer.images, steer.firstPhoto ?? 1) : undefined}
          key={eventKey(steer)}
          // A correction the end of the turn outran is counted by the rule of the quota (a taken `turn/steer` is not given
          // back), and asking it anew is one more message of the day (specs/2026-11-11-ux9-shelf-chat.md SC3.1, Ч-26).
          note={helper
            ? 'Не успел прочесть · уточнение засчитано. Отправить его снова — ещё одно сообщение из дневных.'
            : 'Не успел прочесть · уточнение засчитано. Задать его новым вопросом — ещё один вопрос из дневных.'}
          text={steer.text ?? ''}
        />
      ))}
      {unfinished ? end ?? <TurnEnd ending={ending} helper={helper} turn={turn}
        onRepeat={onRepeatQuestion && turn.question?.text ? () => onRepeatQuestion(turn.question?.text ?? '') : undefined} /> : null}
    </article>
  );
}

/**
 * The work of a running turn (CW2.3): its last three lines under «… ещё N раньше», which opens the whole of it. The
 * feed keeps its place while the author reads above (the viewport follows the bottom only from the bottom).
 */
function LiveSteps({ onOpenSource, renderers, renderSourceLink, turn }: {
  onOpenSource: ChatFeedProps['onOpenSource'];
  renderers: Renderers;
  renderSourceLink: RenderSourceLink;
  turn: FeedTurn;
}) {
  const [whole, setWhole] = useState(false);
  const stepsId = useId();
  const tail = lastLines(turn.steps, shownLines);
  const steps = whole ? turn.steps : tail.steps;
  return (
    <div className={styles.work} data-activity-kind="work" data-live="">
      {tail.hidden > 0 ? (
        <button aria-controls={stepsId} aria-expanded={whole} className={styles.earlier} onClick={() => setWhole(!whole)} type="button">
          {whole ? 'Свернуть начало' : `… ещё ${tail.hidden} раньше`}
        </button>
      ) : null}
      <div id={stepsId}>
        <Steps current={!turn.tail && !turn.answer} live onOpenSource={onOpenSource} renderers={renderers} renderSourceLink={renderSourceLink} steps={steps} turn={turn} />
      </div>
    </div>
  );
}

function Steps({
  current = false,
  live = false,
  onOpenSource,
  renderers,
  renderSourceLink,
  steps,
  turn,
}: {
  /** Mark the last step as the one the live turn is on. */
  current?: boolean;
  live?: boolean;
  onOpenSource: ChatFeedProps['onOpenSource'];
  renderers: Renderers;
  renderSourceLink: RenderSourceLink;
  steps: readonly FeedStep[];
  turn: FeedTurn;
}) {
  const turnLink = useTurnLink(renderers, turn.question?.text ?? '');
  if (steps.length === 0) return null;
  return (
    <ol className={styles.steps}>
      {steps.map((step, index) => (
        <li className={styles.step} data-current={current && index === steps.length - 1 ? '' : undefined} data-steer={step.steer ? '' : undefined} key={step.key}>
          {step.steer ? <SteerRow images={step.steer.images?.length ? renderers.renderSteerImages?.(step.steer.images, step.steer.firstPhoto ?? 1) : undefined} text={plainPlaces(step.steer.text ?? '', step.steer.places)} /> : null}
          {step.event ? <Remark event={step.event} renderImage={renderers.renderImage} renderSourceLink={renderSourceLink} renderWorkshopLink={turnLink} /> : null}
          {step.actions.length > 0 ? (
            <ul aria-label="Что сделал помощник" className={styles.actions}>
              {step.actions.map((action) => (
                <ActionRow action={action} key={action.key} onOpenSource={onOpenSource}
                  drawing={action.description.kind === 'draw' ? renderers.renderActionDrawing?.(action, live, turn) : undefined} />
              ))}
            </ul>
          ) : null}
        </li>
      ))}
    </ol>
  );
}

const actionIcons: Record<ActionKind, typeof Search> = {
  draw: Palette,
  search: Search,
  read: BookOpen,
  chapters: ListOrdered,
  glossary: BookA,
  entity: UserRound,
  timeline: CalendarClock,
  threads: Spline,
  changes: History,
  scan: ScanText,
  remember: Brain,
  memory: Brain,
  other: Sparkles,
};

const statusLabels = { running: 'выполняется', error: 'не получилось' } as const;

/**
 * One action of a step in the voice of the helper: what it does now while it runs, what it did once done
 * (specs/2026-11-10-ux4-chat-waiting.md CW1.1). An action that did not work is an ordinary line: the helper went on, and
 * only a turn that ended without an answer is an alarm (CW1.4, ux1-names N2.4).
 */
function ActionRow({ action, drawing, onOpenSource }: { action: FeedAction; drawing?: ReactNode; onOpenSource: ChatFeedProps['onOpenSource'] }) {
  const { description, status } = action;
  const Icon = status === 'running' ? LoaderCircle : actionIcons[description.kind];
  const source = description.source;
  // A line that did not work says what the helper tried, not what it did.
  const line = status === 'done' ? description.label : description.activity;
  return (
    <li className={styles.action} data-kind={description.kind} data-status={status}>
      <Icon aria-hidden="true" className={styles.actionIcon} size={15} />
      <span className={styles.actionText}>
        {source && onOpenSource ? (
          <button className={styles.actionSource} onClick={(event) => onOpenSource(source, event.currentTarget)} title="Открыть рядом с чатом" type="button">
            {line}
          </button>
        ) : <span className={styles.actionLabel}>{line}</span>}
        {description.detail ? <span className={styles.actionDetail}> — {description.detail}</span> : null}
        {status === 'done' ? null : <span className={styles.visuallyHidden}> ({statusLabels[status]})</span>}
      </span>
      {drawing ? <div className={styles.actionDrawing}>{drawing}</div> : null}
    </li>
  );
}

// Deltas change only the streaming remark; the others keep their event object and skip
// re-parsing their markdown.
const Remark = memo(function Remark({
  answer = false,
  continuations,
  current = false,
  event,
  renderAssistantActions,
  renderImage,
  renderSourceLink,
  renderWorkshopLink,
}: {
  answer?: boolean;
  continuations?: readonly string[] | undefined;
  /** The remark a live turn is on, drawn as the current step under the others. */
  current?: boolean;
  event: CanonicalAgentTimelineEvent;
  renderAssistantActions?: ChatFeedProps['renderAssistantActions'];
  renderImage?: ChatFeedProps['renderImage'];
  renderSourceLink: RenderSourceLink;
  renderWorkshopLink?: RenderTurnLink | undefined;
}) {
  const contentRef = useRef<HTMLDivElement>(null);
  return (
    <section
      className={answer ? styles.answer : current ? `${styles.remark} ${styles.currentRemark}` : styles.remark}
      data-activity-kind="assistant"
      data-answer={answer ? '' : undefined}
      data-provider-item={event.providerItemId}
      data-provider-turn={event.providerTurnId}
      tabIndex={-1}
    >
      <div data-assistant-content="" ref={contentRef}>
        {event.text ? <Markdown places={event.places} renderImage={renderImage} renderSourceLink={renderSourceLink ? (source, label) => renderSourceLink(event, source, label) : undefined} renderWorkshopLink={renderWorkshopLink}>{event.text}</Markdown> : null}
      </div>
      {renderAssistantActions?.(event, contentRef, { continuations: continuations ?? [] })}
    </section>
  );
});

function Markdown({ children, places, renderImage, renderSourceLink, renderWorkshopLink }: {
  children: string;
  places?: Places | undefined;
  renderImage?: (image: MarkdownImage) => ReactNode;
  renderSourceLink?: React.ComponentProps<typeof MarkdownContent>['renderSourceLink'];
  renderWorkshopLink?: RenderTurnLink | undefined;
}) {
  return <MarkdownContent className={styles.markdown} renderImage={renderImage} renderSourceLink={renderSourceLink} {...(places ? { places } : {})} {...(renderWorkshopLink ? { renderWorkshopLink } : {})}>{children}</MarkdownContent>;
}
