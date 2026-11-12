import { useQuery } from '@tanstack/react-query';
import { Check, MessageSquare } from 'lucide-react';
import { useId, type ReactNode } from 'react';
import { sagaHasChatQueryOptions } from '../../../entities/chat/api';
import { textSourceView, type ManuscriptApiClient, type Project } from '../../../entities/manuscript/api';
import { bookFileKinds } from '../../../entities/manuscript/book-file-kinds';
import { Button } from '../../../ui/controls/Button';
import { formatCount } from '../../../ui/copy';
import { firstSagaTitle } from './FirstScreen';
import styles from './FirstSteps.module.css';

export type FirstStep = 1 | 2 | 3;

/**
 * UC2.1: the step of the first success the writer is on, from the shelf queries and the chats of
 * the saga only; null once the saga has a chat (or for a saga the writer cannot change).
 */
export function useFirstStep({ api, bookCount, canEdit, loaded, project }: {
  api: Pick<ManuscriptApiClient, 'listChatSessions'>;
  bookCount: number;
  canEdit: boolean;
  loaded: boolean;
  project: Project | null;
}): FirstStep | null {
  const hasChat = useQuery({
    ...sagaHasChatQueryOptions(api, { projectId: project?.id ?? '', sourceView: textSourceView }),
    enabled: Boolean(project) && canEdit,
  }).data;
  if (!loaded) return null;
  if (!project) return 1;
  if (!canEdit || hasChat !== false) return null;
  return bookCount === 0 ? 2 : 3;
}

/**
 * Step 2 calls for as many books as a saga of the tier takes (specs/2026-11-11-ux9-shelf-chat.md SC1.8, J-09): one book —
 * «Загрузите книгу», more — the whole series up to the limit; null while the limit is unknown or there is none.
 */
export function uploadStepWords(sagaBooks: number | null) {
  if (sagaBooks === 1) return { title: 'Загрузите книгу', text: `${bookFileKinds}. В саге на вашем тарифе — одна книга: начните с первой.` };
  const upTo = sagaBooks !== null && sagaBooks > 1 ? ` — до ${sagaBooks}. Порядок` : ' — порядок';
  return { title: 'Загрузите книги', text: `${bookFileKinds}. Можно сразу все книги саги${upTo} покажу перед загрузкой.` };
}

interface FirstStepsProps {
  bookCount: number;
  /** The books a saga of the author's tier takes, by the counters of the account; null while unknown or unlimited (SC1.8). */
  sagaBooks?: number | null;
  onAskFirstQuestion: () => void;
  projectTitle: string | null;
  /** The line of the reading of the saga (specs/2026-11-10-ux3-saga-state.md SS2.5): it stands in step 3 with its one button. */
  sagaState?: ReactNode;
  /** Step 1 is done once the saga stands on its shelf: before it there is the first screen (FS1.2). */
  step: Exclude<FirstStep, 1>;
  /** The saga has text for the chat (`getIndexingStatus` → `exists`). */
  textReady: boolean;
  /** Step 2: the drop zone, or the request to verify the address. */
  uploadBooks: ReactNode;
  /**
   * Under the steps while the saga has no book (specs/2026-11-10-ux3-first-screen.md FS1.5): «Посмотреть на примере» and
   * «Вычитать мою главу» of a build that has them.
   */
  ways?: ReactNode;
}

/**
 * The steps of a saga to its first answer (R2.1.1; FS1.5): «Сага создана» is checked, the current step acts, the next one
 * waits. «Добро пожаловать» is said once, on the first screen, and «Три шага» are gone (FS1.2). A saga «Поговорить» or a
 * proofreading founded is named by the line of the shell, so the step does not name it twice (FS1.4). While the saga is
 * read, the line of its reading stands in step 3 with its one button (specs/2026-11-10-ux3-saga-state.md SS2.5).
 */
export function FirstSteps({ bookCount, onAskFirstQuestion, projectTitle, sagaBooks = null, sagaState, step, textReady, uploadBooks, ways }: FirstStepsProps) {
  const titleId = useId();
  const upload = uploadStepWords(sagaBooks);
  const named = projectTitle && projectTitle !== firstSagaTitle;
  return (
    <div className={styles.first}>
      <section aria-labelledby={titleId} className={styles.panel}>
        <div className={styles.header}>
          <h2 id={titleId}>До первого ответа</h2>
        </div>
        <ol className={styles.steps}>
          <Step current={step} done={named ? `Сага «${projectTitle}» создана` : 'Сага создана'} number={1} text="" title="Назовите сагу">{null}</Step>
          <Step
            current={step}
            done={formatCount(bookCount, 'книга загружена', 'книги загружены', 'книг загружено')}
            number={2}
            text={upload.text}
            title={upload.title}
          >{uploadBooks}</Step>
          <Step
            current={step}
            number={3}
            text="Когда помощник прочитает книгу, спросите о героях, событиях или противоречиях — ответ придёт со ссылками на главы."
            title="Задайте первый вопрос"
          >
            {sagaState ?? <div className={styles.actions}>
              <Button disabledReason={textReady ? null : 'Помощник начал читать книгу — спросить можно будет через пару минут.'} onClick={onAskFirstQuestion} variant="primary">
                <MessageSquare aria-hidden="true" size={16} />Задать первый вопрос
              </Button>
            </div>}
          </Step>
        </ol>
      </section>
      {ways ? <div className={styles.ways}>{ways}</div> : null}
    </div>
  );
}

function Step({ children, current, done, number, text, title }: {
  children: ReactNode;
  current: FirstStep;
  done?: string;
  number: FirstStep;
  text: string;
  title: string;
}) {
  const state = number < current ? 'done' : number === current ? 'current' : 'next';
  return (
    <li aria-current={state === 'current' ? 'step' : undefined} className={styles.step} data-state={state}>
      <span aria-hidden="true" className={styles.marker}>{state === 'done' ? <Check size={14} /> : number}</span>
      <div className={styles.body}>
        {state === 'done' ? <h3>{done}</h3> : <>
          <h3>{title}</h3>
          <p>{text}</p>
          {state === 'current' ? children : null}
        </>}
      </div>
    </li>
  );
}
