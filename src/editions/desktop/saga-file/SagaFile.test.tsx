// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiStatusError, NetworkApiError, type CanonKeeperApiClient, type DownloadedFile, type Project } from '../../../shared/api';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import type { SagaArchiveOpened } from '../../../entities/saga-file/api';
import { SagaFileSection } from './SagaFileSection';
import { SagaOpened } from './SagaOpened';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const saga = (id: string, title: string, bookCount: number): Project => ({
  id, title, description: null, bookCount, chapterCount: 0, wordCount: 0, createdAt: '2026-11-01T10:00:00Z', updatedAt: '2026-11-01T10:00:00Z',
  currentMembership: { id: `member-${id}`, projectId: id, userId: 'local', role: 'owner', status: 'active', createdAt: '2026-11-01T10:00:00Z' },
} as unknown as Project);

const whitePort = saga('project-1', 'Хроники Белого порта', 13);
const northWind = saga('project-2', 'Северный ветер', 3);
const southFront = saga('project-3', 'Южный рубеж', 1);

const file = (name: string): DownloadedFile => ({ blob: new Blob(['PK']), fileName: name });

function problem(status: number, code: string) {
  return new ApiStatusError(status, { type: 'about:blank', title: 'refused', status, code, requestId: 'req_synthetic_1' }, new Response(null, { status }));
}

/** The answer of the server to the opening of a file: a saga of the file and what it brought. */
const opened: SagaArchiveOpened = {
  projectId: 'project-9', name: 'Хроники Белого порта — из файла от 13 ноября', renamed: true,
  report: {
    books: 1, chapters: 4, cards: 4, chaptersWithoutCards: { count: 0, places: [] }, namesFresh: true, threads: false,
    materials: 0, memory: 0, notes: 0, chats: 2, chatsNotRead: [], coversSkipped: [],
    leftOut: { drafts: 0, drawings: 0, trashedBooks: 0, trashedChapters: 0, othersTurns: 0 }, sections: [],
  },
};

/** The file of the saga goes to the browser: the address of the bytes and the press of the link are recorded. */
function savingInBrowser() {
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: vi.fn(() => 'blob:saga'), revokeObjectURL: vi.fn() }));
  return vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);
}

/** The sagas of the computer as the server lists them, and what a test answers besides. */
function apiOf(sagas: Project[], client: Partial<CanonKeeperApiClient>) {
  return {
    listProjects: vi.fn(async () => ({ data: sagas, meta: { hasMore: false, nextCursor: null } })),
    getProject: vi.fn(async (id: string) => sagas.find((each) => each.id === id)!),
    listBooks: vi.fn(async () => ({ data: [], meta: { hasMore: false, nextCursor: null } })),
    listProjectJobs: vi.fn(async () => ({ data: [] })),
    ...client,
  } as unknown as CanonKeeperApiClient;
}

/** «Настройки» the author leaves and comes back to: the part goes from the page, the notices of the app stay. */
function Leaving({ api }: { api: CanonKeeperApiClient }) {
  const [shown, setShown] = useState(true);
  return (
    <>
      <button onClick={() => setShown((now) => !now)} type="button">{shown ? 'Уйти из настроек' : 'Вернуться в настройки'}</button>
      {shown ? <SagaFileSection api={api} /> : null}
    </>
  );
}

/** «Файл саги» of «Данные» over the sagas of the computer, with the window «Сага открыта» of the notices of the app. */
function renderSection(sagas: Project[], client: Partial<CanonKeeperApiClient>, { leaving = false } = {}) {
  const navigate = vi.fn();
  const api = apiOf(sagas, client);
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <ToastsProvider>
        {leaving ? <Leaving api={api} /> : <SagaFileSection api={api} />}
        <SagaOpened navigate={navigate} />
      </ToastsProvider>
    </QueryClientProvider>,
  );
  return { navigate };
}

const leave = () => fireEvent.click(screen.getByRole('button', { name: 'Уйти из настроек' }));
const comeBack = () => fireEvent.click(screen.getByRole('button', { name: 'Вернуться в настройки' }));

const toasts = () => screen.getByRole('region', { name: 'Уведомления' });
/** A button closed by its reason stays in its place: `aria-disabled`, not `disabled` (PR1.1). */
const closed = (button: HTMLElement) => button.getAttribute('aria-disabled') === 'true';
const saveButton = () => screen.getByRole('button', { name: 'Сохранить сагу в файл…' });
const openButton = () => screen.getByRole('button', { name: 'Открыть файл саги…' });
const picker = () => document.querySelector<HTMLInputElement>('input[type="file"][accept=".cksaga"]')!;
const choose = (name: string) => fireEvent.change(picker(), { target: { files: [new File(['PK'], name)] } });

// specs/2026-11-13-desk-sagafile-web.md SW1.1, SW1.2 (board 44·11, 44·30).
describe('«Сохранить сагу в файл…»', () => {
  it('saves the one saga at once, under the name of the server, and says where the file went', async () => {
    const press = savingInBrowser();
    const getSagaArchive = vi.fn(async () => file('Хроники Белого порта.cksaga'));
    renderSection([whitePort], { getSagaArchive });
    await waitFor(() => expect(saveButton().getAttribute('aria-busy')).toBeNull());
    expect(screen.getByText('В файле — книги, всё, что помощник знает о саге, ваши записи и расшифровки чатов. Его можно открыть на другом компьютере')).toBeTruthy();
    fireEvent.click(saveButton());
    expect(await within(toasts()).findByText('Сага сохранена в файл: Загрузки → Хроники Белого порта.cksaga')).toBeTruthy();
    expect(getSagaArchive).toHaveBeenCalledWith('project-1', expect.objectContaining({ signal: expect.any(AbortSignal) }));
    expect(press).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('asks which of several sagas, the first chosen, and saves the one chosen', async () => {
    savingInBrowser();
    const getSagaArchive = vi.fn(async () => file('Северный ветер.cksaga'));
    renderSection([whitePort, northWind, southFront], { getSagaArchive });
    await waitFor(() => expect(saveButton().getAttribute('aria-busy')).toBeNull());
    fireEvent.click(saveButton());
    const dialog = await screen.findByRole('dialog', { name: 'Какую сагу сохранить в файл?' });
    const choices = within(dialog).getAllByRole('radio');
    expect(choices.map((choice) => choice.closest('label')?.textContent)).toEqual(['Хроники Белого порта13 книг', 'Северный ветер3 книги', 'Южный рубеж1 книга']);
    expect((choices[0] as HTMLInputElement).checked).toBe(true);
    fireEvent.click(choices[1]!);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Сохранить в файл' }));
    await waitFor(() => expect(getSagaArchive).toHaveBeenCalledWith('project-2', expect.anything()));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(await within(toasts()).findByText('Сага сохранена в файл: Загрузки → Северный ветер.cksaga')).toBeTruthy();
  });

  it('stands as a long work with «Остановить» while the file goes, the button closed with its reason; «Остановить» breaks it off quietly', async () => {
    savingInBrowser();
    let signal: AbortSignal | undefined;
    const getSagaArchive = vi.fn((_projectId: string, options?: { signal?: AbortSignal }) => new Promise<DownloadedFile>((_resolve, reject) => {
      signal = options?.signal;
      signal?.addEventListener('abort', () => reject(new NetworkApiError(new DOMException('stopped', 'AbortError'))));
    }));
    renderSection([whitePort], { getSagaArchive });
    await waitFor(() => expect(saveButton().getAttribute('aria-busy')).toBeNull());
    fireEvent.click(saveButton());
    const work = await screen.findByRole('region', { name: 'Сохраняю сагу в файл' });
    expect(within(work).getByText('До нескольких минут: в файл идут и ваши чаты')).toBeTruthy();
    expect(closed(saveButton())).toBe(true);
    expect(screen.getByText('Сага уже сохраняется')).toBeTruthy();
    fireEvent.click(saveButton());
    expect(getSagaArchive).toHaveBeenCalledTimes(1);

    fireEvent.click(within(work).getByRole('button', { name: 'Остановить' }));
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Сохраняю сагу в файл' })).toBeNull());
    expect(signal?.aborted).toBe(true);
    expect(closed(saveButton())).toBe(false);
    expect(within(toasts()).queryAllByRole('status')).toEqual([]);
    expect(within(toasts()).queryAllByRole('alert')).toEqual([]);
  });

  it('says a refusal and a broken file with «Повторить», which asks the same saga again', async () => {
    savingInBrowser();
    const getSagaArchive = vi.fn()
      .mockRejectedValueOnce(problem(500, 'server.error'))
      .mockRejectedValueOnce(new NetworkApiError(new TypeError('network error')))
      .mockResolvedValueOnce(file('Хроники Белого порта.cksaga'));
    renderSection([whitePort], { getSagaArchive });
    await waitFor(() => expect(saveButton().getAttribute('aria-busy')).toBeNull());
    fireEvent.click(saveButton());
    const failed = await within(toasts()).findByRole('alert');
    expect(failed.textContent).toContain('Не удалось сохранить сагу в файл. Попробуйте ещё раз');
    fireEvent.click(within(failed).getByRole('button', { name: 'Повторить' }));
    await waitFor(() => expect(getSagaArchive).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(within(toasts()).getAllByRole('alert')).toHaveLength(2));
    fireEvent.click(within(within(toasts()).getAllByRole('alert')[1]!).getByRole('button', { name: 'Повторить' }));
    expect(await within(toasts()).findByText('Сага сохранена в файл: Загрузки → Хроники Белого порта.cksaga')).toBeTruthy();
    expect(getSagaArchive.mock.calls.map(([projectId]) => projectId)).toEqual(['project-1', 'project-1', 'project-1']);
  });

  it('says a save of the author that goes on already by the answer 429', async () => {
    savingInBrowser();
    renderSection([whitePort], { getSagaArchive: vi.fn().mockRejectedValue(problem(429, 'rate_limited')) });
    await waitFor(() => expect(saveButton().getAttribute('aria-busy')).toBeNull());
    fireEvent.click(saveButton());
    expect(await within(toasts()).findByText('Сага уже сохраняется')).toBeTruthy();
  });

  it('stays in its place with its reason before the first saga', async () => {
    renderSection([], { getSagaArchive: vi.fn() });
    await waitFor(() => expect(closed(saveButton())).toBe(true));
    expect(screen.getByText('Саги ещё нет — сохранять нечего')).toBeTruthy();
  });

  it('reads the list of sagas again when it did not read and saves by it; a list that fails again says so with «Повторить»', async () => {
    savingInBrowser();
    const listProjects = vi.fn()
      .mockRejectedValueOnce(new NetworkApiError(new TypeError('network error')))
      .mockRejectedValueOnce(new NetworkApiError(new TypeError('network error')))
      .mockResolvedValue({ data: [whitePort], meta: { hasMore: false, nextCursor: null } });
    const getSagaArchive = vi.fn(async () => file('Хроники Белого порта.cksaga'));
    renderSection([whitePort], { getSagaArchive, listProjects });
    await waitFor(() => expect(listProjects).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(saveButton().getAttribute('aria-busy')).toBeNull());
    // Not a press that does nothing: it reads the list again, and says so when the list does not come.
    fireEvent.click(saveButton());
    const failed = await within(toasts()).findByRole('alert');
    expect(failed.textContent).toContain('Не удалось прочитать список саг');
    expect(failed.textContent).toContain('Нет связи с сервером');
    expect(getSagaArchive).not.toHaveBeenCalled();
    fireEvent.click(within(failed).getByRole('button', { name: 'Повторить' }));
    expect(await within(toasts()).findByText('Сага сохранена в файл: Загрузки → Хроники Белого порта.cksaga')).toBeTruthy();
    expect(getSagaArchive).toHaveBeenCalledWith('project-1', expect.anything());
    expect(listProjects).toHaveBeenCalledTimes(3);
  });
});

// SW1.2, SW2.2: the author leaves «Настройки» while the file goes — the work does not break off, its end comes after him.
describe('a saga file while the author is away from «Настройки»', () => {
  it('saves on: the part back sees the save going, the line of its end comes when the part is gone', async () => {
    savingInBrowser();
    let give: (value: DownloadedFile) => void = () => undefined;
    const getSagaArchive = vi.fn(() => new Promise<DownloadedFile>((resolve) => { give = resolve; }));
    renderSection([whitePort], { getSagaArchive }, { leaving: true });
    await waitFor(() => expect(saveButton().getAttribute('aria-busy')).toBeNull());
    fireEvent.click(saveButton());
    await screen.findByRole('region', { name: 'Сохраняю сагу в файл' });
    leave();
    comeBack();
    expect(await screen.findByRole('region', { name: 'Сохраняю сагу в файл' })).toBeTruthy();
    expect(closed(saveButton())).toBe(true);
    leave();
    await act(async () => give(file('Хроники Белого порта.cksaga')));
    expect(await within(toasts()).findByText('Сага сохранена в файл: Загрузки → Хроники Белого порта.cksaga')).toBeTruthy();
    expect(getSagaArchive).toHaveBeenCalledTimes(1);
  });

  it('opens on: the window «Сага открыта» comes when the part is gone', async () => {
    let answer: (value: SagaArchiveOpened) => void = () => undefined;
    renderSection([whitePort], { openSagaArchive: vi.fn(() => new Promise<SagaArchiveOpened>((resolve) => { answer = resolve; })) }, { leaving: true });
    choose('Хроники Белого порта.cksaga');
    await screen.findByRole('region', { name: 'Открываю файл саги: Хроники Белого порта.cksaga' });
    leave();
    await act(async () => answer(opened));
    expect(await screen.findByRole('dialog', { name: 'Сага открыта' })).toBeTruthy();
  });

  it('a broken opening says so when the part is gone, and «Повторить» of its line sends the same key', async () => {
    let fail: (error: unknown) => void = () => undefined;
    const openSagaArchive = vi.fn()
      .mockImplementationOnce(() => new Promise((_resolve, reject) => { fail = reject; }))
      .mockResolvedValueOnce(opened);
    renderSection([whitePort], { openSagaArchive }, { leaving: true });
    choose('Хроники Белого порта.cksaga');
    await screen.findByRole('region', { name: 'Открываю файл саги: Хроники Белого порта.cksaga' });
    leave();
    await act(async () => fail(new NetworkApiError(new TypeError('network error'))));
    const line = await within(toasts()).findByRole('alert');
    expect(line.textContent).toContain('Не удалось открыть файл саги');
    fireEvent.click(within(line).getByRole('button', { name: 'Повторить' }));
    expect(await screen.findByRole('dialog', { name: 'Сага открыта' })).toBeTruthy();
    const keys = openSagaArchive.mock.calls.map(([, options]) => (options as { idempotencyKey: string }).idempotencyKey);
    expect(keys).toHaveLength(2);
    expect(keys[1]).toBe(keys[0]);
  });
});

// specs/2026-11-13-desk-sagafile-web.md SW2.2, SW2.3, SW2.4 (board 44·31, 44·32).
describe('«Открыть файл саги…»', () => {
  it('opens the chosen file as a long work without «Остановить», then the window «Сага открыта» leads to the saga', async () => {
    let answer: (value: SagaArchiveOpened) => void = () => undefined;
    const openSagaArchive = vi.fn(() => new Promise<SagaArchiveOpened>((resolve) => { answer = resolve; }));
    const { navigate } = renderSection([whitePort], { openSagaArchive });
    expect(picker().accept).toBe('.cksaga');
    choose('Хроники Белого порта.cksaga');
    const work = await screen.findByRole('region', { name: 'Открываю файл саги: Хроники Белого порта.cksaga' });
    expect(within(work).getByText('До минуты')).toBeTruthy();
    expect(within(work).queryByRole('button')).toBeNull();
    expect(closed(openButton())).toBe(true);
    expect(screen.getByText('Файл уже открывается')).toBeTruthy();

    await act(async () => answer(opened));
    const window = await screen.findByRole('dialog', { name: 'Сага открыта' });
    expect(within(window).getByText('Хроники Белого порта — из файла от 13 ноября: 1 книга, 4 главы. Помощник уже знает эту сагу — читать книги заново не нужно')).toBeTruthy();
    expect(within(window).getByText('Сага с таким названием уже есть — эта названа „Хроники Белого порта — из файла от 13 ноября“')).toBeTruthy();
    fireEvent.click(within(window).getByRole('button', { name: 'Перейти к саге' }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId: 'project-9' });
    expect(screen.queryByRole('dialog', { name: 'Сага открыта' })).toBeNull();
    expect(screen.queryByRole('region', { name: /Открываю файл саги/u })).toBeNull();
  });

  it('leaves the author where he is when the window is closed', async () => {
    const { navigate } = renderSection([whitePort], { openSagaArchive: vi.fn(async () => opened) });
    choose('Хроники Белого порта.cksaga');
    const window = await screen.findByRole('dialog', { name: 'Сага открыта' });
    fireEvent.click(within(window).getByRole('button', { name: 'Закрыть' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Сага открыта' })).toBeNull());
    expect(navigate).not.toHaveBeenCalled();
  });

  it('keeps every note in a body of its own that scrolls, «Перейти к саге» in its place', async () => {
    const all: SagaArchiveOpened = {
      ...opened,
      report: {
        ...opened.report, chaptersWithoutCards: { count: 5, places: ['Книга 1, гл. 1 „Туманный берег“'] }, chatsNotRead: ['Кто убил капитана', 'Хронология осады'],
        coversSkipped: ['Белый порт'], leftOut: { drafts: 3, drawings: 12, trashedBooks: 1, trashedChapters: 4, othersTurns: 14 },
        sections: ['workshops/writing/drafts.jsonl'],
      },
    };
    renderSection([whitePort], { openSagaArchive: vi.fn(async () => all) });
    choose('Хроники Белого порта.cksaga');
    const window = await screen.findByRole('dialog', { name: 'Сага открыта' });
    const body = window.querySelector('ul[tabindex="0"]')!;
    expect(within(body as HTMLElement).getAllByRole('listitem').filter((item) => item.parentElement === body)).toHaveLength(8);
    expect(within(body as HTMLElement).getByText('Книга 1, гл. 1 „Туманный берег“')).toBeTruthy();
    expect(body.nextElementSibling?.querySelector('button')?.textContent).toBe('Перейти к саге');
    expect(window.textContent).not.toMatch(/¶|b\d+c\d+/u);
  });

  it('says each refusal by its code in its words, «Написать нам» with a file with an error', async () => {
    const refusals: [ApiStatusError, string][] = [
      [problem(422, 'saga_archive.not_archive'), 'Это не файл саги. Файл саги называется как сага и кончается на .cksaga'],
      [problem(422, 'saga_archive.damaged'), 'Файл саги повреждён или скачан не до конца. Сохраните сагу в файл ещё раз и откройте новый'],
      [problem(422, 'saga_archive.newer_version'), 'Этот файл сохранён более новой версией CanonKeeper. Обновите приложение и откройте файл снова'],
      [problem(413, 'saga_archive.too_large'), 'Файл больше 1 ГБ — такой файл саги CanonKeeper не открывает'],
      [problem(507, 'saga_archive.no_space'), 'На диске не хватает места, чтобы открыть файл саги. Освободите место и откройте файл снова'],
      [problem(422, 'saga_archive.invalid'), 'В файле саги ошибка, и CanonKeeper не может его открыть'],
      [problem(429, 'rate_limited'), 'Файл уже открывается'],
    ];
    const openSagaArchive = vi.fn();
    for (const [refusal] of refusals) openSagaArchive.mockRejectedValueOnce(refusal);
    renderSection([whitePort], { openSagaArchive });
    const said: { words: string; action: string }[] = [];
    for (const [, words] of refusals) {
      choose('Хроники Белого порта.cksaga');
      const line = (await within(toasts()).findByText(words)).closest('[data-tone]') as HTMLElement;
      // The one action of the line besides closing it: «Написать нам» of a file with an error.
      said.push({ words, action: [...line.querySelectorAll('a, button')].map((each) => each.textContent).filter((text) => text !== '').join() });
    }
    expect(said.filter(({ action }) => action !== '')).toEqual([{ words: 'В файле саги ошибка, и CanonKeeper не может его открыть', action: 'Написать нам' }]);
  });

  it('says a refusal it has no words for as the general line with the code for support', async () => {
    renderSection([whitePort], { openSagaArchive: vi.fn().mockRejectedValue(problem(500, 'server.error')) });
    choose('Хроники Белого порта.cksaga');
    const line = await within(toasts()).findByRole('alert');
    expect(line.textContent).toContain('Не удалось открыть файл саги');
    expect(line.textContent).toContain('Код для поддержки: req_synthetic_1');
    expect(within(line).getByText('Написать нам')).toBeTruthy();
  });

  it('repeats a broken opening with the same key and opens a new choice with a new one', async () => {
    const openSagaArchive = vi.fn()
      .mockRejectedValueOnce(new NetworkApiError(new TypeError('network error')))
      .mockResolvedValueOnce(opened)
      .mockResolvedValueOnce(opened);
    renderSection([whitePort], { openSagaArchive });
    choose('Хроники Белого порта.cksaga');
    const broken = await within(toasts()).findByRole('alert');
    expect(broken.textContent).toContain('Не удалось открыть файл саги');
    fireEvent.click(within(broken).getByRole('button', { name: 'Повторить' }));
    fireEvent.click(within(await screen.findByRole('dialog', { name: 'Сага открыта' })).getByRole('button', { name: 'Закрыть' }));
    choose('Хроники Белого порта.cksaga');
    await waitFor(() => expect(openSagaArchive).toHaveBeenCalledTimes(3));
    const keys = openSagaArchive.mock.calls.map(([, options]) => (options as { idempotencyKey: string }).idempotencyKey);
    expect(keys[0]).toMatch(/^openSagaArchive:/u);
    expect(keys[1]).toBe(keys[0]);
    expect(keys[2]).not.toBe(keys[0]);
  });
});
