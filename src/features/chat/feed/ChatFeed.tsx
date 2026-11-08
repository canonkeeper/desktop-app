import { memo, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react';
import { BookA, BookOpen, Brain, ChevronDown, ChevronRight, ChevronUp, CircleAlert, History, ListOrdered, LoaderCircle, Palette, ScanText, Search, Sparkles, Spline } from 'lucide-react';
import type { PendingSteer } from '../../../entities/chat/steer';
import type { SourceCitation } from '../../../shared/navigation/chat-source';
import { Button } from '../../../ui/controls/Button';
import { MarkdownContent, type MarkdownImage, type WorkshopLinkTarget } from '../../../ui/MarkdownContent';
import { BrowserNoticeToggle } from '../BrowserNoticeToggle';
import { actionCount, agentStepCount, buildFeedTurns, eventKey, feedStatus, steerCount, type FeedAction, type FeedStep, type FeedTurn } from './feed-model';
import { plural, type ActionKind } from './saga-actions';
import { AskSteerButton, SteerRow } from './SteerRow';
import type { AgentJobStatus, CanonicalAgentTimelineEvent } from './types';
import styles from './ChatFeed.module.css';

export interface ChatFeedProps {
  className?: string;
  emptyMessage?: string;
  /** The failure block under the last turn: what happened, what to do and the support code. */
  error?: ReactNode;
  errorActions?: ReactNode;
  errorLabel?: string;
  events?: readonly CanonicalAgentTimelineEvent[];
  /** What stands under the answer of a turn, by the native turns of its events: the notes a workshop's helper wrote
   *  in it (specs/2026-11-02-writer-workshop.md W4.5). */
  belowAnswer?: (turnIds: readonly string[]) => ReactNode;
  /** What follows the last turn, such as the style choice of the first drawing (illustrations R8.6). */
  footer?: ReactNode;
  /** What stands before the first turn in the same scroll, such as the notes of a workshop's helper (W4.5). */
  header?: ReactNode;
  jobStatus?: AgentJobStatus | null;
  notice?: string;
  onOpenSource?: (source: SourceCitation, trigger: HTMLElement) => void;
  /** A question just sent whose turn has not reached the timeline yet. */
  pendingQuestion?: string;
  /** Corrections the server took that the timeline does not show yet (specs/2026-10-30-chat-steering.md R3.5). */
  pendingSteers?: readonly PendingSteer[];
  /** Puts a correction the end of the turn left unread into the question field (R3.6). */
  onAskSteer?: (text: string) => void;
  /** The place of the waiting question in the fair queue (getJob of a queued turn). */
  queuePosition?: number | null;
  renderAssistantActions?: (event: CanonicalAgentTimelineEvent, contentRef: RefObject<HTMLElement | null>) => ReactNode;
  renderSourceLink?: (event: CanonicalAgentTimelineEvent, source: SourceCitation, label: ReactNode) => ReactNode;
  /** Draws `![подпись](image:K)` of a remark or an answer: a drawing of the saga (R8.1). */
  renderImage?: (image: MarkdownImage) => ReactNode;
  /** Draws `drawing:new` and `drawing:K` of a remark or an answer with the question of its turn (image-workshop IW7.3). */
  renderWorkshopLink?: (target: WorkshopLinkTarget, label: ReactNode, question: string) => ReactNode;
  /** Draws the images attached to a question under it (illustrations R15.9). */
  renderQuestionImages?: (numbers: readonly number[]) => ReactNode;
  /** Draws the card of the chapter attached to a question under it (specs/2026-11-07-chat-attach-chapter.md CA4.2). */
  renderQuestionAttachment?: (attachment: NonNullable<CanonicalAgentTimelineEvent['attachment']>) => ReactNode;
  /** Draws the images of a correction with their «фото N» from `first` (R3.1). */
  renderSteerImages?: (numbers: readonly number[], first: number) => ReactNode;
  /** What a drawing action of a step shows under its line: the drawing as it develops (R8.1). */
  renderActionDrawing?: (action: FeedAction, live: boolean, turn: FeedTurn) => ReactNode;
  /** What stands under «Запомнил: …»: its undo (specs/2026-11-02-writer-workshop.md, Decision log 03.11). */
  renderActionRemembered?: (action: FeedAction, turn: FeedTurn) => ReactNode;
  /** When the Job of the live turn was created, for the time it runs. */
  startedAt?: string;
  viewportClassName?: string;
}

type RenderSourceLink = ChatFeedProps['renderSourceLink'];
type Renderers = Pick<ChatFeedProps, 'renderImage' | 'renderActionDrawing' | 'renderActionRemembered' | 'renderQuestionImages' | 'renderQuestionAttachment' | 'renderSteerImages' | 'renderWorkshopLink'>;
type RenderTurnLink = (target: WorkshopLinkTarget, label: ReactNode) => ReactNode;

/** The `drawing:` links of a turn lead with its question; one function per turn keeps the remarks memoized. */
/** A long question of the author folds to a few lines; the author opens it whole (the owner's snapshot 03.11). */
function QuestionText({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const long = text.length > 480 || text.split('\n').length > 6;
  return (
    <>
      <p className={long && !open ? styles.folded : undefined}>{text}</p>
      {long ? <button className={styles.questionToggle} onClick={() => setOpen(!open)} type="button">{open ? 'Свернуть' : 'Показать полностью'}</button> : null}
    </>
  );
}

function useTurnLink(renderers: Renderers, question: string): RenderTurnLink | undefined {
  const render = renderers.renderWorkshopLink;
  return useMemo(() => render ? (target: WorkshopLinkTarget, label: ReactNode) => render(target, label, question) : undefined, [question, render]);
}

/**
 * The chat as the author reads it: each question, the steps the agent took (its remarks and
 * the actions after them) and the answer. A finished turn folds its steps behind one line; a
 * live turn shows them as they arrive under a line that says what the agent is doing now.
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
  notice,
  onAskSteer,
  onOpenSource,
  pendingQuestion,
  pendingSteers = [],
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
  viewportClassName,
}: ChatFeedProps) {
  const active = jobStatus === 'queued' || jobStatus === 'running' || jobStatus === 'canceling';
  const turns = useMemo(() => {
    const built = buildFeedTurns(events, active, jobStatus === 'failed' || jobStatus === 'canceled' ? jobStatus : undefined);
    // A running Job whose turn has not reached the timeline yet still shows that work started.
    const waiting = active && !built.at(-1)?.live && (built.length === 0 || Boolean(pendingQuestion));
    return waiting ? [...built, pendingTurn(pendingQuestion)] : built;
  }, [active, events, jobStatus, pendingQuestion]);
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
              key={turn.key}
              onAskSteer={onAskSteer}
              onOpenSource={onOpenSource}
              pendingSteers={index === turns.length - 1 && turn.live ? pendingSteers : []}
              queuePosition={queuePosition}
              ref={index === turns.length - 1 ? lastTurnRef : undefined}
              renderAssistantActions={renderAssistantActions}
              renderers={{ renderImage, renderActionDrawing, renderActionRemembered, renderQuestionImages, renderQuestionAttachment, renderSteerImages, renderWorkshopLink }}
              renderSourceLink={renderSourceLink}
              startedAt={startedAt}
              turn={turn}
            />
          ))}
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

function TurnView({
  belowAnswer,
  onAskSteer,
  onOpenSource,
  pendingSteers,
  queuePosition,
  ref,
  renderAssistantActions,
  renderers,
  renderSourceLink,
  startedAt,
  turn,
}: {
  belowAnswer: ChatFeedProps['belowAnswer'];
  onAskSteer: ChatFeedProps['onAskSteer'];
  onOpenSource: ChatFeedProps['onOpenSource'];
  pendingSteers: readonly PendingSteer[];
  queuePosition?: number | null;
  ref?: RefObject<HTMLElement | null>;
  renderAssistantActions: ChatFeedProps['renderAssistantActions'];
  renderers: Renderers;
  renderSourceLink: RenderSourceLink;
  startedAt?: string;
  turn: FeedTurn;
}) {
  const failed = Boolean(turn.unfinished);
  const turnLink = useTurnLink(renderers, turn.question?.text ?? '');
  // The author may fold the steps of a live turn; the remark it is writing stays in view.
  const [workOpen, setWorkOpen] = useState(true);
  const stepsId = useId();
  return (
    <article aria-label="Вопрос и ответ" className={styles.turn} data-live={turn.live ? '' : undefined} ref={ref}>
      {turn.question ? (
        <div aria-label="Вопрос автора" className={styles.question} role="note">
          <QuestionText text={turn.question.text ?? ''} />
          {turn.question.images?.length ? renderers.renderQuestionImages?.(turn.question.images) : null}
          {turn.question.attachment ? renderers.renderQuestionAttachment?.(turn.question.attachment) : null}
        </div>
      ) : null}
      {turn.live ? (
        <section aria-label="Готовим ответ" className={styles.work} data-activity-kind="work" data-live="">
          <LiveStatus
            fold={turn.steps.length > 0 ? { open: workOpen, stepsId, toggle: () => setWorkOpen((open) => !open) } : undefined}
            queuePosition={queuePosition}
            startedAt={startedAt}
            turn={turn}
          />
          <div hidden={!workOpen} id={stepsId}>
            <Steps current={!turn.tail && !turn.answer} live onOpenSource={onOpenSource} renderers={renderers} renderSourceLink={renderSourceLink} turn={turn} />
          </div>
        </section>
      ) : turn.steps.length > 0 ? (
        <details className={styles.work} data-activity-kind="work" open={failed && !turn.answer ? true : undefined}>
          <summary>
            <ChevronRight aria-hidden="true" className={styles.chevron} size={16} />
            <span className={styles.workTitle}>Ход работы</span>{' '}
            <span>· {workCounts(turn)}</span>
          </summary>
          <Steps onOpenSource={onOpenSource} renderers={renderers} renderSourceLink={renderSourceLink} turn={turn} />
        </details>
      ) : null}
      {/* One place for the remark a live turn is on, the answer it is writing and the final
          answer, so streaming text keeps its element when the turn finishes. */}
      {turn.answer ?? turn.tail ? (
        <Remark
          answer={Boolean(turn.answer)}
          current={Boolean(turn.tail)}
          event={(turn.answer ?? turn.tail)!}
          key={eventKey((turn.answer ?? turn.tail)!)}
          // «В заметки» belongs to an answer: a turn that got none keeps its text without it.
          renderAssistantActions={turn.live || failed ? undefined : renderAssistantActions}
          renderImage={renderers.renderImage}
          renderSourceLink={renderSourceLink}
          renderWorkshopLink={turnLink}
        />
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
          note="Ответ закончился раньше, чем это уточнение было прочитано."
          text={steer.text ?? ''}
        />
      ))}
      {failed ? (
        <p className={styles.outcome} role="status">
          <CircleAlert aria-hidden="true" size={16} />
          {turn.unfinished === 'interrupted' ? 'Ответ остановлен.' : 'Ответ не получен.'}
        </p>
      ) : null}
    </article>
  );
}

interface StepsFold {
  open: boolean;
  stepsId: string;
  toggle: () => void;
}

function LiveStatus({ fold, queuePosition, startedAt, turn }: {
  fold?: StepsFold;
  queuePosition?: number | null;
  startedAt?: string;
  turn: FeedTurn;
}) {
  const elapsed = useElapsed(startedAt);
  const actions = actionCount(turn);
  const meta = [elapsed, actions ? `${actions} ${plural(actions, ['действие', 'действия', 'действий'])}` : undefined].filter(Boolean).join(' · ');
  return (
    <p className={styles.status}>
      <span aria-hidden="true" className={styles.pulse} />
      {/* Only the activity is announced; the clock beside it changes every second. */}
      <strong aria-live="polite">{feedStatus(turn, queuePosition)}</strong>
      {meta ? <span className={styles.statusMeta}>{meta}</span> : null}
      <span className={styles.statusActions}>
        {fold ? (
          <Button aria-controls={fold.stepsId} aria-expanded={fold.open} aria-label={fold.open ? 'Свернуть ход работы' : 'Развернуть ход работы'}
            onClick={fold.toggle} size="sm" variant="ghost">
            {fold.open ? <ChevronUp aria-hidden="true" size={16} /> : <ChevronDown aria-hidden="true" size={16} />}
            {fold.open ? 'Свернуть' : 'Развернуть'}
          </Button>
        ) : null}
        <BrowserNoticeToggle />
      </span>
    </p>
  );
}

function Steps({
  current = false,
  live = false,
  onOpenSource,
  renderers,
  renderSourceLink,
  turn,
}: {
  /** Mark the last step as the one the live turn is on. */
  current?: boolean;
  live?: boolean;
  onOpenSource: ChatFeedProps['onOpenSource'];
  renderers: Renderers;
  renderSourceLink: RenderSourceLink;
  turn: FeedTurn;
}) {
  const steps: readonly FeedStep[] = turn.steps;
  const turnLink = useTurnLink(renderers, turn.question?.text ?? '');
  if (steps.length === 0) return null;
  return (
    <ol className={styles.steps}>
      {steps.map((step, index) => (
        <li className={styles.step} data-current={current && index === steps.length - 1 ? '' : undefined} data-steer={step.steer ? '' : undefined} key={step.key}>
          {step.steer ? <SteerRow images={step.steer.images?.length ? renderers.renderSteerImages?.(step.steer.images, step.steer.firstPhoto ?? 1) : undefined} text={step.steer.text ?? ''} /> : null}
          {step.event ? <Remark event={step.event} renderImage={renderers.renderImage} renderSourceLink={renderSourceLink} renderWorkshopLink={turnLink} /> : null}
          {step.actions.length > 0 ? (
            <ul aria-label="Действия" className={styles.actions}>
              {step.actions.map((action) => (
                <ActionRow action={action} key={action.key} onOpenSource={onOpenSource}
                  drawing={action.description.kind === 'draw' ? renderers.renderActionDrawing?.(action, live, turn)
                    : action.description.remembered ? renderers.renderActionRemembered?.(action, turn) : undefined} />
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
  threads: Spline,
  changes: History,
  scan: ScanText,
  remember: Brain,
  memory: Brain,
  other: Sparkles,
};

const statusLabels = { running: 'выполняется', error: 'ошибка' } as const;

function ActionRow({ action, drawing, onOpenSource }: { action: FeedAction; drawing?: ReactNode; onOpenSource: ChatFeedProps['onOpenSource'] }) {
  const { description, status } = action;
  const Icon = status === 'running' ? LoaderCircle : status === 'error' ? CircleAlert : actionIcons[description.kind];
  const source = description.source;
  return (
    <li className={styles.action} data-kind={description.kind} data-status={status}>
      <Icon aria-hidden="true" className={styles.actionIcon} size={15} />
      <span className={styles.actionText}>
        {source && onOpenSource ? (
          <button className={styles.actionSource} onClick={(event) => onOpenSource(source, event.currentTarget)} title="Открыть рядом с чатом" type="button">
            {description.label}
          </button>
        ) : <span className={styles.actionLabel}>{description.label}</span>}
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
  current = false,
  event,
  renderAssistantActions,
  renderImage,
  renderSourceLink,
  renderWorkshopLink,
}: {
  answer?: boolean;
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
        {event.text ? <Markdown renderImage={renderImage} renderSourceLink={renderSourceLink ? (source, label) => renderSourceLink(event, source, label) : undefined} renderWorkshopLink={renderWorkshopLink}>{event.text}</Markdown> : null}
      </div>
      {renderAssistantActions?.(event, contentRef)}
    </section>
  );
});

function Markdown({ children, renderImage, renderSourceLink, renderWorkshopLink }: {
  children: string;
  renderImage?: (image: MarkdownImage) => ReactNode;
  renderSourceLink?: React.ComponentProps<typeof MarkdownContent>['renderSourceLink'];
  renderWorkshopLink?: RenderTurnLink | undefined;
}) {
  return <MarkdownContent className={styles.markdown} renderImage={renderImage} renderSourceLink={renderSourceLink} {...(renderWorkshopLink ? { renderWorkshopLink } : {})}>{children}</MarkdownContent>;
}

function pendingTurn(question: string | undefined): FeedTurn {
  return {
    key: 'pending',
    steps: [],
    lateSteers: [],
    turnIds: [],
    live: true,
    activity: 'starting',
    ...(question ? {
      question: {
        cursor: 'pending', id: 'pending', jobId: '', kind: 'user', logicalSection: 0, role: 'main', text: question,
      },
    } : {}),
  };
}

function workCounts(turn: FeedTurn) {
  const steps = agentStepCount(turn);
  const actions = actionCount(turn);
  const steers = steerCount(turn);
  return [
    steps || !steers ? `${steps} ${plural(steps, ['шаг', 'шага', 'шагов'])}` : undefined,
    actions ? `${actions} ${plural(actions, ['действие', 'действия', 'действий'])}` : undefined,
    steers ? `${steers} ${plural(steers, ['уточнение', 'уточнения', 'уточнений'])}` : undefined,
  ].filter(Boolean).join(' · ');
}

function useElapsed(startedAt: string | undefined) {
  const started = startedAt ? Date.parse(startedAt) : Number.NaN;
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (Number.isNaN(started)) return undefined;
    const timer = window.setInterval(() => setNow(Date.now()), 1_000);
    return () => window.clearInterval(timer);
  }, [started]);
  return Number.isNaN(started) ? undefined : formatElapsed(now - started);
}

export function formatElapsed(milliseconds: number) {
  const seconds = Math.max(0, Math.floor(milliseconds / 1_000));
  const hours = Math.floor(seconds / 3_600);
  const minutes = Math.floor((seconds % 3_600) / 60);
  const rest = String(seconds % 60).padStart(2, '0');
  return hours ? `${hours}:${String(minutes).padStart(2, '0')}:${rest}` : `${minutes}:${rest}`;
}

function joinClassNames(...names: Array<string | undefined>) {
  return names.filter(Boolean).join(' ');
}
