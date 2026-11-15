// SPDX-License-Identifier: AGPL-3.0-only
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { checkGroups, verdictWords } from './check-words';
import type { Probe, Reason, Verdict } from './helper-words';
import { reasonWords, wordsOfReason } from './reason-words';

const model = 'deepseek-chat';
type Kind = Probe['probe'];

/** The probes of one model in both roles, in the order of the check (PB2.3); `failed` names the probe that did not pass. */
function oneModel(failed: Partial<Record<Kind, Reason>> = {}, name = model): Probe[] {
  const order: [Kind, Probe['role']][] = [['short', 'chat'], ['tool', 'chat'], ['long_input', 'chat'], ['calibrate', 'books'], ['long_output', 'books']];
  return order.map(([probe, role]) => ({ probe, role, model: name, passed: !failed[probe], reason: failed[probe] ?? null }));
}

const fit: Verdict = { fit: 'fit', reason: null };
const unfit = (reason: Reason): Verdict => ({ fit: 'unfit', reason });

/** The lines of a check as the author reads them: the words, the advice under them, the mark. */
function lines(probes: Probe[], preset: 'deepseek' | 'ollama' = 'deepseek') {
  return checkGroups(probes, preset).flatMap((group) => group.lines.map((line) => [line.passed ? '✓' : '✗', line.words, line.advice ?? ''].join(' ').trim()));
}

// specs/2026-11-13-desk-helper-web.md HW2.2, HW2.3 (board 44·26, the table Data).
describe('the words of «Проверить связь»', () => {
  it('has the words of every reason of the contract and of no other (a new code of the server without words fails here)', () => {
    const contract = JSON.parse(readFileSync(join(process.cwd(), 'contracts/openapi.desktop.json'), 'utf8')) as {
      components: { schemas: { AiReason: { enum: string[] } } };
    };
    expect(Object.keys(reasonWords).sort()).toEqual([...contract.components.schemas.AiReason.enum].sort());
    for (const reason of contract.components.schemas.AiReason.enum) {
      expect(wordsOfReason(reason, 'deepseek')).toMatchObject({ code: null });
      expect(wordsOfReason(reason, 'deepseek').line).not.toBe('Проверка не удалась');
    }
  });

  it('«Годится»: five lines with their marks and «Годится для чата и для чтения книг»', () => {
    expect(lines(oneModel())).toEqual([
      '✓ Связь есть', '✓ Модель отвечает', '✓ Модель умеет вызывать инструменты', '✓ Длинный текст читает целиком', '✓ Длинный ответ даёт целиком',
    ]);
    expect(verdictWords(fit, fit)).toEqual({ tone: 'success', words: 'Годится для чата и для чтения книг' });
  });

  it('«Только книги»: the model calls no tool — the books fit, the chat asks for another model', () => {
    expect(lines(oneModel({ tool: 'no_tools' }))).toEqual([
      '✓ Связь есть', '✓ Модель отвечает', '✗ Модель не умеет вызывать инструменты', '✓ Длинный текст читает целиком', '✓ Длинный ответ даёт целиком',
    ]);
    expect(verdictWords(unfit('no_tools'), fit)).toEqual({
      tone: 'warning', words: 'Для чтения книг годится. Чат с этой моделью работать не будет — выберите для чата другую',
    });
  });

  it('«Короткий контекст»: the line says what to do — in Ollama its context, elsewhere another model', () => {
    expect(lines(oneModel({ long_input: 'start_only' }, 'qwen3:14b'), 'ollama')).toEqual([
      '✓ Связь есть', '✓ Модель отвечает', '✓ Модель умеет вызывать инструменты',
      '✗ Модель видит только начало длинного текста В Ollama увеличьте контекст модели (OLLAMA_CONTEXT_LENGTH) и проверьте ещё раз',
      '✓ Длинный ответ даёт целиком',
    ]);
    expect(lines(oneModel({ long_input: 'start_only' }))[3]).toBe(
      '✗ Модель видит только начало длинного текста Выберите модель, которая читает длинный текст целиком, и проверьте ещё раз',
    );
    expect(verdictWords(unfit('start_only'), unfit('start_only'))).toEqual({ tone: 'warning', words: 'Не годится ни для чата, ни для чтения книг' });
  });

  it('«Обрезает ответ»: the line with its advice; the chat fits', () => {
    expect(lines(oneModel({ long_output: 'cut_answer' })).at(-1)).toBe(
      '✗ Модель обрывает длинный ответ Выберите модель с длинным ответом или увеличьте «Предел ответа» в «Дополнительно»',
    );
    expect(verdictWords(fit, unfit('cut_answer'))).toEqual({ tone: 'warning', words: 'Для чата годится. Для чтения книг не годится' });
  });

  it('«Нет связи»: one line and nothing after it, no verdict', () => {
    expect(lines([{ probe: 'short', role: 'chat', model, passed: false, reason: 'unreachable' }])).toEqual(['✗ Провайдер не ответил: проверьте адрес и сеть']);
    expect(verdictWords(unfit('unreachable'), unfit('unreachable'))).toBeNull();
  });

  it('«Ключ не подошёл»: the link is there, the key is not taken, no verdict', () => {
    expect(lines([{ probe: 'short', role: 'chat', model, passed: false, reason: 'key_refused' }])).toEqual(['✓ Связь есть', '✗ Провайдер не принял ключ']);
    expect(verdictWords(unfit('key_refused'), unfit('key_refused'))).toBeNull();
  });

  it('says the words of the table Data for each code', () => {
    expect(lines([{ probe: 'short', role: 'chat', model, passed: false, reason: 'rate_limited' }]).at(-1))
      .toBe('✗ Провайдер просит подождать — попробуйте через несколько минут');
    expect(lines([{ probe: 'short', role: 'chat', model, passed: false, reason: 'no_model' }]).at(-1)).toBe('✗ У провайдера нет модели с таким названием');
    expect(verdictWords(unfit('small_window'), fit)?.words)
      .toBe('Для чтения книг годится. Окно контекста меньше 64 000 — для чата этого мало. Увеличьте его в «Дополнительно», если модель умеет больше');
    expect(verdictWords({ fit: 'thin', reason: 'small_window' }, fit)?.words)
      .toBe('Для чтения книг годится. Чат работает. На длинных сагах ответы будут беднее: окно контекста меньше 128 000');
    expect(verdictWords(fit, unfit('small_limit'))?.words)
      .toBe('Для чата годится. Предел ответа меньше 16 000 — для чтения книг этого мало. Увеличьте его в «Дополнительно»');
  });

  it('says a check that ran out of its three minutes once, with what it learned above and no line for what it did not end', () => {
    const probes = oneModel({ long_output: 'slow' });
    expect(lines(probes)).toEqual([
      '✓ Связь есть', '✓ Модель отвечает', '✓ Модель умеет вызывать инструменты', '✓ Длинный текст читает целиком',
    ]);
    expect(verdictWords(fit, unfit('slow'))?.words)
      .toBe('Для чата годится. Модель отвечает слишком медленно: проверка не уложилась в три минуты. То, что успели проверить, — выше');
    expect(verdictWords(unfit('slow'), unfit('slow'))?.words)
      .toBe('Модель отвечает слишком медленно: проверка не уложилась в три минуты. То, что успели проверить, — выше');
  });

  it('gives the calibration no line, passed or not: the verdicts do not read it', () => {
    const probes = oneModel({ calibrate: 'other' });
    expect(lines(probes)).toEqual([
      '✓ Связь есть', '✓ Модель отвечает', '✓ Модель умеет вызывать инструменты', '✓ Длинный текст читает целиком', '✓ Длинный ответ даёт целиком',
    ]);
  });

  it.each([
    ['no_model', '✗ У провайдера нет модели с таким названием'],
    ['rate_limited', '✗ Провайдер просит подождать — попробуйте через несколько минут'],
  ] as const)('says %s once though every probe of the model failed for it, as the server answers it', (reason, words) => {
    const every = oneModel({ short: reason, tool: reason, long_input: reason, calibrate: reason, long_output: reason });
    expect(lines(every)).toEqual(['✓ Связь есть', words]);
    const chat = 'north-large';
    const books = 'north-small';
    const two: Probe[] = [
      { probe: 'short', role: 'chat', model: chat, passed: false, reason },
      { probe: 'short', role: 'books', model: books, passed: false, reason },
      { probe: 'tool', role: 'chat', model: chat, passed: false, reason },
      { probe: 'long_input', role: 'chat', model: chat, passed: false, reason },
      { probe: 'long_input', role: 'books', model: books, passed: false, reason },
      { probe: 'calibrate', role: 'books', model: books, passed: false, reason },
      { probe: 'long_output', role: 'books', model: books, passed: false, reason },
    ];
    expect(checkGroups(two, 'openai').map((group) => [group.role, group.lines.map((line) => line.words)])).toEqual([
      [null, ['Связь есть']], ['chat', [words.slice(2)]], ['books', [words.slice(2)]],
    ]);
  });

  it('says a code the table does not know in the general words, with the code for «Написать нам»', () => {
    const probes = [{ probe: 'short', role: 'chat', model, passed: false, reason: 'quota_gone' }] as unknown as Probe[];
    expect(checkGroups(probes, 'deepseek')[0]!.lines.at(-1)).toEqual({ passed: false, words: 'Проверка не удалась', code: 'quota_gone' });
    expect(verdictWords(unfit('quota_gone' as Reason), fit)?.words).toBe('Для чтения книг годится. Проверка не удалась');
  });

  // specs/2026-11-13-desk-consent-web.md RW3.3 (board 44·26 «Идёт по пробам»): while the check runs, the probe it waits for
  // stands under the probes that went, in the words of what it asks, marked as running; the calibration gives no line.
  it('says the probe that runs under the probes that went, and none for the calibration', () => {
    const went = oneModel().slice(0, 2);
    const running = (probes: Probe[], current: Pick<Probe, 'probe' | 'role' | 'model'> | null) => checkGroups(probes, 'deepseek', current)
      .flatMap((group) => group.lines.map((line) => [line.running ? '◌' : line.passed ? '✓' : '✗', line.words].join(' ')));
    expect(running([], { probe: 'short', role: 'chat', model })).toEqual(['◌ Проверяю, отвечает ли модель']);
    expect(running(went, { probe: 'long_input', role: 'chat', model })).toEqual([
      '✓ Связь есть', '✓ Модель отвечает', '✓ Модель умеет вызывать инструменты', '◌ Проверяю, читает ли модель длинный текст целиком',
    ]);
    expect(running(oneModel().slice(0, 3), { probe: 'calibrate', role: 'books', model })).toEqual([
      '✓ Связь есть', '✓ Модель отвечает', '✓ Модель умеет вызывать инструменты', '✓ Длинный текст читает целиком',
    ]);
    expect(running(oneModel().slice(0, 4), { probe: 'long_output', role: 'books', model }).at(-1)).toBe('◌ Проверяю, даёт ли модель длинный ответ целиком');
    // Without a probe that runs, the lines are those of the probes that went.
    expect(running(went, null)).toEqual(['✓ Связь есть', '✓ Модель отвечает', '✓ Модель умеет вызывать инструменты']);
  });

  it('puts the probe that runs under the role of its model', () => {
    const probes: Probe[] = [
      { probe: 'short', role: 'chat', model: 'north-large', passed: true, reason: null },
      { probe: 'short', role: 'books', model: 'north-small', passed: true, reason: null },
    ];
    const groups = checkGroups(probes, 'openai', { probe: 'tool', role: 'chat', model: 'north-large' });
    expect(groups.map((group) => [group.role, group.lines.map((line) => line.words)])).toEqual([
      [null, ['Связь есть']],
      ['chat', ['Модель отвечает', 'Проверяю, умеет ли модель вызывать инструменты']],
      ['books', ['Модель отвечает']],
    ]);
  });

  it('puts the lines of two models under their roles', () => {
    const chat = 'north-large';
    const books = 'north-small';
    const probes: Probe[] = [
      { probe: 'short', role: 'chat', model: chat, passed: true, reason: null },
      { probe: 'short', role: 'books', model: books, passed: true, reason: null },
      { probe: 'tool', role: 'chat', model: chat, passed: true, reason: null },
      { probe: 'long_input', role: 'chat', model: chat, passed: true, reason: null },
      { probe: 'long_input', role: 'books', model: books, passed: true, reason: null },
      { probe: 'calibrate', role: 'books', model: books, passed: true, reason: null },
      { probe: 'long_output', role: 'books', model: books, passed: false, reason: 'cut_answer' },
    ];
    const groups = checkGroups(probes, 'openai');
    expect(groups.map((group) => [group.role, group.model, group.lines.map((line) => line.words)])).toEqual([
      [null, null, ['Связь есть']],
      ['chat', chat, ['Модель отвечает', 'Модель умеет вызывать инструменты', 'Длинный текст читает целиком']],
      ['books', books, ['Модель отвечает', 'Длинный текст читает целиком', 'Модель обрывает длинный ответ']],
    ]);
  });
});
