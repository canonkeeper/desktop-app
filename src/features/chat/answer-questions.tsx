import { useMemo, useRef, useState, type RefObject } from 'react';
import { Confirm } from '../../ui/overlays/Confirm';
import type { AnswerAsk } from './AnswerActions';
import type { Blank } from './brackets';

interface AnswerQuestionsInput {
  /** What stands in the field now. */
  taskText: string;
  /** Puts a text into the field, without the blanks of an example. */
  changeTask: (text: string, blanks?: readonly Blank[]) => void;
  field: RefObject<HTMLTextAreaElement | null>;
  /** The author may ask in this chat now. */
  enabled: boolean;
  /** Why a button under an answer cannot put a question now; it stays and says so. */
  unavailable: string | null;
}

/**
 * A question a button under an answer puts into the field (specs/2026-11-11-ux4-after-answer.md AA1.3, AA1.5; the place of
 * an answer, «Спросить, откуда это», AA1.6): the focus stands at its end and nothing is sent; what the author typed is not
 * lost — «Заменить набранное?» asks first. The page keeps one function for the buttons of every answer: typing in the field
 * draws no answer anew.
 */
export function useAnswerQuestions({ changeTask, enabled, field, taskText, unavailable }: AnswerQuestionsInput) {
  const [replacing, setReplacing] = useState<string | null>(null);

  function put(text: string) {
    changeTask(text, []);
    window.requestAnimationFrame(() => {
      const node = field.current;
      if (!node) return;
      node.focus();
      node.setSelectionRange(node.value.length, node.value.length);
    });
  }

  function ask(text: string) {
    const typed = taskText.trim();
    if (typed && typed !== text.trim()) {
      setReplacing(text);
      return;
    }
    put(text);
  }
  const askRef = useRef(ask);
  askRef.current = ask;

  const answerAsk = useMemo<AnswerAsk | undefined>(() => enabled
    ? { put: (text) => askRef.current(text), unavailable }
    : undefined, [enabled, unavailable]);

  const question = replacing !== null ? (
    <Confirm action="Заменить" consent="no" onCancel={() => setReplacing(null)} title="Заменить набранное?"
      onConfirm={() => { put(replacing); setReplacing(null); }}>
      {`В поле уже есть ваш текст. Вместо него встанет: «${replacing.trim()}»`}
    </Confirm>
  ) : null;

  return { answerAsk, ask, question };
}
