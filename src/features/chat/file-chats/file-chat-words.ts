import type { SagaTranscriptTurn } from '../../../entities/saga-file/api';
import { formatCount, pluralize } from '../../../ui/copy';

/**
 * The words of the chats of a saga file, word for word by board 44·38 (specs/2026-11-13-desk-boards3.md;
 * specs/2026-11-13-desk-sagafile-web.md SW3).
 */
export const fileChatWords = {
  section: 'Из файла саги',
  notice: 'Этот чат пришёл из файла саги. Продолжить его нельзя — задайте вопрос в новом чате',
  newChat: 'Новый чат',
  attached: (title: string) => `К вопросу была приложена глава „${title}“ — в файл она не вошла`,
  /** «В этом чате было ещё 3 вопроса других участников — в файл они не вошли»; nothing of none. */
  others: (count: number) => count > 0
    ? `В этом чате ${pluralize(count, 'был', 'было', 'было')} ещё ${formatCount(count, 'вопрос', 'вопроса', 'вопросов')} других участников — в файл ${count === 1 ? 'он не вошёл' : 'они не вошли'}`
    : null,
} as const;

/**
 * What stands in place of the answer of a turn that has none (SW3.2): the helper stopped, the helper failed. A turn still
 * going when the saga was saved has no column on the board; its words are the decision of the spec.
 */
export function turnEndWords(turn: Pick<SagaTranscriptTurn, 'outcome' | 'answer'>): string | null {
  if (turn.outcome === 'answered' && turn.answer?.trim()) return null;
  if (turn.outcome === 'interrupted') return 'Ответа нет: помощник был остановлен';
  if (turn.outcome === 'in_progress') return 'Ответа нет: помощник ещё отвечал, когда сага сохранялась в файл';
  return 'Ответа нет: помощник не смог ответить';
}
