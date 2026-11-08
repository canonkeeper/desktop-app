import { useQuery } from '@tanstack/react-query';
import { Check, MessageSquare } from 'lucide-react';
import { useId, type ReactNode } from 'react';
import { sagaHasChatQueryOptions } from '../../../entities/chat/api';
import { textSourceView, type ManuscriptApiClient, type Project } from '../../../entities/manuscript/api';
import { Button } from '../../../ui/controls/Button';
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

interface FirstStepsProps {
  bookCount: number;
  /** Step 1: the form that names the saga. */
  createSaga: ReactNode;
  onAskFirstQuestion: () => void;
  projectTitle: string | null;
  step: FirstStep;
  /** The saga has text for the chat (`getIndexingStatus` → `exists`). */
  textReady: boolean;
  /** Step 2: the drop zone, or the request to verify the address. */
  uploadBooks: ReactNode;
}

/** «Три шага» до первого ответа по саге: a done step is checked, the current one acts, the next ones wait (R2.1.1). */
export function FirstSteps({ bookCount, createSaga, onAskFirstQuestion, projectTitle, step, textReady, uploadBooks }: FirstStepsProps) {
  const titleId = useId();
  return (
    <section aria-labelledby={titleId} className={styles.panel}>
      <div className={styles.header}>
        <h2 id={titleId}>Добро пожаловать в Canon Keeper</h2>
        <p>Три шага до первого ответа по вашей саге</p>
      </div>
      <ol className={styles.steps}>
        <Step
          current={step}
          done={`Сага «${projectTitle ?? ''}» создана`}
          number={1}
          text="Сага объединяет книги одной серии. Название можно поменять позже."
          title="Назовите сагу"
        >{createSaga}</Step>
        <Step
          current={step}
          done={`Загружено книг: ${bookCount}`}
          number={2}
          text="FB2, EPUB или DOCX, лучше все книги серии по порядку. Книга без номера в имени файла получит следующий номер."
          title="Загрузите книги"
        >{uploadBooks}</Step>
        <Step
          current={step}
          number={3}
          text="Когда книга подготовится, спросите о героях, событиях или противоречиях — ответ придёт со ссылками на главы."
          title="Задайте первый вопрос"
        >
          <div className={styles.actions}>
            <Button disabled={!textReady} onClick={onAskFirstQuestion} variant="primary">
              <MessageSquare aria-hidden="true" size={16} />Задать первый вопрос
            </Button>
          </div>
        </Step>
      </ol>
    </section>
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
