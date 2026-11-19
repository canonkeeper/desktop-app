// SPDX-License-Identifier: AGPL-3.0-only
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MockApiError } from '../../../../api/mock/scenarios';
import type { CanonKeeperApiClient, OperationRequest } from '../../../../shared/api';
import { createAppRoute } from '../../../../shared/navigation/app-route';
import { renderWithLink, returnAs, theApp, type TheApp } from '../../account/account.test-helpers';
import { noShell, shellOfTheWindow } from '../settings.test-helpers';
import { ConnectWindow } from './ConnectWindow';
import { HelperPart } from './HelperPart';

vi.mock('@tanstack/react-router', async (actual) => ({ ...await actual<object>(), useBlocker: vi.fn(() => ({ status: 'idle' })) }));

afterEach(() => {
  noShell();
  vi.restoreAllMocks();
});

const profile: NonNullable<OperationRequest<'updateAiSettings'>['own']> = {
  preset: 'deepseek', chatModel: 'deepseek-chat', cardsModel: 'deepseek-reasoner',
  advanced: { contextWindow: 64_000, maxOutput: 16_000, reasoning: 'high', parallel: 2, priceInPerMillion: 0.27, priceOutPerMillion: 1.1, currency: 'RUB' },
};

function renderHelper(api: TheApp) {
  renderWithLink(api, <HelperPart api={api as unknown as CanonKeeperApiClient} />);
}

/** «Откуда помощник»: the option by its name. */
function option(name: string) {
  return screen.getByRole('radio', { name }) as HTMLInputElement;
}

// specs/2026-11-15-desk-account-link-web.md AW3.1, AW3.5 (board 44·9): the part begins with the choice of the source; a choice
// sends the settings whole with the profile of «Свой ИИ» the server keeps; the helper of the account has nothing to set up.
describe('the choice of the helper in «Помощник»', () => {
  it('«Свой ИИ» → the account → «Свой ИИ»: the profile, its models, «Дополнительно» and prices live through both choices', async () => {
    shellOfTheWindow();
    const api = await theApp({ accountLinked: true });
    await api.updateAiSettings({ source: 'own', own: profile });
    const before = (await api.getAiSettings()).settings.own;
    const writes = vi.spyOn(api, 'updateAiSettings');
    renderHelper(api);
    await screen.findByRole('button', { name: /^Провайдер/u });
    expect(option('Свой ИИ').checked).toBe(true);
    fireEvent.click(option('Аккаунт CanonKeeper'));
    expect(await screen.findByText('Помощник CanonKeeper по вашему тарифу. Настраивать нечего. Обучение на ваших текстах выключено')).toBeTruthy();
    expect(await screen.findByText('Модель для чата: gpt-north-chat')).toBeTruthy();
    expect(screen.getByText('Модель для чтения книг: gpt-north-cards')).toBeTruthy();
    // Nothing to set up: no check of the link, no list of models, no provider.
    expect(screen.queryByRole('button', { name: 'Проверить связь' })).toBeNull();
    expect(screen.queryByRole('button', { name: /^Модель для чата/u })).toBeNull();
    expect(screen.queryByRole('button', { name: /^Провайдер/u })).toBeNull();
    expect(writes.mock.calls[0]![0]).toEqual({
      source: 'account',
      own: { preset: 'deepseek', chatModel: 'deepseek-chat', cardsModel: 'deepseek-reasoner', advanced: {
        contextWindow: 64_000, maxOutput: 16_000, reasoning: 'high', parallel: 2, priceInPerMillion: 0.27, priceOutPerMillion: 1.1,
        priceCachedInPerMillion: null, currency: 'RUB',
      } },
    });
    expect((await api.getAiSettings()).settings).toEqual({ source: 'account', own: before });

    fireEvent.click(option('Свой ИИ'));
    await waitFor(() => expect(writes).toHaveBeenCalledTimes(2));
    expect(writes.mock.calls[1]![0]).toEqual({ ...writes.mock.calls[0]![0], source: 'own' });
    expect((await api.getAiSettings()).settings).toEqual({ source: 'own', own: before });
    expect(await screen.findByRole('button', { name: /^Провайдер/u })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Проверить связь' })).toBeTruthy();
  });

  // AW3.1 with HW1.6: the unsaved edits of «Свой ИИ» do not go silently — the choice of the account asks first the question
  // of a way out with unsaved changes; «Отмена» changes nothing, «Не сохранять» lets the choice go.
  it('asks «Не сохранять?» before the account takes the place of unsaved edits of «Свой ИИ»', async () => {
    shellOfTheWindow();
    const api = await theApp({ accountLinked: true });
    await api.updateAiSettings({ source: 'own', own: profile });
    const writes = vi.spyOn(api, 'updateAiSettings');
    renderHelper(api);
    await screen.findByRole('button', { name: /^Провайдер/u });
    fireEvent.click(screen.getByRole('button', { name: 'Дополнительно' }));
    fireEvent.change(screen.getByLabelText('Окно контекста, токенов'), { target: { value: '32 000' } });

    fireEvent.click(option('Аккаунт CanonKeeper'));
    const question = await screen.findByRole('dialog', { name: 'Не сохранять?' });
    expect(within(question).getByText('Изменения настроек помощника не будут сохранены.')).toBeTruthy();
    expect(writes).not.toHaveBeenCalled();
    fireEvent.click(within(question).getByRole('button', { name: 'Отмена' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Не сохранять?' })).toBeNull());
    expect(option('Свой ИИ').checked).toBe(true);
    expect((screen.getByLabelText('Окно контекста, токенов') as HTMLInputElement).value).toBe('32 000');
    expect(writes).not.toHaveBeenCalled();

    fireEvent.click(option('Аккаунт CanonKeeper'));
    fireEvent.click(within(await screen.findByRole('dialog', { name: 'Не сохранять?' })).getByRole('button', { name: 'Не сохранять' }));
    expect(await screen.findByText('Модель для чата: gpt-north-chat')).toBeTruthy();
    // The choice sends the profile the server keeps: the edits the author let go are not saved.
    expect((writes.mock.calls[0]![0] as { own: { advanced: { contextWindow: number } } }).own.advanced.contextWindow).toBe(64_000);
    fireEvent.click(option('Свой ИИ'));
    await screen.findByRole('button', { name: /^Провайдер/u });
    fireEvent.click(screen.getByRole('button', { name: 'Дополнительно' }));
    expect((screen.getByLabelText('Окно контекста, токенов') as HTMLInputElement).value).toBe('64 000');
  });

  it('asks «Не сохранять?» before the account takes the place of a key typed and not saved', async () => {
    shellOfTheWindow();
    const api = await theApp({ accountLinked: true });
    await api.updateAiSettings({ source: 'own', own: profile });
    const writes = vi.spyOn(api, 'updateAiSettings');
    renderHelper(api);
    await screen.findByRole('button', { name: /^Провайдер/u });
    fireEvent.change(screen.getByLabelText('Ключ'), { target: { value: 'key-test-north-signal' } });
    fireEvent.click(option('Аккаунт CanonKeeper'));
    const question = await screen.findByRole('dialog', { name: 'Не сохранять?' });
    fireEvent.click(within(question).getByRole('button', { name: 'Отмена' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Не сохранять?' })).toBeNull());
    expect((screen.getByLabelText('Ключ') as HTMLInputElement).value).toBe('key-test-north-signal');
    expect(writes).not.toHaveBeenCalled();
  });

  // AW3.2: the author stands at «Настройки → Помощник» with an unsaved edit and a link ends with «Свой ИИ» — «Выбрать помощника
  // CanonKeeper» of the line of the end does not choose by itself: it brings the author to the choice of this part, which asks
  // its own «Не сохранять?». On any other page it chooses at once (the test of the observer of the link).
  it('brings the author to the choice of the part from the line of a link made, and chooses nothing behind the form', async () => {
    shellOfTheWindow();
    const api = await theApp();
    await api.updateAiSettings({ source: 'own', own: profile });
    await api.startAccountLink({ chooseHelper: false });
    const writes = vi.spyOn(api, 'updateAiSettings');
    renderWithLink(api, <HelperPart api={api as unknown as CanonKeeperApiClient} />, undefined, createAppRoute('/settings', { part: 'helper' }));
    await screen.findByRole('button', { name: /^Провайдер/u });
    fireEvent.click(screen.getByRole('button', { name: 'Дополнительно' }));
    fireEvent.change(screen.getByLabelText('Окно контекста, токенов'), { target: { value: '32 000' } });
    await new Promise((resolve) => setTimeout(resolve, 50));
    returnAs(api, 'linked');
    const line = await screen.findByText('Приложение связано с аккаунтом a…\u2060@example.test. Отвечает ваш помощник', undefined, { timeout: 4_000 });
    fireEvent.click(within(line.closest('[role="status"], [role="alert"]') as HTMLElement).getByRole('button', { name: 'Выбрать помощника CanonKeeper' }));
    await waitFor(() => expect(screen.getByRole('group', { name: 'Откуда помощник' }).contains(document.activeElement)).toBe(true));
    await waitFor(() => expect(screen.queryByText(/^Приложение связано с аккаунтом/u)).toBeNull());
    expect(writes).not.toHaveBeenCalled();
    expect((screen.getByLabelText('Окно контекста, токенов') as HTMLInputElement).value).toBe('32 000');
    // The choice of the part asks its question.
    fireEvent.click(option('Аккаунт CanonKeeper'));
    expect(await screen.findByRole('dialog', { name: 'Не сохранять?' })).toBeTruthy();
  }, 15_000);

  it('says a choice the server could not take — no connection, with «Повторить» — and keeps the source', async () => {
    shellOfTheWindow();
    const api = await theApp({ accountLinked: true, accountSilent: true });
    await api.updateAiSettings({ source: 'own', own: profile });
    renderHelper(api);
    await screen.findByRole('button', { name: /^Провайдер/u });
    fireEvent.click(option('Аккаунт CanonKeeper'));
    const refusal = await screen.findByText('Помощника CanonKeeper выбрать не получилось: нет связи с сервером CanonKeeper');
    await waitFor(() => expect(option('Свой ИИ').checked).toBe(true));
    expect((await api.getAiSettings()).settings.source).toBe('own');
    api.setScenario({ ...api.getScenario(), accountSilent: false });
    fireEvent.click(within(refusal.closest('[role="alert"]') as HTMLElement).getByRole('button', { name: 'Повторить' }));
    expect(await screen.findByText('Модель для чата: gpt-north-chat')).toBeTruthy();
    expect((await api.getAiSettings()).settings.source).toBe('account');
  });

  it('says a choice refused otherwise as a choice that did not happen', async () => {
    shellOfTheWindow();
    const api = await theApp({ accountLinked: true });
    await api.updateAiSettings({ source: 'own', own: profile });
    renderHelper(api);
    await screen.findByRole('button', { name: /^Провайдер/u });
    vi.spyOn(api, 'updateAiSettings').mockRejectedValueOnce(new MockApiError(500, 'the shell left', 'updateAiSettings', 'internal'));
    fireEvent.click(option('Аккаунт CanonKeeper'));
    expect(await screen.findByText('Помощника выбрать не получилось. Попробуйте ещё раз')).toBeTruthy();
    await waitFor(() => expect(option('Свой ИИ').checked).toBe(true));
  });

  it('reads the link again on helper.account_unlinked and shows «Связать с аккаунтом», which starts a link to choose it', async () => {
    shellOfTheWindow();
    const api = await theApp({ accountLinked: true });
    renderHelper(api);
    await screen.findByRole('button', { name: /^Провайдер/u });
    // The link went meanwhile — another window unlinked the app.
    await api.unlinkAccount();
    vi.spyOn(api, 'updateAiSettings').mockRejectedValueOnce(new MockApiError(409, 'the app is not linked with an account', 'updateAiSettings', 'helper.account_unlinked'));
    const started = vi.spyOn(api, 'startAccountLink');
    fireEvent.click(option('Аккаунт CanonKeeper'));
    fireEvent.click(await screen.findByRole('button', { name: 'Связать с аккаунтом' }));
    await waitFor(() => expect(started).toHaveBeenCalledWith({ chooseHelper: true }));
    expect(await screen.findByRole('dialog', { name: 'Подтвердите связь в браузере: там открылась страница CanonKeeper' })).toBeTruthy();
  });

  it('says «Применится после ответа помощника» only by the sign of the server, under the choice (board 44·9)', async () => {
    shellOfTheWindow();
    const api = await theApp({ accountLinked: true });
    await api.updateAiSettings({ source: 'account' });
    renderHelper(api);
    expect(await screen.findByText('Модель для чата: gpt-north-chat')).toBeTruthy();
    expect(screen.queryByText('Применится после ответа помощника')).toBeNull();
    const shown = await api.getAiSettings();
    vi.spyOn(api, 'getAiSettings').mockResolvedValue({ ...shown, appliesAfterAnswer: true });
    document.body.innerHTML = '';
    renderHelper(api);
    const applies = await screen.findByText('Применится после ответа помощника');
    const choice = screen.getByRole('group', { name: 'Откуда помощник' });
    expect(choice.compareDocumentPosition(applies) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(applies.compareDocumentPosition(screen.getByText('Помощник CanonKeeper по вашему тарифу. Настраивать нечего. Обучение на ваших текстах выключено')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  // Board 44·9 «Аккаунт, не связано»: the helper of the account of an app not linked says to link it first, beside the button.
  it('says «Сначала свяжите приложение с аккаунтом» beside «Связать с аккаунтом» when the app is not linked', async () => {
    shellOfTheWindow();
    const api = await theApp();
    await api.updateAiSettings({ source: 'own', own: profile });
    renderHelper(api);
    await screen.findByRole('button', { name: /^Провайдер/u });
    fireEvent.click(option('Аккаунт CanonKeeper'));
    const first = await screen.findByText('Сначала свяжите приложение с аккаунтом');
    const link = await screen.findByRole('button', { name: 'Связать с аккаунтом' });
    expect(first.compareDocumentPosition(link) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.queryByText('Помощник CanonKeeper по вашему тарифу. Настраивать нечего. Обучение на ваших текстах выключено')).toBeNull();
  });
});

// AW3.3, AW3.2 (board 44·4): «Подключить помощника» — two cards by the words of the build.
describe('the window «Подключить помощника»', () => {
  function renderWindow(api: TheApp, navigate = vi.fn(), onClose = vi.fn()) {
    renderWithLink(api, <ConnectWindow navigate={navigate} onClose={onClose} />);
    return { navigate, onClose };
  }

  // AW1.1: one window over the page — the link started from its card closes «Подключить помощника», and only the window of the
  // waiting stands; the card says the waiting when the author opens «Подключить помощника» again.
  it('starts a link to choose the helper of the account in place of itself, and its card says the waiting when opened again', async () => {
    shellOfTheWindow();
    const api = await theApp();
    const started = vi.spyOn(api, 'startAccountLink');
    function Page() {
      const [open, setOpen] = useState(true);
      return (
        <>
          <button onClick={() => setOpen(true)} type="button">Подключить помощника</button>
          {open ? <ConnectWindow navigate={vi.fn()} onClose={() => setOpen(false)} /> : null}
        </>
      );
    }
    renderWithLink(api, <Page />);
    const account = await screen.findByRole('region', { name: 'Аккаунт CanonKeeper' });
    expect(within(account).getByText('Помощник CanonKeeper по вашему тарифу. Настраивать нечего. Обучение на ваших текстах выключено')).toBeTruthy();
    expect(within(screen.getByRole('region', { name: 'Свой ИИ' })).getByText('Ключ и адрес вашего провайдера. Тариф не тратится')).toBeTruthy();
    fireEvent.click(await within(account).findByRole('button', { name: 'Связать с аккаунтом' }));
    await waitFor(() => expect(started).toHaveBeenCalledWith({ chooseHelper: true }));
    const waiting = await screen.findByRole('dialog', { name: 'Подтвердите связь в браузере: там открылась страница CanonKeeper' });
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Подключить помощника' })).toBeNull());
    expect(screen.getAllByRole('dialog')).toEqual([waiting]);
    fireEvent.click(within(waiting).getByRole('button', { name: 'Закрыть окно связи' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    fireEvent.click(screen.getByRole('button', { name: 'Подключить помощника' }));
    const again = await screen.findByRole('region', { name: 'Аккаунт CanonKeeper' });
    expect(await within(again).findByText('Ждёт подтверждения в браузере · Осталось 9 минут')).toBeTruthy();
    expect(within(again).getByRole('button', { name: 'Открыть окно связи' })).toBeTruthy();
  });

  it('chooses the helper of a linked app by «Выбрать», says a refusal with «Повторить», and closes once it answers', async () => {
    shellOfTheWindow();
    const api = await theApp({ accountLinked: true, accountSilent: true });
    await api.updateAiSettings({ source: 'own', own: profile });
    const { onClose } = renderWindow(api);
    const account = await screen.findByRole('region', { name: 'Аккаунт CanonKeeper' });
    fireEvent.click(await within(account).findByRole('button', { name: 'Выбрать' }));
    expect(await screen.findByText('Помощника CanonKeeper выбрать не получилось: нет связи с сервером CanonKeeper')).toBeTruthy();
    api.setScenario({ ...api.getScenario(), accountSilent: false });
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect((await api.getAiSettings()).settings).toMatchObject({ source: 'account', own: { preset: 'deepseek', chatModel: 'deepseek-chat' } });
  });

  it('opens «Настройки → Помощник» by «Настроить» of «Свой ИИ»', async () => {
    shellOfTheWindow();
    const { navigate, onClose } = renderWindow(await theApp());
    fireEvent.click(within(await screen.findByRole('region', { name: 'Свой ИИ' })).getByRole('button', { name: 'Настроить' }));
    expect(navigate).toHaveBeenCalledWith('/settings', { part: 'helper' });
    expect(onClose).toHaveBeenCalled();
  });
});
