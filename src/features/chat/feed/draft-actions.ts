import { pluralize } from '../../../ui/copy';
import type { ActionDescription, ActionStatus, SagaToolCall } from './action-types';

/**
 * The tools of the helper of «Письмо» (specs/2026-11-06-writing-agent.md) in the voice of the helper
 * (specs/2026-11-10-ux4-chat-waiting.md CW1.3): what it does now and what it did. The first line of a result is written
 * for the model («автор видит их у мест в тексте…»), so a line takes from it only a number.
 */
const draftWords: Readonly<Record<string, [done: string, running: string]>> = {
  state: ['Прочитал черновик', 'Читаю черновик'],
  replace: ['Заменил в тексте', 'Заменяю в тексте'],
  plan: ['Составил план сцен', 'Составляю план сцен'],
  decide: ['Записал ваше решение', 'Записываю ваше решение'],
  // The tools of the previous helper of a draft: their lines stay so that the old talks read.
  brief: ['Написал справку к главе', 'Пишу справку к главе'],
  variants: ['Предложил варианты сцены', 'Предлагаю варианты сцены'],
  continue: ['Дописал по замыслу', 'Дописываю по замыслу'],
};

const noteWords: Readonly<Record<string, [done: string, running: string, counted: (count: number) => string]>> = {
  canon: ['Сверил с книгой', 'Сверяю с книгой', (count) => count ? `${count} ${plural(count, ['расхождение', 'расхождения', 'расхождений'])}` : 'расхождений нет'],
  question: ['Записал вопросы к главе', 'Пишу вопросы к главе', (count) => `${count} ${plural(count, ['вопрос', 'вопроса', 'вопросов'])}`],
};

const writeWords: Readonly<Record<string, [done: string, running: string]>> = {
  scenes: ['Начал писать сцены', 'Начинаю писать сцены'],
  selection: ['Предложил варианты', 'Предлагаю варианты'],
  check: ['Начал проверять главу по саге', 'Начинаю проверять главу по саге'],
};

/** The pack of saga.moment: what the writing knows of the saga at the place of the chapter. */
export const momentWords: [done: string, running: string] = ['Собрал, что нужно знать к этой главе', 'Собираю, что нужно знать к этой главе'];

export function isDraftTool(tool: SagaToolCall) {
  return tool.namespace === 'draft' && (tool.name in draftWords || tool.name === 'notes' || tool.name === 'write');
}

export function describeDraftAction(tool: SagaToolCall, status: ActionStatus, triedAnotherWay: string): ActionDescription {
  const summary = status === 'done' ? tool.summary?.trim() : undefined;
  let words: [string, string] = draftWords[tool.name] ?? ['Записал заметки к главе', 'Пишу заметки к главе'];
  let detail: string | undefined;
  if (tool.name === 'notes') {
    const notes = noteWords[String(tool.arguments?.kind)];
    if (notes) {
      words = [notes[0], notes[1]];
      const written = summary ? /^Записано [^:]+: (\d+)/u.exec(summary) : null;
      detail = written ? notes[2](Number(written[1])) : undefined;
    }
  } else if (tool.name === 'write') {
    words = writeWords[String(tool.arguments?.mode)] ?? ['Начал письмо', 'Начинаю письмо'];
  } else if (tool.name === 'plan') {
    const plan = summary ? /^Версия плана (\d+) записана: (\d+) сцен/u.exec(summary) : null;
    detail = plan ? `версия ${plan[1]}: ${plan[2]} ${plural(Number(plan[2]), ['сцена', 'сцены', 'сцен'])}` : undefined;
  }
  const [label, activity] = words;
  const result: ActionDescription = { kind: 'other', label, activity };
  if (status === 'error') result.detail = triedAnotherWay;
  else if (detail) result.detail = detail;
  return result;
}

function plural(count: number, [one, few, many]: readonly [string, string, string]) {
  return pluralize(count, one, few, many);
}
