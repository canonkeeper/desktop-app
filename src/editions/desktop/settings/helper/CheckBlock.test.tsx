// SPDX-License-Identifier: AGPL-3.0-only
import { cleanup, act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { choose, renderHelper, theApp, type, type TheApp } from './helper.test-helpers';

vi.mock('@tanstack/react-router', () => ({ useBlocker: vi.fn(() => ({ status: 'idle' })) }));

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
});

afterEach(() => {
  vi.useRealTimers();
});

/** DeepSeek with its key and one model in both roles, saved: the check may start. */
async function deepSeek(api: TheApp, chat = 'deepseek-chat', books = chat) {
  await api.setAiKey({ key: 'key-test-0123456789' });
  await api.updateAiSettings({ source: 'own', own: { preset: 'deepseek', chatModel: chat, cardsModel: books } });
}

function checkButton() {
  return screen.getByRole('button', { name: 'Проверить связь' });
}

/** The check of the page, by its name. */
function checkGroup() {
  return screen.getByRole('group', { name: 'Проверка связи' });
}

/** The page asks again every second while the check runs: one probe a read of the mock. */
async function nextRead() {
  await act(async () => { await vi.advanceTimersByTimeAsync(1_050); });
}

/** Reads until the check ends. */
async function toTheEnd() {
  for (let read = 0; read < 10 && screen.queryByRole('region', { name: 'Проверка связи' }); read += 1) await nextRead();
}

function lineWords() {
  return within(checkGroup()).getAllByRole('listitem').map((line) => line.textContent);
}

// specs/2026-11-13-desk-helper-web.md HW2.1–HW2.3 (board 44·26).
describe('«Проверить связь» of «Помощник»', () => {
  it('runs probe by probe with «Остановить», closed with its reason while it runs, and ends with the verdict', async () => {
    const api = await theApp();
    await deepSeek(api);
    await renderHelper(api);
    fireEvent.click(checkButton());
    const work = await screen.findByRole('region', { name: 'Проверка связи' });
    expect(within(work).getAllByText('Проверяю связь…').length).toBeGreaterThan(0);
    expect(within(work).getByText('До трёх минут')).toBeTruthy();
    expect(within(work).getByRole('button', { name: 'Остановить' })).toBeTruthy();
    // The button stays and says why it is closed.
    expect(checkButton().getAttribute('aria-disabled')).toBe('true');
    fireEvent.click(checkButton());
    expect(screen.getByText('Проверка уже идёт')).toBeTruthy();
    // The probes come as they go; the probe the check waits for stands last, with the mark of a work that goes and the words
    // of what it asks (specs/2026-11-13-desk-consent-web.md RW3.3, board 44·26 «Идёт по пробам»).
    expect(lineWords()).toEqual(['Проверяю, отвечает ли модель']);
    await nextRead();
    expect(lineWords()).toEqual(['Связь есть', 'Модель отвечает', 'Проверяю, умеет ли модель вызывать инструменты']);
    const lines = () => within(checkGroup()).getAllByRole('listitem');
    expect(lines().map((line) => line.getAttribute('data-running'))).toEqual([null, null, 'true']);
    expect(lines().at(-1)!.getAttribute('data-passed')).toBeNull();
    // The probe ended: its line is its result, and the next one runs under it.
    await nextRead();
    expect(lineWords()).toEqual([
      'Связь есть', 'Модель отвечает', 'Модель умеет вызывать инструменты', 'Проверяю, читает ли модель длинный текст целиком',
    ]);
    expect(lines()[2]!.getAttribute('data-passed')).toBe('true');
    // The calibration has no line, running or not.
    await nextRead();
    expect(lineWords()).toEqual(['Связь есть', 'Модель отвечает', 'Модель умеет вызывать инструменты', 'Длинный текст читает целиком']);
    await nextRead();
    expect(lineWords().at(-1)).toBe('Проверяю, даёт ли модель длинный ответ целиком');
    await toTheEnd();
    expect(screen.queryByRole('region', { name: 'Проверка связи' })).toBeNull();
    expect(lineWords()).toEqual([
      'Связь есть', 'Модель отвечает', 'Модель умеет вызывать инструменты', 'Длинный текст читает целиком', 'Длинный ответ даёт целиком',
    ]);
    expect(within(checkGroup()).getByRole('note').textContent).toBe('Годится для чата и для чтения книг');
    expect(within(checkGroup()).queryByText('Проверено до изменения настроек')).toBeNull();
    expect(checkButton().getAttribute('aria-disabled')).toBeNull();
  });

  it('says «Только книги» for a model that calls no tool', async () => {
    const api = await theApp();
    await deepSeek(api, 'deepseek-reasoner');
    await renderHelper(api);
    fireEvent.click(checkButton());
    await screen.findByRole('region', { name: 'Проверка связи' });
    await toTheEnd();
    expect(lineWords()).toContain('Модель не умеет вызывать инструменты');
    expect(within(checkGroup()).getByRole('note').textContent)
      .toBe('Для чтения книг годится. Чат с этой моделью работать не будет — выберите для чата другую');
  });

  it('stops at «Остановить» and keeps the report it had', async () => {
    const api = await theApp();
    const cancel = vi.spyOn(api, 'cancelAiCheck');
    await deepSeek(api);
    await renderHelper(api);
    fireEvent.click(checkButton());
    const work = await screen.findByRole('region', { name: 'Проверка связи' });
    fireEvent.click(within(work).getByRole('button', { name: 'Остановить' }));
    expect(await screen.findByText('Проверка остановлена')).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Проверка связи' })).toBeNull());
    expect(cancel).toHaveBeenCalledTimes(1);
    // Nothing was checked before: no report to show.
    expect(screen.queryByRole('group', { name: 'Проверка связи' })).toBeNull();
    expect((await api.getAiCheck()).state).toBe('canceled');
  });

  it('goes on while the author is away and shows how far it went when he is back', async () => {
    const api = await theApp();
    await deepSeek(api);
    await renderHelper(api);
    fireEvent.click(checkButton());
    await screen.findByRole('region', { name: 'Проверка связи' });
    cleanup();
    // Away: the check went on by itself.
    await api.getAiCheck();
    await renderHelper(api);
    const work = await screen.findByRole('region', { name: 'Проверка связи' });
    expect(within(work).getAllByText('Проверяю связь…').length).toBeGreaterThan(0);
    expect(lineWords().length).toBeGreaterThanOrEqual(3);
    await toTheEnd();
    expect(within(checkGroup()).getByRole('note').textContent).toBe('Годится для чата и для чтения книг');
  });

  it('saves what the author sees before it checks', async () => {
    const api = await theApp();
    await deepSeek(api);
    const save = vi.spyOn(api, 'updateAiSettings');
    const start = vi.spyOn(api, 'startAiCheck');
    await renderHelper(api);
    await waitFor(() => expect(screen.getByRole('button', { name: /^Модель для чата/u }).textContent).toBe('deepseek-chat'));
    choose('Модель для чата', 'deepseek-reasoner');
    fireEvent.click(checkButton());
    await screen.findByRole('region', { name: 'Проверка связи' });
    expect(save).toHaveBeenCalledTimes(1);
    expect(save.mock.invocationCallOrder[0]!).toBeLessThan(start.mock.invocationCallOrder[0]!);
    expect((await api.getAiSettings()).settings.own?.chatModel).toBe('deepseek-reasoner');
  });

  it('says a report made before a change of what it checked, and checks again', async () => {
    const api = await theApp();
    await deepSeek(api);
    await renderHelper(api);
    fireEvent.click(checkButton());
    await screen.findByRole('region', { name: 'Проверка связи' });
    await toTheEnd();
    // «Дополнительно» does not make it stale: its verdicts read what is saved.
    fireEvent.click(screen.getByRole('button', { name: /Дополнительно/u }));
    type('Окно контекста, токенов', '100 000');
    expect(within(checkGroup()).queryByText('Проверено до изменения настроек')).toBeNull();
    // The model of a role does, saved or not.
    choose('Модель для чтения книг', 'deepseek-reasoner');
    expect(within(checkGroup()).getByText('Проверено до изменения настроек')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
    expect(await screen.findByText('Настройки помощника сохранены')).toBeTruthy();
    expect(within(checkGroup()).getByText('Проверено до изменения настроек')).toBeTruthy();
    // The verdicts read the saved window now: the chat works, poorer on long sagas.
    expect(within(checkGroup()).getByRole('note').textContent)
      .toBe('Для чтения книг годится. Чат работает. На длинных сагах ответы будут беднее: окно контекста меньше 128 000');
    fireEvent.click(within(checkGroup()).getByRole('button', { name: 'Проверить ещё раз' }));
    await screen.findByRole('region', { name: 'Проверка связи' });
    await toTheEnd();
    expect(within(checkGroup()).queryByText('Проверено до изменения настроек')).toBeNull();
  });

  it('is closed with its reason until both models are named, and the helper works without it', async () => {
    const api = await theApp();
    await api.setAiKey({ key: 'key-test-0123456789' });
    await api.updateAiSettings({ source: 'own', own: { preset: 'deepseek', chatModel: 'deepseek-chat', cardsModel: '' } });
    const start = vi.spyOn(api, 'startAiCheck');
    await renderHelper(api);
    expect(checkButton().getAttribute('aria-disabled')).toBe('true');
    fireEvent.click(checkButton());
    expect(screen.getByText('Сначала выберите модели для чата и для чтения книг')).toBeTruthy();
    expect(start).not.toHaveBeenCalled();
  });

  it('says the provider did not answer in one line, without a verdict', async () => {
    const api = await theApp();
    await api.updateAiSettings({
      source: 'own', own: { preset: 'custom', baseUrl: 'https://down.example.test/v1', protocol: 'auto', chatModel: 'north-large', cardsModel: 'north-large' },
    });
    await renderHelper(api);
    fireEvent.click(checkButton());
    await screen.findByRole('region', { name: 'Проверка связи' });
    await toTheEnd();
    expect(lineWords()).toEqual(['Провайдер не ответил: проверьте адрес и сеть']);
    expect(within(checkGroup()).queryByRole('note')).toBeNull();
  });

  it('says a check that ran out of its three minutes and what it learned', async () => {
    const api = await theApp();
    await deepSeek(api, 'deepseek-chat', 'slow-reader');
    await renderHelper(api);
    fireEvent.click(checkButton());
    await screen.findByRole('region', { name: 'Проверка связи' });
    await toTheEnd();
    expect((await api.getAiCheck()).state).toBe('timed_out');
    expect(within(checkGroup()).getByRole('note').textContent)
      .toBe('Для чата годится. Модель отвечает слишком медленно: проверка не уложилась в три минуты. То, что успели проверить, — выше');
  });
});
