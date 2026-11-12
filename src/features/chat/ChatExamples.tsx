import { ChevronDown, ChevronUp } from 'lucide-react';
import { useId, useState, type RefObject } from 'react';
import { flushSync } from 'react-dom';
import type { SagaFirstQuestion } from '../../entities/saga/api';
import { useAccountSession } from '../../shared/session/account-session';
import { Button } from '../../ui/controls/Button';
import { blanksOf, type Blank } from './brackets';
import { longMinutes, usualMinutes } from './feed/LiveStatus';
import styles from './ResearchChatPanel.module.css';

// Data «Пустой чат»: general templates, not questions of one writer (Decision log 28.10).
const examples = [
  'Что [имя героя] знает о [событии] к главе [номер]?',
  'Где впервые упоминается [предмет или место]?',
  'Сколько лет [имя героя] в главе [номер] и не противоречит ли это раннему тексту?',
  'Перескажи, что происходило с [имя героя] в книге [номер].',
  'Не спорит ли с каноном эта сцена: [вставьте сцену]',
] as const;

interface ChatExamplesProps {
  /** The question field the example goes into. */
  field: RefObject<HTMLTextAreaElement | null>;
  message: string;
  /** The question goes into the field with the blanks its example put there (specs/2026-11-12-ux12-shell.md SH1.1). */
  onPick: (question: string, blanks: readonly Blank[]) => void;
  /** The questions of the saga by its map, once the server has answered (specs/2026-11-10-first-questions.md FQ1.4). */
  questions?: readonly SagaFirstQuestion[] | undefined;
  /** The field holds what the author typed. */
  typed: boolean;
}

/**
 * UC2.3 «Первый вопрос»: a new chat says what it knows and how long it takes, and offers questions. A saga with a map
 * offers three with the names of its book and folds the general examples under «Ещё примеры вопросов» (FQ1.5). A
 * question goes into the field whole, an example with its first «[…]» selected for typing over; neither is sent by
 * itself (R2.3.1, FQ1.6).
 */
export function ChatExamples({ field, message, onPick, questions, typed }: ChatExamplesProps) {
  // An unverified address has no question field to put a question into (UC1.4).
  const fieldShown = !useAccountSession()?.emailVerification;
  const titleId = useId();
  const moreId = useId();
  const [moreOpen, setMoreOpen] = useState(false);
  // The examples the author has begun with — focused or typed over before the questions came — stay in front of him.
  const [examplesHeld, setExamplesHeld] = useState(false);
  const saga = !examplesHeld && questions?.length === 3 ? questions : undefined;
  if (!saga && !examplesHeld && typed) setExamplesHeld(true);

  function pick(question: string, blanks: readonly Blank[], selection: [number, number]) {
    flushSync(() => onPick(question, blanks));
    const input = field.current;
    if (!input) return;
    input.focus();
    input.setSelectionRange(...selection);
  }

  const exampleButtons = examples.map((example) => (
    <Button key={example} onClick={() => pick(example, blanksOf(example), [example.indexOf('['), example.indexOf(']') + 1])} variant="outline">{example}</Button>
  ));

  return (
    <section aria-labelledby={titleId} className={styles.examples} onFocus={() => { if (!saga) setExamplesHeld(true); }}>
      <h2 id={titleId}>Спросите о своей саге</h2>
      <p>{message}</p>
      {fieldShown && saga ? (
        <>
          <div className={styles.exampleList}>
            {saga.map((question) => (
              <Button key={question.text} onClick={() => pick(question.text, [], [question.text.length, question.text.length])} variant="outline">{question.text}</Button>
            ))}
          </div>
          <Button aria-controls={moreOpen ? moreId : undefined} aria-expanded={moreOpen} className={styles.moreExamples} onClick={() => setMoreOpen((open) => !open)} variant="ghost">
            {moreOpen ? <ChevronUp aria-hidden="true" size={16} /> : <ChevronDown aria-hidden="true" size={16} />}
            Ещё примеры вопросов
          </Button>
          {moreOpen ? <div className={styles.exampleList} id={moreId}>{exampleButtons}</div> : null}
        </>
      ) : fieldShown ? (
        <div className={styles.exampleList}>{exampleButtons}</div>
      ) : null}
      {/* The term of the line of a running turn, said once for both (specs/2026-11-11-ux9-shelf-chat.md SC3.3, Ч-03). */}
      <p className={styles.examplesHint}>
        {`Отвечаю только по тексту ваших книг и даю ссылки на главы и абзацы. Обычный вопрос — ${usualMinutes}, обзор всей саги — ${longMinutes}. Можно уйти с этой страницы — ответ сохранится в этом чате.`}
      </p>
    </section>
  );
}
