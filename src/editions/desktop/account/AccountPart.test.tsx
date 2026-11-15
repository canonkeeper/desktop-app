// SPDX-License-Identifier: AGPL-3.0-only
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MockApiError } from '../../../api/mock/scenarios';
import type { CanonKeeperApiClient, OperationResponse } from '../../../shared/api';
import { EraseData } from '../settings/EraseData';
import { noShell, shellOfTheWindow } from '../settings/settings.test-helpers';
import { AccountPart } from './AccountPart';
import { renderWithLink, theApp, type TheApp } from './account.test-helpers';

afterEach(() => {
  noShell();
  vi.restoreAllMocks();
});

type Account = OperationResponse<'getAccount', '200'>;

const intro = 'С аккаунтом CanonKeeper помощник отвечает на вопросы по книгам и читает их — по вашему тарифу. Настраивать нечего. Книги лежат на этом компьютере';
const time = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit' });

function renderPart(api: TheApp) {
  renderWithLink(api, <AccountPart api={api as unknown as CanonKeeperApiClient} />);
}

/** The part over an answer of getAccount the test shapes from the mock's. */
async function renderAnswer(api: TheApp, shape: (account: Account) => Account) {
  const answer = shape(await api.getAccount());
  vi.spyOn(api, 'getAccount').mockImplementation(async () => structuredClone(answer));
  renderPart(api);
  return answer;
}

// specs/2026-11-15-desk-account-link-web.md AW2.1, AW2.4 (board 44·6): what the account gives in this build, «Связать с
// аккаунтом», what goes through the server; a build that cannot be linked; the waiting; a link that failed.
describe('«Аккаунт» not linked', () => {
  it('says what the account gives, «Связать с аккаунтом» and what goes through the server', async () => {
    shellOfTheWindow();
    renderPart(await theApp());
    expect(await screen.findByText(intro)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Связать с аккаунтом' })).toBeTruthy();
    const server = screen.getByRole('button', { name: 'Что проходит через сервер' });
    expect(server.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(server);
    expect(screen.getByRole('note').textContent).toBe('Через сервер CanonKeeper к помощнику проходят: разговор чата с каждым вопросом — ваши вопросы, ответы помощника и места книг, которые он прочитал, — и приложенные главы; при чтении книг — их текст; ваши материалы, когда вы их загружаете. Книги, главы и чаты лежат на этом компьютере');
    // The build without the workshops promises none of them.
    expect(document.body.textContent).not.toMatch(/Письм|Рисун|голос/u);
  });

  it('says a build that cannot be linked, without the button', async () => {
    shellOfTheWindow();
    const api = await theApp();
    vi.spyOn(api, 'startAccountLink').mockRejectedValue(new MockApiError(409, 'this build cannot be linked', 'startAccountLink', 'account.link_unsupported'));
    renderPart(api);
    fireEvent.click(await screen.findByRole('button', { name: 'Связать с аккаунтом' }));
    expect(await screen.findByText('Эту сборку CanonKeeper нельзя связать с аккаунтом')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Связать с аккаунтом' })).toBeNull();
  });

  it('stands the waiting in the place of the button', async () => {
    shellOfTheWindow();
    const api = await theApp();
    await api.startAccountLink({ chooseHelper: false });
    renderPart(api);
    expect(await screen.findByText('Ждёт подтверждения в браузере · Осталось 9 минут')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Открыть окно связи' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Связать с аккаунтом' })).toBeNull();
  });

  it('says a link that failed with its reason and «Связать снова»', async () => {
    shellOfTheWindow();
    const api = await theApp({ accountPassStore: 'file' });
    await api.startAccountLink({ chooseHelper: false });
    api.setScenario({ ...api.getScenario(), accountLinkOutcome: 'keyring' });
    await api.getAccount();
    renderPart(api);
    expect(await screen.findByText(intro)).toBeTruthy();
    expect(screen.getByRole('note').textContent).toBe('Связь не сохранилась: не получилось записать её в папку данных');
    expect(screen.getByRole('button', { name: 'Связать снова' })).toBeTruthy();
  });

  it('says a broken link with «Связать снова»', async () => {
    shellOfTheWindow();
    renderPart(await theApp({ accountBroken: true }));
    expect(await screen.findByText('Связь с аккаунтом прервана. Так бывает после смены пароля, «Выйти везде» или отвязки на сайте')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Связать снова' })).toBeTruthy();
  });
});

// AW2.2, AW2.3 (board 44·7): who, the tier with its term or without, the questions of today when the site told them, the
// reading of 30 days, when the site told it, this computer on the site, where the link lies, the pages of the site.
describe('«Аккаунт» linked', () => {
  it('says what the site told, every word by the server', async () => {
    const invoke = shellOfTheWindow();
    const api = await theApp({ accountLinked: true, accountTierUntil: '2026-12-14T09:00:00.000Z' });
    renderPart(api);
    expect(await screen.findByText('Автор Севера · a…\u2060@example.test')).toBeTruthy();
    expect(screen.getByText('Тариф «Писатель» до 14 декабря')).toBeTruthy();
    expect(screen.getByText('Вопросы сегодня: 4 из 30')).toBeTruthy();
    expect(screen.getByText('Чтение книг за 30 дней: использовано 3,2 из 40 млн знаков')).toBeTruthy();
    const at = (await api.getAccount()).account!.at;
    expect(screen.getByText(`Сведения от ${time.format(new Date(at))}`)).toBeTruthy();
    expect(screen.getByText('Этот компьютер на сайте: «Рабочий стол»')).toBeTruthy();
    expect(screen.getByText('Связь с аккаунтом лежит в хранилище паролей системы')).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/пропуск/u);
    fireEvent.click(screen.getByRole('link', { name: 'Тарифы' }));
    await waitFor(() => expect(invoke).toHaveBeenCalledWith('open_external', { url: 'https://canonkeeper.example.test/tariffs' }));
    fireEvent.click(screen.getByRole('link', { name: 'Приложения на компьютере' }));
    await waitFor(() => expect(invoke).toHaveBeenCalledWith('open_external', { url: 'https://canonkeeper.example.test/account#apps' }));
  });

  it('says a tier without a term, no questions line without questions, the link in the file and the information of another day', async () => {
    shellOfTheWindow();
    const api = await theApp({ accountLinked: true, accountQuestionsUnknown: true, accountPassStore: 'file' });
    const answer = await renderAnswer(api, (account) => ({
      ...account, account: { ...account.account!, tier: { code: 'free', title: 'Бесплатно', until: null }, at: '2026-11-13T11:05:00.000Z' },
    }));
    expect(await screen.findByText('Тариф «Бесплатно»')).toBeTruthy();
    expect(screen.queryByText(/Вопросы сегодня/u)).toBeNull();
    expect(screen.getByText(`Сведения от 13 ноября, ${time.format(new Date(answer.account!.at))}`)).toBeTruthy();
    expect(screen.getByText('Связь с аккаунтом лежит в файле в папке данных: в системе нет хранилища паролей')).toBeTruthy();
  });

  it('changes its words when a value of the server changes', async () => {
    shellOfTheWindow();
    const api = await theApp({ accountLinked: true });
    await renderAnswer(api, (account) => ({ ...account, account: { ...account.account!, questions: { used: 12, limit: 30 }, reading: { used: 39_000_000, limit: 40_000_000 } } }));
    expect(await screen.findByText('Вопросы сегодня: 12 из 30')).toBeTruthy();
    expect(screen.getByText('Чтение книг за 30 дней: использовано 39 из 40 млн знаков')).toBeTruthy();
  });

  it('keeps what the site told with «Нет связи…» and «Обновить сведения», which asks the site now', async () => {
    shellOfTheWindow();
    const api = await theApp({ accountLinked: true, accountUnreachable: true });
    const asked = vi.spyOn(api, 'getAccount');
    renderPart(api);
    const at = (await api.getAccount()).account!.at;
    expect(await screen.findByText(`Нет связи с сервером CanonKeeper. Сведения от ${time.format(new Date(at))}`)).toBeTruthy();
    expect(screen.getByText('Автор Севера · a…\u2060@example.test')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Обновить сведения' }));
    await waitFor(() => expect(asked).toHaveBeenCalledWith({ fresh: true }));
  });

  it('says the link without what the site told — after a restart without the network, or right after the link', async () => {
    shellOfTheWindow();
    for (const unreachable of [true, false]) {
      const api = await theApp({ accountLinked: true, accountInfoMissing: true, accountUnreachable: unreachable });
      renderPart(api);
      expect(await screen.findByText('Приложение связано. Сведения об аккаунте появятся, когда будет связь с сервером')).toBeTruthy();
      expect(screen.getByRole('button', { name: 'Обновить сведения' })).toBeTruthy();
      cleanup();
    }
  });
});

// AW1.5 (board 44·8, 44·12): «Отвязать приложение…» asks first; the line says whether the site heard it, and when it did
// not — «Приложения на компьютере»; «Стереть все данные» of a linked app names the unlinking.
describe('unlinking and erasing a linked app', () => {
  it('asks, unlinks and says the site heard it', async () => {
    shellOfTheWindow();
    const api = await theApp({ accountLinked: true });
    renderPart(api);
    fireEvent.click(await screen.findByRole('button', { name: 'Действия связи с аккаунтом' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Отвязать приложение…' }));
    const question = screen.getByRole('dialog', { name: 'Отвязать приложение от аккаунта?' });
    expect(within(question).getByText('Помощник CanonKeeper перестанет отвечать и читать книги, пока вы не свяжете приложение снова. Книги и всё, что помощник уже узнал о них, останутся на компьютере')).toBeTruthy();
    expect(within(question).getByRole('button', { name: 'Отмена' })).toBeTruthy();
    fireEvent.click(within(question).getByRole('button', { name: 'Отвязать' }));
    expect(await screen.findByText('Приложение отвязано от аккаунта')).toBeTruthy();
    expect(await screen.findByRole('button', { name: 'Связать с аккаунтом' })).toBeTruthy();
  });

  it('says the site did not hear it, with «Приложения на компьютере»', async () => {
    const invoke = shellOfTheWindow();
    const api = await theApp({ accountLinked: true, unlinkSiteSilent: true });
    renderPart(api);
    fireEvent.click(await screen.findByRole('button', { name: 'Действия связи с аккаунтом' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Отвязать приложение…' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Отвязать' }));
    const line = await screen.findByText('Приложение отвязано на этом компьютере. Сайт об этом не узнал: в «Приложениях на компьютере» оно может остаться — отвяжите его там');
    fireEvent.click(within(line.closest('[role="status"], [role="alert"]') as HTMLElement).getByRole('link', { name: 'Приложения на компьютере' }));
    await waitFor(() => expect(invoke).toHaveBeenCalledWith('open_external', { url: 'https://canonkeeper.example.test/account#apps' }));
  });

  // No refusal of an action is silent: an unlinking that failed keeps its question open and says so.
  it('keeps the question open with its words when the unlinking failed', async () => {
    shellOfTheWindow();
    const api = await theApp({ accountLinked: true });
    vi.spyOn(api, 'unlinkAccount').mockRejectedValueOnce(new MockApiError(500, 'the shell left', 'unlinkAccount', 'internal'));
    renderPart(api);
    fireEvent.click(await screen.findByRole('button', { name: 'Действия связи с аккаунтом' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Отвязать приложение…' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Отвязать' }));
    const question = await screen.findByRole('dialog', { name: 'Отвязать приложение от аккаунта?' });
    expect(await within(question).findByText('Приложение не отвязалось. Попробуйте ещё раз')).toBeTruthy();
    // Again from the same question: it unlinks.
    fireEvent.click(within(question).getByRole('button', { name: 'Отвязать' }));
    expect(await screen.findByText('Приложение отвязано от аккаунта')).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('names the unlinking in the question of the erasure of a linked app only', async () => {
    shellOfTheWindow();
    const erase = 'Приложение будет отвязано от аккаунта CanonKeeper. Если сервер не ответит, отвяжите его в «Приложениях на компьютере» на сайте';
    for (const linked of [true, false]) {
      const api = await theApp({ accountLinked: linked });
      renderWithLink(api, <EraseData api={api as unknown as CanonKeeperApiClient} />);
      fireEvent.click(screen.getByRole('button', { name: 'Стереть все данные…' }));
      const question = await screen.findByRole('dialog', { name: 'Стереть все данные CanonKeeper на этом компьютере?' });
      if (linked) expect(await within(question).findByText(erase)).toBeTruthy();
      else {
        await new Promise((resolve) => setTimeout(resolve, 50));
        expect(within(question).queryByText(erase)).toBeNull();
      }
      cleanup();
    }
  });
});
