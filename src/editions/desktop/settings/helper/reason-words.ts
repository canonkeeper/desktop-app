// SPDX-License-Identifier: AGPL-3.0-only
import type { Preset, Reason } from './helper-words';

/** A reason in the words of the author, and what to do about it when the words are not that already. */
export interface ReasonWords {
  line: string;
  advice?: string;
}

/**
 * Why the provider or the model failed the author — the one table of the page over the closed list `AiReason` of the server
 * (specs/2026-11-13-desk-helper-web.md HW2.3): the words of the board 44·26, then the table Data, then the Decision log.
 * `small_window` and `small_limit` are what the settings lack, said by the verdicts (`check-words.ts`); `slow` is the end
 * of a check that ran out of its three minutes.
 */
export const reasonWords: Record<Reason, (preset: Preset) => ReasonWords> = {
  unreachable: () => ({ line: 'Провайдер не ответил: проверьте адрес и сеть' }),
  key_refused: () => ({ line: 'Провайдер не принял ключ' }),
  no_model: () => ({ line: 'У провайдера нет модели с таким названием' }),
  no_tools: () => ({ line: 'Модель не умеет вызывать инструменты' }),
  start_only: (preset) => ({
    line: 'Модель видит только начало длинного текста',
    advice: preset === 'ollama'
      ? 'В Ollama увеличьте контекст модели (OLLAMA_CONTEXT_LENGTH) и проверьте ещё раз'
      : 'Выберите модель, которая читает длинный текст целиком, и проверьте ещё раз',
  }),
  cut_answer: () => ({ line: 'Модель обрывает длинный ответ', advice: 'Выберите модель с длинным ответом или увеличьте «Предел ответа» в «Дополнительно»' }),
  rate_limited: () => ({ line: 'Провайдер просит подождать — попробуйте через несколько минут' }),
  other: () => ({ line: 'Модель не дала ответа — проверьте ещё раз или выберите другую модель' }),
  small_window: () => ({ line: 'Окно контекста меньше 64 000 — для чата этого мало. Увеличьте его в «Дополнительно», если модель умеет больше' }),
  small_limit: () => ({ line: 'Предел ответа меньше 16 000 — для чтения книг этого мало. Увеличьте его в «Дополнительно»' }),
  slow: () => ({ line: 'Модель отвечает слишком медленно: проверка не уложилась в три минуты. То, что успели проверить, — выше' }),
};

/** A code the table does not know (a newer server): the general words, and the code for «Написать нам» (HW2.3). */
export const unknownReason = 'Проверка не удалась';

/** The words of a reason; one the table does not know says the general words with its code. */
export function wordsOfReason(reason: string, preset: Preset): ReasonWords & { code: string | null } {
  const known = Object.hasOwn(reasonWords, reason) ? reasonWords[reason as Reason](preset) : null;
  return known ? { ...known, code: null } : { line: unknownReason, code: reason };
}
