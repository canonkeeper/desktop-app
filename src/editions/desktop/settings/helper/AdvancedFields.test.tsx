// SPDX-License-Identifier: AGPL-3.0-only
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { choose, listOf, renderHelper, theApp, type, type TheApp } from './helper.test-helpers';

vi.mock('@tanstack/react-router', () => ({ useBlocker: vi.fn(() => ({ status: 'idle' })) }));

/** The local model saved with its models: «Дополнительно» is all there is to change. */
async function savedOllama(advanced: Record<string, unknown> = {}) {
  const api = await theApp();
  await api.updateAiSettings({ source: 'own', own: { preset: 'ollama', chatModel: 'gemma4', cardsModel: 'gemma4', advanced } });
  const update = vi.spyOn(api, 'updateAiSettings');
  await renderHelper(api);
  return { api, update };
}

function open() {
  fireEvent.click(screen.getByRole('button', { name: 'Дополнительно' }));
  return screen.getByRole('group', { name: 'Дополнительно' });
}

function save() {
  fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
}

async function sent(update: { mock: { calls: Parameters<TheApp['updateAiSettings']>[] } }) {
  await waitFor(() => expect(update).toHaveBeenCalled());
  return update.mock.calls.at(-1)![0].own?.advanced;
}

// specs/2026-11-13-desk-helper-web.md HW1.5 (board 44·25, «„Дополнительно“ раскрыто»).
describe('«Дополнительно» of «Свой ИИ»', () => {
  it('is folded until the author opens it, and each field says what it is for', async () => {
    await savedOllama();
    const toggle = screen.getByRole('button', { name: 'Дополнительно' });
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(screen.queryByLabelText('Окно контекста, токенов')).toBeNull();
    const group = open();
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    for (const [label, value, hint] of [
      ['Окно контекста, токенов', '128 000', 'Сколько текста модель видит за раз — как пишет провайдер. Для чата нужно не меньше 64 000'],
      ['Предел ответа, токенов', '16 000', 'Самый длинный ответ модели. Для чтения книг нужно не меньше 16 000'],
      ['Запросов одновременно', '4', 'Сколько запросов уходит провайдеру разом: больше — книги читаются быстрее, если провайдер это позволяет'],
    ] as const) {
      const field = within(group).getByLabelText<HTMLInputElement>(label);
      expect(field.value).toBe(value);
      expect(document.getElementById(field.getAttribute('aria-describedby')!)?.textContent).toBe(hint);
    }
    const reasoning = within(group).getByRole('radiogroup', { name: 'Рассуждения' });
    expect(within(reasoning).getAllByRole('radio').map((radio) => radio.textContent)).toEqual(['Нет', 'Низкие', 'Средние', 'Высокие']);
    expect(within(reasoning).getByRole('radio', { name: 'Средние' }).getAttribute('aria-checked')).toBe('true');
    expect(document.getElementById(reasoning.getAttribute('aria-describedby')!)?.textContent).toBe('Чем больше модель рассуждает, тем точнее ответ, но дольше и дороже');
    for (const label of ['Цена за миллион токенов: вход', 'кэшированный вход', 'выход']) {
      const price = within(group).getByLabelText<HTMLInputElement>(label);
      expect(price.value).toBe('');
      expect(price.placeholder).toBe('не указана');
      expect(document.getElementById(price.getAttribute('aria-describedby')!.split(' ')[0]!)?.textContent).toBe('Цены нужны только для оценки стоимости чтения книг');
    }
    // specs/2026-11-13-desk-consent-web.md RW3.1 (board 44·25): the price of a cached input is optional and says why; the
    // currency of the prices is the dollar until the author chooses another.
    expect(within(group).getByText('Необязательно: если провайдер берёт за повторный вход меньше')).toBeTruthy();
    expect(listOf('Валюта').textContent).toContain('$ — доллар');
    fireEvent.click(listOf('Валюта'));
    expect(within(screen.getByRole('listbox')).getAllByRole('option').map((option) => option.textContent)).toEqual(['₽ — рубль', '$ — доллар', '¥ — юань', '€ — евро']);
  });

  it('sends every field under its own name', async () => {
    const { api, update } = await savedOllama();
    const group = open();
    type('Окно контекста, токенов', '200 000');
    type('Предел ответа, токенов', '32000');
    fireEvent.click(within(group).getByRole('radio', { name: 'Высокие' }));
    type('Запросов одновременно', '8');
    type('Цена за миллион токенов: вход', '0,27');
    type('кэшированный вход', '0,07');
    type('выход', '1.1');
    choose('Валюта', '₽ — рубль');
    save();
    expect(await sent(update)).toEqual({
      contextWindow: 200_000, maxOutput: 32_000, reasoning: 'high', parallel: 8, priceInPerMillion: 0.27, priceOutPerMillion: 1.1,
      priceCachedInPerMillion: 0.07, currency: 'RUB',
    });
    expect(await screen.findByText('Настройки помощника сохранены')).toBeTruthy();
    expect((await api.getAiSettings()).settings.own?.advanced).toMatchObject({ contextWindow: 200_000, priceInPerMillion: 0.27, priceCachedInPerMillion: 0.07, currency: 'RUB' });
    // Saved, the fields say the numbers as the author reads them, and the currency is read back.
    expect(screen.getByLabelText<HTMLInputElement>('Цена за миллион токенов: вход').value).toBe('0,27');
    expect(screen.getByLabelText<HTMLInputElement>('кэшированный вход').value).toBe('0,07');
    expect(listOf('Валюта').textContent).toContain('₽ — рубль');
  });

  it('takes a price of six thousand a million and sends an empty price of the cache as taken away', async () => {
    const { update } = await savedOllama({ priceInPerMillion: 20, priceOutPerMillion: 80, priceCachedInPerMillion: 2, currency: 'RUB' });
    open();
    expect(screen.getByLabelText<HTMLInputElement>('кэшированный вход').value).toBe('2');
    type('выход', '6 000');
    type('кэшированный вход', '');
    save();
    expect(await sent(update)).toMatchObject({ priceOutPerMillion: 6_000, priceCachedInPerMillion: null, currency: 'RUB' });
  });

  it('sends an empty price as not named', async () => {
    const { update } = await savedOllama({ priceInPerMillion: 20, priceOutPerMillion: 80 });
    open();
    expect(screen.getByLabelText<HTMLInputElement>('Цена за миллион токенов: вход').value).toBe('20');
    type('Цена за миллион токенов: вход', '');
    expect(screen.getByLabelText<HTMLInputElement>('Цена за миллион токенов: вход').placeholder).toBe('не указана');
    save();
    expect(await sent(update)).toMatchObject({ priceInPerMillion: null, priceOutPerMillion: 80 });
  });

  it('puts the refusal of the server at its field and opens the fold for it', async () => {
    const { update } = await savedOllama();
    open();
    type('Окно контекста, токенов', '5 000');
    type('Запросов одновременно', '6');
    fireEvent.click(screen.getByRole('button', { name: 'Дополнительно' }));
    expect(screen.queryByLabelText('Окно контекста, токенов')).toBeNull();
    save();
    const window = await screen.findByLabelText('Окно контекста, токенов');
    await waitFor(() => expect(window.getAttribute('aria-invalid')).toBe('true'));
    expect(screen.getByText('Число от 8 000 до 2 000 000')).toBeTruthy();
    expect(update).toHaveBeenCalledTimes(1);
    // The rest is not lost.
    expect(screen.getByLabelText<HTMLInputElement>('Запросов одновременно').value).toBe('6');
  });

  it('refuses at its field a number the page cannot read, sending nothing', async () => {
    const { update } = await savedOllama();
    open();
    type('Цена за миллион токенов: вход', 'двадцать');
    save();
    const price = screen.getByLabelText('Цена за миллион токенов: вход');
    expect(price.getAttribute('aria-invalid')).toBe('true');
    expect(screen.getByText('Число от 0 до 1 000 000, не больше четырёх знаков после запятой')).toBeTruthy();
    expect(update).not.toHaveBeenCalled();
  });
});

// specs/2026-11-13-desk-consent-web.md RW3.1 (board 44·25, «Валюта меняется при согласии»): a change of the currency or the
// removal of the prices while sagas are read under a sum in money — the server says how many — asks first; none — no question.
describe('the question before a change of the prices', () => {
  async function underMoney(sagas: number) {
    const { api, update } = await savedOllama({ priceInPerMillion: 20, priceOutPerMillion: 80, currency: 'RUB' });
    const read = api.getAiSettings.bind(api);
    vi.spyOn(api, 'getAiSettings').mockImplementation(async (...input) => ({ ...await read(...input), moneyStopSagas: sagas }));
    cleanup();
    await renderHelper(api);
    open();
    return { api, update };
  }

  it('asks before a new currency and saves only by «Сохранить»', async () => {
    const { update } = await underMoney(2);
    choose('Валюта', '$ — доллар');
    save();
    const question = await screen.findByRole('dialog', { name: 'Чтение книг встанет на паузу: сумму остановки нужно будет назвать заново' });
    expect(question.textContent).toContain('Это коснётся чтения 2 саг.');
    expect(update).not.toHaveBeenCalled();
    fireEvent.click(within(question).getByRole('button', { name: 'Отмена' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(update).not.toHaveBeenCalled();
    save();
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Сохранить' }));
    expect(await sent(update)).toMatchObject({ currency: 'USD' });
  });

  it('asks before the prices are taken away', async () => {
    await underMoney(1);
    type('выход', '');
    save();
    expect((await screen.findByRole('dialog')).textContent).toContain('Это коснётся чтения 1 саги.');
  });

  it('does not ask for a new price in the same currency, nor without a saga under a sum in money', async () => {
    const first = await underMoney(1);
    type('выход', '90');
    save();
    expect(await sent(first.update)).toMatchObject({ priceOutPerMillion: 90, currency: 'RUB' });
    expect(screen.queryByRole('dialog')).toBeNull();
    cleanup();
    const none = await underMoney(0);
    choose('Валюта', '€ — евро');
    save();
    expect(await sent(none.update)).toMatchObject({ currency: 'EUR' });
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});
