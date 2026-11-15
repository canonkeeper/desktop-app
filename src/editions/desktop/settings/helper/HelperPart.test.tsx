// SPDX-License-Identifier: AGPL-3.0-only
import { useBlocker } from '@tanstack/react-router';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { choose, listOf, renderHelper, theApp, type, type TheApp } from './helper.test-helpers';

vi.mock('@tanstack/react-router', () => ({ useBlocker: vi.fn(() => ({ status: 'idle' })) }));

afterEach(() => {
  vi.mocked(useBlocker).mockImplementation(() => ({ status: 'idle' }) as ReturnType<typeof useBlocker>);
});

const key = 'key-test-0123456789';

/** DeepSeek with its key saved: the list of its models is asked of it (HW1.4). */
async function deepSeekWithKey(api: TheApp) {
  await renderHelper(api);
  choose('Провайдер', 'DeepSeek');
  type('Ключ', key);
  fireEvent.click(screen.getByRole('button', { name: 'Сохранить ключ' }));
  await screen.findByText('Хранится в хранилище паролей системы');
}

// specs/2026-11-13-desk-helper-web.md HW1.1–HW1.4, HW1.6 (board 44·9, 44·25, 44·36).
describe('«Помощник» of «Настройки»: «Свой ИИ»', () => {
  it('says what its own AI gives and begins with the choice of the provider', async () => {
    await renderHelper(await theApp());
    expect(await screen.findByText('Помощник будет отвечать в чате и читать книги саги через вашего провайдера ИИ — по вашему ключу.')).toBeTruthy();
    expect(screen.getByText('Вопросы чата и текст глав, которые он читает, уйдут этому провайдеру.')).toBeTruthy();
    expect(listOf('Провайдер').textContent).toBe('Выберите провайдера');
    expect(screen.queryByLabelText('Ключ')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Сохранить' })).toBeNull();
    fireEvent.click(listOf('Провайдер'));
    expect(within(screen.getByRole('listbox')).getAllByRole('option').map((option) => option.textContent))
      .toEqual(['Выберите провайдера', 'DeepSeek', 'OpenAI', 'Локальная модель (Ollama)', 'Другой, совместимый с OpenAI']);
  });

  it('shows the address of DeepSeek unchangeable and takes its key in a hidden field, then lists its models', async () => {
    const api = await theApp();
    const setKey = vi.spyOn(api, 'setAiKey');
    await renderHelper(api);
    choose('Провайдер', 'DeepSeek');
    const address = screen.getByLabelText<HTMLInputElement>('Адрес провайдера');
    expect(address.value).toBe('https://api.deepseek.com/v1');
    expect(address.readOnly).toBe(true);
    const field = screen.getByLabelText<HTMLInputElement>('Ключ');
    expect(field.type).toBe('password');
    expect(screen.getByText('Сохранится в хранилище паролей системы')).toBeTruthy();
    expect(screen.getByText('Список моделей провайдера появится после «Сохранить» — или впишите название модели')).toBeTruthy();
    // The button of the key stays and says why while the field is empty.
    expect(screen.getByRole('button', { name: 'Сохранить ключ' }).getAttribute('aria-disabled')).toBe('true');
    type('Ключ', ` ${key}\n`);
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить ключ' }));
    expect(await screen.findByText('Хранится в хранилище паролей системы')).toBeTruthy();
    // The key went once, without the spaces of the paste, after its provider was saved.
    expect(setKey).toHaveBeenCalledWith({ key });
    expect((await api.getAiSettings()).settings.own?.preset).toBe('deepseek');
    expect(screen.getByText('Ключ сохранён')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Заменить' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Удалить' })).toBeTruthy();
    // The models of DeepSeek, from its list.
    await waitFor(() => expect(listOf('Модель для чата').textContent).toBe('Выберите модель'));
    fireEvent.click(listOf('Модель для чата'));
    expect(within(screen.getByRole('listbox')).getAllByRole('option').map((option) => option.textContent))
      .toEqual(['Выберите модель', 'deepseek-chat', 'deepseek-reasoner', 'Другая — вписать название']);
  });

  it('asks «Другой» for its address and protocol and the key only if the provider wants one', async () => {
    await renderHelper(await theApp());
    choose('Провайдер', 'Другой, совместимый с OpenAI');
    const address = screen.getByLabelText<HTMLInputElement>('Адрес провайдера');
    expect(address.readOnly).toBe(false);
    const protocol = screen.getByRole('radiogroup', { name: 'Протокол' });
    expect(within(protocol).getAllByRole('radio').map((radio) => radio.textContent)).toEqual(['Определить', 'Responses', 'Chat Completions']);
    expect(within(protocol).getByRole('radio', { name: 'Определить' }).getAttribute('aria-checked')).toBe('true');
    expect(screen.getByText('«Определить» — приложение само узнает, как говорить с провайдером')).toBeTruthy();
    expect(screen.getByText('Нужен, если провайдер его просит. Сохранится в хранилище паролей системы')).toBeTruthy();
  });

  it('needs no key for the local model', async () => {
    await renderHelper(await theApp());
    choose('Провайдер', 'Локальная модель (Ollama)');
    expect(screen.getByLabelText<HTMLInputElement>('Адрес провайдера').value).toBe('http://127.0.0.1:11434/v1');
    expect(screen.getByText('Ключ не нужен')).toBeTruthy();
    expect(screen.queryByLabelText('Ключ')).toBeNull();
  });

  it('keeps what was written for each provider while the author switches between them unsaved', async () => {
    await renderHelper(await theApp());
    choose('Провайдер', 'Другой, совместимый с OpenAI');
    type('Адрес провайдера', 'https://llm.example.test/v1');
    type('Модель для чата', 'north-large');
    choose('Провайдер', 'DeepSeek');
    expect(screen.getByLabelText<HTMLInputElement>('Адрес провайдера').value).toBe('https://api.deepseek.com/v1');
    expect(screen.getByLabelText<HTMLInputElement>('Модель для чата').value).toBe('');
    choose('Провайдер', 'Другой, совместимый с OpenAI');
    expect(screen.getByLabelText<HTMLInputElement>('Адрес провайдера').value).toBe('https://llm.example.test/v1');
    expect(screen.getByLabelText<HTMLInputElement>('Модель для чата').value).toBe('north-large');
  });

  it('says the key lies in a file where the computer has no store of keys', async () => {
    const api = await theApp({ aiSecretsStore: 'file' });
    await deepSeekWithKeyIn(api, 'Хранится в файле в папке данных: в системе нет хранилища паролей');
  });

  it('keeps the typed key in the field when the shell did not keep it, and «Сохранить ещё раз» saves it', async () => {
    const api = await theApp();
    await renderHelper(api);
    choose('Провайдер', 'DeepSeek');
    type('Ключ', 'key-test-not-kept-0001');
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить ключ' }));
    expect(await screen.findByText('Ключ не сохранён: хранилище паролей системы не ответило. Если система спрашивала пароль — введите его и сохраните ключ ещё раз')).toBeTruthy();
    expect(screen.getByLabelText<HTMLInputElement>('Ключ').value).toBe('key-test-not-kept-0001');
    expect(screen.getByLabelText('Ключ').getAttribute('aria-invalid')).toBe('true');
    expect((await api.getAiSettings()).keySaved).toBe(false);
    type('Ключ', key);
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить ключ' }));
    expect(await screen.findByText('Хранится в хранилище паролей системы')).toBeTruthy();
    expect((await api.getAiSettings()).keySaved).toBe(true);
  });

  it('says «Сохранить ещё раз» at the key the shell did not keep', async () => {
    const api = await theApp();
    await renderHelper(api);
    choose('Провайдер', 'DeepSeek');
    type('Ключ', 'key-test-not-kept-0001');
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить ключ' }));
    expect(await screen.findByRole('button', { name: 'Сохранить ещё раз' })).toBeTruthy();
  });

  it('replaces a kept key and removes it only after the question', async () => {
    const api = await theApp();
    const remove = vi.spyOn(api, 'deleteAiKey');
    await deepSeekWithKey(api);
    fireEvent.click(screen.getByRole('button', { name: 'Заменить' }));
    expect(screen.getByLabelText<HTMLInputElement>('Ключ').value).toBe('');
    fireEvent.click(screen.getByRole('button', { name: 'Отмена' }));
    expect(screen.getByText('Хранится в хранилище паролей системы')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Удалить' }));
    const dialog = screen.getByRole('dialog', { name: 'Удалить ключ?' });
    expect(within(dialog).getByText('Вернуть будет нельзя.')).toBeTruthy();
    expect(within(dialog).getByText('Помощник перестанет отвечать, пока вы не введёте ключ снова.')).toBeTruthy();
    expect(remove).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить' }));
    expect(await screen.findByText('Ключ удалён')).toBeTruthy();
    expect(remove).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(screen.getByLabelText<HTMLInputElement>('Ключ').value).toBe(''));
  });

  it('never has the key in an answer of the server, a read request, the page or the cache', async () => {
    const api = await theApp();
    const reads: unknown[] = [];
    for (const name of ['getAiSettings', 'listAiModels', 'getAiCheck'] as const) {
      const real = api[name].bind(api);
      vi.spyOn(api, name).mockImplementation(async (...args: unknown[]) => {
        const answer = await (real as (...input: unknown[]) => Promise<unknown>)(...args);
        reads.push(args, answer);
        return answer as never;
      });
    }
    const client = await renderHelper(api);
    choose('Провайдер', 'DeepSeek');
    type('Ключ', key);
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить ключ' }));
    await screen.findByText('Хранится в хранилище паролей системы');
    await waitFor(() => expect(listOf('Модель для чата')).toBeTruthy());
    expect(reads.length).toBeGreaterThan(0);
    expect(JSON.stringify(reads)).not.toContain(key);
    expect(document.body.innerHTML).not.toContain(key);
    expect(JSON.stringify(client.getQueryCache().getAll().map((query) => query.state))).not.toContain(key);
    expect(JSON.stringify(client.getMutationCache().getAll().map((mutation) => mutation.state))).not.toContain(key);
  });

  it('keeps a key the shell did not keep in the field alone: no write of the cache holds it', async () => {
    const api = await theApp();
    const client = await renderHelper(api);
    choose('Провайдер', 'DeepSeek');
    type('Ключ', 'key-test-not-kept-0002');
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить ключ' }));
    expect(await screen.findByRole('button', { name: 'Сохранить ещё раз' })).toBeTruthy();
    expect(screen.getByLabelText<HTMLInputElement>('Ключ').value).toBe('key-test-not-kept-0002');
    expect(JSON.stringify(client.getMutationCache().getAll().map((mutation) => mutation.state))).not.toContain('key-test-not-kept-0002');
    expect(JSON.stringify(client.getQueryCache().getAll().map((query) => query.state))).not.toContain('key-test-not-kept-0002');
  });

  it('saves only the key the author sees: one typed for DeepSeek is not the key of the local model', async () => {
    const api = await theApp();
    const setKey = vi.spyOn(api, 'setAiKey');
    await renderHelper(api);
    choose('Провайдер', 'DeepSeek');
    type('Ключ', 'key-test-deepseek-0123456789');
    choose('Провайдер', 'Локальная модель (Ollama)');
    expect(screen.queryByLabelText('Ключ')).toBeNull();
    type('Модель для чата', 'gemma4');
    type('Модель для чтения книг', 'gemma4');
    fireEvent.click(screen.getByRole('button', { name: 'Проверить связь' }));
    expect(await screen.findByRole('button', { name: 'Остановить' })).toBeTruthy();
    expect(setKey).not.toHaveBeenCalled();
    expect(await api.getAiSettings()).toMatchObject({ settings: { own: { preset: 'ollama' } }, keySaved: false });
    // What was typed for DeepSeek is still DeepSeek's (HW1.2).
    choose('Провайдер', 'DeepSeek');
    expect(screen.getByLabelText<HTMLInputElement>('Ключ').value).toBe('key-test-deepseek-0123456789');
  });

  it('replaces the key of the provider «Заменить» was pressed for, not of the one the author switched to', async () => {
    const api = await theApp();
    await deepSeekWithKey(api);
    fireEvent.click(screen.getByRole('button', { name: 'Заменить' }));
    type('Ключ', 'key-test-deepseek-new-0001');
    choose('Провайдер', 'OpenAI');
    expect(screen.getByText('Это ключ «DeepSeek». Для «OpenAI» нажмите «Заменить»')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Заменить' })).toBeTruthy();
    choose('Провайдер', 'DeepSeek');
    expect(screen.getByLabelText<HTMLInputElement>('Ключ').value).toBe('key-test-deepseek-new-0001');
    expect(screen.getByRole('button', { name: 'Отмена' })).toBeTruthy();
  });

  it('keeps an edit the author made while «Сохранить» was on its way', async () => {
    const api = await theApp();
    const real = api.updateAiSettings.bind(api);
    let answer = () => {};
    const update = vi.spyOn(api, 'updateAiSettings').mockImplementation(async (body) => {
      await new Promise<void>((resolve) => { answer = resolve; });
      return real(body);
    });
    await renderHelper(api);
    choose('Провайдер', 'Локальная модель (Ollama)');
    type('Модель для чата', 'gemma4');
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(update).toHaveBeenCalledTimes(1));
    type('Модель для чтения книг', 'qwen3:8b');
    answer();
    expect(await screen.findByText('Настройки помощника сохранены')).toBeTruthy();
    expect((await api.getAiSettings()).settings.own).toMatchObject({ chatModel: 'gemma4', cardsModel: '' });
    await waitFor(() => expect(listOf('Модель для чтения книг').textContent).toBe('qwen3:8b'));
    expect(listOf('Модель для чата').textContent).toBe('gemma4');
    // The edit is still to be saved.
    expect(screen.getByRole('button', { name: 'Сохранить' }).getAttribute('aria-disabled')).not.toBe('true');
  });

  it.each([
    ['https://llm.example.test/v1', 'Провайдер не отдаёт список моделей — впишите название модели'],
    ['https://down.example.test/v1', 'Провайдер не ответил: проверьте адрес и сеть. Название модели можно вписать'],
    ['https://busy.example.test/v1', 'Провайдер просит подождать — попробуйте через несколько минут. Название модели можно вписать'],
    ['https://broken.example.test/v1', 'Модель не дала ответа — проверьте ещё раз или выберите другую модель. Название модели можно вписать'],
  ])('says what the provider at %s gave for its models, and the name is written', async (address, words) => {
    const api = await theApp();
    await renderHelper(api);
    choose('Провайдер', 'Другой, совместимый с OpenAI');
    type('Адрес провайдера', address);
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
    expect(await screen.findByText(words)).toBeTruthy();
    type('Модель для чата', 'north-large');
    expect(screen.getByLabelText<HTMLInputElement>('Модель для чата').value).toBe('north-large');
  });

  it('says the provider did not take the key when it asks the list with it', async () => {
    const api = await theApp();
    await renderHelper(api);
    choose('Провайдер', 'Другой, совместимый с OpenAI');
    type('Адрес провайдера', 'https://llm.example.test/v1');
    type('Ключ', 'key-test-refused-0001');
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить ключ' }));
    expect(await screen.findByText('Провайдер не принял ключ. Название модели можно вписать')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Спросить список ещё раз' })).toBeTruthy();
  });

  it('chooses a model of the list and writes one the list has not', async () => {
    const api = await theApp();
    await deepSeekWithKey(api);
    await waitFor(() => expect(listOf('Модель для чата').textContent).toBe('Выберите модель'));
    choose('Модель для чата', 'deepseek-chat');
    choose('Модель для чтения книг', 'Другая — вписать название');
    type('Модель для чтения книг', 'deepseek-v4');
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
    expect(await screen.findByText('Настройки помощника сохранены')).toBeTruthy();
    expect((await api.getAiSettings()).settings.own).toMatchObject({ chatModel: 'deepseek-chat', cardsModel: 'deepseek-v4' });
    expect(screen.getByLabelText<HTMLInputElement>('Модель для чтения книг').value).toBe('deepseek-v4');
  });

  it('puts a refusal of the server at its field and keeps the rest', async () => {
    await renderHelper(await theApp());
    choose('Провайдер', 'Другой, совместимый с OpenAI');
    type('Адрес провайдера', 'ftp://llm.example.test');
    type('Модель для чата', 'north-large');
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
    const address = await screen.findByLabelText('Адрес провайдера');
    await waitFor(() => expect(address.getAttribute('aria-invalid')).toBe('true'));
    expect(screen.getByText('Адрес — http:// или https:// и имя сервера, без пробелов, имени и пароля, до 300 знаков')).toBeTruthy();
    expect(screen.getByLabelText<HTMLInputElement>('Модель для чата').value).toBe('north-large');
    // Mended, the refusal goes.
    type('Адрес провайдера', 'https://llm.example.test/v1');
    expect(address.getAttribute('aria-invalid')).toBeNull();
  });

  it('says «Всё сохранено» at «Сохранить» with nothing to save', async () => {
    const api = await theApp();
    await api.updateAiSettings({ source: 'own', own: { preset: 'ollama', chatModel: 'gemma4', cardsModel: 'gemma4' } });
    await renderHelper(api);
    const save = await screen.findByRole('button', { name: 'Сохранить' });
    expect(save.getAttribute('aria-disabled')).toBe('true');
    fireEvent.click(save);
    expect(screen.getByText('Всё сохранено')).toBeTruthy();
  });

  it('says a new window applies after the answer of the helper that runs', async () => {
    const api = await theApp({ aiAnswerRunning: true });
    await api.updateAiSettings({ source: 'own', own: { preset: 'ollama', chatModel: 'gemma4', cardsModel: 'gemma4', advanced: { contextWindow: 64_000 } } });
    await renderHelper(api);
    expect((await screen.findByRole('note')).textContent).toBe('Применится после ответа помощника');
  });

  it('asks «Не сохранять?» on the way out with unsaved changes, and only then', async () => {
    const proceed = vi.fn();
    const reset = vi.fn();
    let options: { disabled?: boolean; shouldBlockFn: (input: unknown) => boolean } | undefined;
    vi.mocked(useBlocker).mockImplementation((given) => {
      options = given as unknown as typeof options;
      return { status: 'idle' } as ReturnType<typeof useBlocker>;
    });
    await renderHelper(await theApp());
    expect(options?.disabled).toBe(true);
    choose('Провайдер', 'DeepSeek');
    expect(options?.disabled).toBe(false);
    const leaves = (pathname: string, search: Record<string, string>) => options!.shouldBlockFn({ current: { pathname: '/settings', search: { part: 'helper' } }, next: { pathname, search } });
    expect(leaves('/settings', { part: 'data' })).toBe(true);
    expect(leaves('/manuscript/books', {})).toBe(true);
    expect(leaves('/settings', { part: 'helper' })).toBe(false);
    vi.mocked(useBlocker).mockImplementation(() => ({ status: 'blocked', proceed, reset }) as unknown as ReturnType<typeof useBlocker>);
    choose('Провайдер', 'OpenAI');
    const dialog = await screen.findByRole('dialog', { name: 'Не сохранять?' });
    expect(within(dialog).getByText('Изменения настроек помощника не будут сохранены.')).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Не сохранять' }));
    expect(proceed).toHaveBeenCalledTimes(1);
  });
});

async function deepSeekWithKeyIn(api: TheApp, words: string) {
  await renderHelper(api);
  choose('Провайдер', 'DeepSeek');
  expect(screen.getByText(words.replace('Хранится', 'Сохранится'))).toBeTruthy();
  type('Ключ', key);
  fireEvent.click(screen.getByRole('button', { name: 'Сохранить ключ' }));
  expect(await screen.findByText(words)).toBeTruthy();
}
