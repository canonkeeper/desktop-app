import { useId, type RefObject } from 'react';
import { flushSync } from 'react-dom';
import { useAccountSession } from '../../shared/session/account-session';
import { Button } from '../../ui/controls/Button';
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
  onPick: (question: string) => void;
}

/**
 * UC2.3 «Первый вопрос»: a new chat says what it knows and how long it takes, and offers
 * examples. An example goes into the question field with its first «[…]» selected for typing
 * over; it is never sent by itself (R2.3.1).
 */
export function ChatExamples({ field, message, onPick }: ChatExamplesProps) {
  // An unverified address has no question field to put an example into (UC1.4).
  const fieldShown = !useAccountSession()?.emailVerification;
  const titleId = useId();

  function pick(example: string) {
    flushSync(() => onPick(example));
    const input = field.current;
    if (!input) return;
    input.focus();
    input.setSelectionRange(example.indexOf('['), example.indexOf(']') + 1);
  }

  return (
    <section aria-labelledby={titleId} className={styles.examples}>
      <h2 id={titleId}>Спросите о своей саге</h2>
      <p>{message}</p>
      {fieldShown ? (
        <div className={styles.exampleList}>
          {examples.map((example) => <Button key={example} onClick={() => pick(example)} variant="outline">{example}</Button>)}
        </div>
      ) : null}
      <p className={styles.examplesHint}>
        Отвечаю только по тексту ваших книг и даю ссылки на главы и абзацы (¶). Обычный вопрос — около минуты, обзор всей
        саги — до 15 минут. Можно уйти с этой страницы — ответ сохранится в этом чате.
      </p>
    </section>
  );
}
