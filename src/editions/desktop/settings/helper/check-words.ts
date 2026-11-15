// SPDX-License-Identifier: AGPL-3.0-only
import type { Preset, Probe, Reason, Verdict } from './helper-words';
import { reasonWords, wordsOfReason } from './reason-words';

/** The words of «Проверить связь» (specs/2026-11-13-desk-helper-web.md HW2; board 44·26, the table Data). */
export const checkWords = {
  check: 'Проверить связь',
  again: 'Проверить ещё раз',
  running: 'Проверяю связь…',
  term: 'До трёх минут',
  label: 'Проверка связи',
  stop: 'Остановить',
  stopped: 'Проверка остановлена',
  busy: 'Проверка уже идёт',
  stale: 'Проверено до изменения настроек',
  noModels: 'Сначала выберите модели для чата и для чтения книг',
  unavailable: 'Проверка не началась: приложение закрывается',
  notStarted: 'Проверка не началась. Попробуйте ещё раз',
  connected: 'Связь есть',
} as const;

/** What a probe that passed says (44·26); the calibration counts the tokens of Russian text and has no line. */
const passedWords: Record<Probe['probe'], string | null> = {
  short: 'Модель отвечает',
  tool: 'Модель умеет вызывать инструменты',
  long_input: 'Длинный текст читает целиком',
  long_output: 'Длинный ответ даёт целиком',
  calibrate: null,
};

/**
 * What a probe that runs asks (specs/2026-11-13-desk-consent-web.md RW3.3; board 44·26 «Идёт по пробам»): the line under the
 * probes that went while the check waits for it. The calibration has no line, running or not.
 */
const runningWords: Record<Probe['probe'], string | null> = {
  short: 'Проверяю, отвечает ли модель',
  tool: 'Проверяю, умеет ли модель вызывать инструменты',
  long_input: 'Проверяю, читает ли модель длинный текст целиком',
  long_output: 'Проверяю, даёт ли модель длинный ответ целиком',
  calibrate: null,
};

/** The probe that runs, as getAiCheck says it (`current`). */
export type RunningProbe = Pick<Probe, 'probe' | 'role' | 'model'>;

/** One line of the check with its mark (`Pattern/Desktop/Check Line`): what passed, or what failed and what to do. */
export interface CheckLine {
  passed: boolean;
  /** The probe runs: the line says what it asks, with the mark of a work that goes. */
  running?: boolean;
  words: string;
  advice?: string;
  /** A reason the table does not know: its code for «Написать нам». */
  code: string | null;
}

/** The lines of one model; with two models in the roles each has its own, under the name of its role. */
export interface CheckGroup {
  role: 'chat' | 'books' | null;
  model: string | null;
  lines: CheckLine[];
}

function lineOf(probe: Probe, preset: Preset): CheckLine | null {
  if (probe.passed) {
    const words = passedWords[probe.probe];
    return words ? { passed: true, words, code: null } : null;
  }
  const reason = wordsOfReason(probe.reason ?? 'other', preset);
  return { passed: false, words: reason.line, ...(reason.advice ? { advice: reason.advice } : {}), code: reason.code };
}

/**
 * The lines of a model: a line a probe, and a failure said once — a provider without the model or that asks to wait fails
 * every probe of it the same way (the check goes on after them, `Reason.Stops`).
 */
function linesOf(probes: readonly Probe[], preset: Preset): CheckLine[] {
  const lines: CheckLine[] = [];
  for (const probe of probes) {
    const line = lineOf(probe, preset);
    if (line && (line.passed || !lines.some((said) => !said.passed && said.words === line.words))) lines.push(line);
  }
  return lines;
}

/**
 * The lines of the probes that went, in their order (HW2.1, HW2.2): «Связь есть» first — or that the provider did not
 * answer, and nothing after it; then a line a probe, a failure once. A probe the three minutes stopped has no line: the
 * verdict says the check ran out (Data, `slow`). The calibration has no line, passed or not: the verdicts do not read it.
 * While the check runs, the probe it waits for stands last in the group of its model, in the words of what it asks (RW3.3).
 */
export function checkGroups(probes: readonly Probe[], preset: Preset, current: RunningProbe | null = null): CheckGroup[] {
  const went = probes.filter((probe) => probe.reason !== 'slow' && probe.probe !== 'calibrate');
  const runningWord = current ? runningWords[current.probe] : null;
  const running: CheckLine | null = runningWord ? { passed: false, running: true, words: runningWord, code: null } : null;
  if (went.length === 0) return running ? [{ role: null, model: null, lines: [running] }] : [];
  const first = went[0]!;
  if (first.reason === 'unreachable') return [{ role: null, model: null, lines: [lineOf(first, preset)!] }];
  const connection: CheckLine = { passed: true, words: checkWords.connected, code: null };
  const models = [...new Set([...went.map((probe) => probe.model), ...(running && current ? [current.model] : [])])];
  if (models.length <= 1) return [{ role: null, model: null, lines: [connection, ...linesOf(went, preset), ...(running ? [running] : [])] }];
  return [
    { role: null, model: null, lines: [connection] },
    ...(['chat', 'books'] as const).map((role) => ({
      role, model: went.find((probe) => probe.role === role)?.model ?? (current?.role === role ? current.model : null),
      lines: [...linesOf(went.filter((probe) => probe.role === role), preset), ...(running && current?.role === role ? [running] : [])],
    })).filter((group) => group.lines.length > 0),
  ];
}

/** The reasons of the provider, not of a model: the lines say them, the verdict does not repeat them. */
const ofTheProvider = new Set<Reason>(['unreachable', 'key_refused', 'rate_limited']);

/** The verdict of the check (HW2.2, HW2.3): a line in the tone of its alert, or none when the lines say it all. */
export interface VerdictWords {
  tone: 'success' | 'warning';
  words: string;
}

/**
 * The verdicts «для чата» and «для чтения книг» in one line (board 44·26, the table Data): both fit — «Годится для чата и
 * для чтения книг»; a chat without the tools of a model fit for the books — the words of the board; otherwise what fits
 * first, then what does not, each once. A provider that did not answer, refused the key or asks to wait is said by the
 * lines alone; a check that ran out of its three minutes says so.
 */
export function verdictWords(chat: Verdict, books: Verdict): VerdictWords | null {
  if (chat.fit === 'fit' && books.fit === 'fit') return { tone: 'success', words: 'Годится для чата и для чтения книг' };
  if (chat.fit === 'unfit' && books.fit === 'unfit' && chat.reason && books.reason && ofTheProvider.has(chat.reason) && ofTheProvider.has(books.reason)) return null;
  const fits: string[] = [];
  const not: string[] = [];
  const said = (words: string) => { if (!not.includes(words)) not.push(words); };
  if (books.fit === 'fit') fits.push('Для чтения книг годится');
  if (chat.fit === 'fit') fits.push('Для чата годится');
  const chatLacks = chat.fit === 'thin' ? 'Чат работает. На длинных сагах ответы будут беднее: окно контекста меньше 128 000'
    : chat.fit === 'unfit' ? lackOf(chat.reason, 'chat') : null;
  const booksLacks = books.fit === 'unfit' ? lackOf(books.reason, 'books') : null;
  if (chatLacks === 'model' && booksLacks === 'model') said('Не годится ни для чата, ни для чтения книг');
  else {
    if (chatLacks) said(chatLacks === 'model' ? 'Для чата не годится' : chatLacks);
    if (booksLacks) said(booksLacks === 'model' ? 'Для чтения книг не годится' : booksLacks);
  }
  return { tone: 'warning', words: [...fits, ...not].join('. ') };
}

/** What a role lacks, in words; `model` — the model does not do what the role needs, its line says what to do. */
function lackOf(reason: Reason | null, role: 'chat' | 'books'): string | 'model' | null {
  if (!reason) return 'model';
  if (reason === 'no_tools' && role === 'chat') return 'Чат с этой моделью работать не будет — выберите для чата другую';
  if (reason === 'small_window' || reason === 'small_limit' || reason === 'slow' || ofTheProvider.has(reason)) return reasonWords[reason]('custom').line;
  if (!Object.hasOwn(reasonWords, reason)) return wordsOfReason(reason, 'custom').line;
  return 'model';
}
