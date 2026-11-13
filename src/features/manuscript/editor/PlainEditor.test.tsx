import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { MockApiError } from '../../../api/mock/scenarios';
import { createAppRoute } from '../../../shared/navigation/app-route';
import { readUnsaved } from '../../../shared/session/unsaved-copy';
import { plain } from '../../../shared/text/faces';
import { reasonOf } from '../../../test/disabled-reason';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { richFieldOf } from '../../../ui/rich-field';
import { PlainEditor } from './PlainEditor';

// specs/2026-11-13-desk-editor.md ED1, ED2: the editor of a chapter without the helper — the edit of a chapter of the book
// and a new chapter of a book.
const projectId = 'project-white-port';
const bookId = 'book-02';
const chapterId = 'chapter-12';
const author = 'user-local';
type Api = ReturnType<typeof createMockApiClient>;

vi.setConfig({ testTimeout: 20_000 });
beforeEach(() => window.localStorage.clear());
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

function editorApi() {
  const api = createMockApiClient();
  api.setScenario({ actorRole: 'editor', preset: 'normal' });
  return api;
}

function renderEditor(api: Api, query: Record<string, string>, { saveDelayMs = 30 }: { saveDelayMs?: number } = {}) {
  const navigate = vi.fn();
  function Harness() {
    const [shown, setShown] = useState<Record<string, string>>({ projectId, ...query });
    navigate.mockImplementation((path: string, next: Record<string, string | undefined> = {}) => {
      if (path === '/manuscript/edit') setShown(Object.fromEntries(Object.entries(next).filter((entry): entry is [string, string] => entry[1] !== undefined)));
    });
    return <PlainEditor api={api} author={author} navigate={navigate} route={createAppRoute('/manuscript/edit', new URLSearchParams(shown))} saveDelayMs={saveDelayMs} />;
  }
  const view = render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ToastsProvider><Harness /></ToastsProvider></QueryClientProvider>);
  return { navigate, ...view };
}

const textField = () => screen.getByRole('textbox', { name: 'Текст главы' });
const fill = (text: string) => act(() => { richFieldOf(textField())!.fill(text); });
/** The words of the field as the author reads them: the faces of the draft stay where the typing did not reach. */
const words = (text: string) => plain(text).text;
const saved = () => within(screen.getByRole('region', { name: 'Правка главы' })).getByRole('status');
const toasts = () => screen.getByRole('region', { name: 'Уведомления' });

async function openEdit(api: Api) {
  const draft = await api.openChapterEdit(projectId, chapterId);
  return draft;
}

describe('ED1 — the edit of a chapter of the book', () => {
  // ED1.2: the draft loads into its fields; a key saves 1,5 s later at the revision of the draft.
  it('loads the draft and saves what is typed 1,5 s after the last key, at its revision', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const api = editorApi();
    const draft = await openEdit(api);
    const update = vi.spyOn(api, 'updateChapterDraft');
    renderEditor(api, { draftId: draft.id }, { saveDelayMs: 1_500 });
    expect(await screen.findByRole('button', { name: /К главе в книге/u })).toBeTruthy();
    expect((screen.getByRole('textbox', { name: 'Название главы' }) as HTMLInputElement).value).toBe(draft.title);
    expect(textField().textContent).toContain(draft.text.split('\n')[0]!.replace(/[*\\]/gu, '').slice(0, 20));
    expect(saved().textContent).toMatch(/^Сохранено · \d\d:\d\d$/u);

    fill(`${words(draft.text)}\n\nСава ждал у причала.`);
    expect(saved().textContent).toBe('Не сохранено');
    await act(async () => { vi.advanceTimersByTime(1_000); });
    expect(update).not.toHaveBeenCalled();
    await act(async () => { vi.advanceTimersByTime(600); });
    await waitFor(() => expect(update).toHaveBeenCalledTimes(1));
    expect(update.mock.calls[0]![2]).toMatchObject({ expectedRevision: draft.revision, text: `${draft.text}\n\nСава ждал у причала.` });
    await waitFor(() => expect(saved().textContent).toMatch(/^Сохранено · \d\d:\d\d$/u));
  });

  // ED1.2: a refused save says «Не сохранено», the text stays in the field and in this browser; the window opened again has it.
  it('keeps a text a save refused and brings it back when the window opens again', async () => {
    const api = editorApi();
    const draft = await openEdit(api);
    const update = vi.spyOn(api, 'updateChapterDraft').mockRejectedValue(new MockApiError(400, 'Refused.', 'updateChapterDraft', 'request.validation'));
    const typed = `${draft.text}\n\nМара ударила в колокол.`;
    renderEditor(api, { draftId: draft.id });
    await screen.findByRole('button', { name: /К главе в книге/u });
    fill(`${words(draft.text)}\n\nМара ударила в колокол.`);
    await waitFor(() => expect(update).toHaveBeenCalled());
    await waitFor(() => expect(saved().textContent).toBe('Не сохранено'));
    expect(textField().textContent).toContain('Мара ударила в колокол.');
    expect(readUnsaved(author, draft.id)?.edits.text).toBe(typed);

    // The window goes: its last save is refused too, and the copy in this browser is all that holds the text.
    const tries = update.mock.calls.length;
    cleanup();
    await waitFor(() => expect(update.mock.calls.length).toBeGreaterThan(tries));
    await act(async () => { await Promise.resolve(); });
    expect(readUnsaved(author, draft.id)?.edits.text).toBe(typed);
    update.mockRestore();
    const save = vi.spyOn(api, 'updateChapterDraft');
    renderEditor(api, { draftId: draft.id });
    await waitFor(() => expect(textField().textContent).toContain('Мара ударила в колокол.'));
    await waitFor(() => expect(save).toHaveBeenCalledWith(projectId, draft.id, expect.objectContaining({ text: typed })));
    await waitFor(() => expect(readUnsaved(author, draft.id)).toBeNull());
  });

  // ED1.3: the main button is closed while the book holds the text, by the sign of the server; the replacement leads to the reader.
  it('closes «Заменить главу в книге» while the text is the book’s and replaces the chapter once it differs', async () => {
    const api = editorApi();
    const draft = await openEdit(api);
    const { navigate } = renderEditor(api, { draftId: draft.id });
    const replace = await screen.findByRole('button', { name: 'Заменить главу в книге' });
    expect(replace.getAttribute('aria-disabled')).toBe('true');
    expect(reasonOf(replace)).toBe('Текст такой же, как в книге: заменять нечего.');

    const update = vi.spyOn(api, 'updateChapterDraft');
    fill(`${words(draft.text)}\n\nСава ждал у причала.`);
    await waitFor(() => expect(update).toHaveBeenCalled());
    await waitFor(() => expect(screen.getByRole('button', { name: 'Заменить главу в книге' }).getAttribute('aria-disabled')).toBeNull());
    fireEvent.click(screen.getByRole('button', { name: 'Заменить главу в книге' }));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId, bookId, chapterId, mode: 'read' }, { replace: true }));
    expect(within(toasts()).getByText('Глава в книге обновлена')).toBeTruthy();
    expect(within(toasts()).getByRole('button', { name: 'Отменить' })).toBeTruthy();
    const chapter = await api.getChapter(chapterId, 'published');
    expect(chapter.paragraphs.at(-1)?.text).toBe('Сава ждал у причала.');
  });

  // ED1.3–ED1.5: «Отменить» of the replacement gives the book its text before, and the author's edit stays a version, as
  // before «Вернуть как было»: the server keeps none when it gives the text back.
  it('keeps the edit of the author as a version when «Отменить» gives the book its text before', async () => {
    const api = editorApi();
    const draft = await openEdit(api);
    renderEditor(api, { draftId: draft.id });
    await screen.findByRole('button', { name: 'Заменить главу в книге' });
    fill(`${words(draft.text)}\n\nСава ждал у причала.`);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Заменить главу в книге' }).getAttribute('aria-disabled')).toBeNull());
    fireEvent.click(screen.getByRole('button', { name: 'Заменить главу в книге' }));
    const revert = vi.spyOn(api, 'revertChapterFromDraft');
    fireEvent.click(await within(toasts()).findByRole('button', { name: 'Отменить' }));
    await waitFor(() => expect(revert).toHaveBeenCalled());
    await waitFor(async () => expect((await api.getChapter(chapterId, 'published')).paragraphs.at(-1)?.text).not.toBe('Сава ждал у причала.'));
    const versions = await api.listChapterDraftVersions(projectId, draft.id);
    expect(versions.versions[0]).toMatchObject({ reason: 'author_variant' });
    expect((await api.getChapterDraftVersion(projectId, draft.id, versions.versions[0]!.number)).text).toContain('Сава ждал у причала.');
  });

  // ED3.3: the draft moved on in another place — the refusal says the words of the site, and they are true: the draft is
  // read again, so the next press replaces the chapter.
  it('says a draft changed in another place in the words of the site and replaces the chapter at the next press', async () => {
    const api = editorApi();
    const draft = await openEdit(api);
    const { navigate } = renderEditor(api, { draftId: draft.id });
    await screen.findByRole('button', { name: 'Заменить главу в книге' });
    fill(`${words(draft.text)}\n\nСава ждал у причала.`);
    await waitFor(() => expect(saved().textContent).toMatch(/^Сохранено · \d\d:\d\d$/u));
    // Another window of the same author saves the draft: the revision this page holds is behind.
    const now = await api.getChapterDraft(projectId, draft.id);
    await api.updateChapterDraft(projectId, draft.id, { expectedRevision: now.revision, title: `${now.title}, ночь` });
    fireEvent.click(screen.getByRole('button', { name: 'Заменить главу в книге' }));
    expect(await within(toasts()).findByText('Главу только что изменили в другом месте. Нажмите ещё раз.')).toBeTruthy();
    expect(navigate).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Заменить главу в книге' }).getAttribute('aria-busy')).not.toBe('true'));
    await waitFor(() => expect((screen.getByRole('textbox', { name: 'Название главы' }) as HTMLInputElement).value).toBe(`${now.title}, ночь`));
    fireEvent.click(screen.getByRole('button', { name: 'Заменить главу в книге' }));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId, bookId, chapterId, mode: 'read' }, { replace: true }));
    expect((await api.getChapter(chapterId, 'published')).paragraphs.at(-1)?.text).toBe('Сава ждал у причала.');
  });

  // ED1.4: «⋯» — «Версии текста», «Вернуть как было», «Отказаться от правки…» last; the giving up asks the one question.
  it('has the three items of «⋯» in the order of the board and asks before giving the edit up', async () => {
    const api = editorApi();
    const opened = await openEdit(api);
    const edited = await api.updateChapterDraft(projectId, opened.id, { expectedRevision: opened.revision, text: `${opened.text}\n\nСава ждал у причала.` });
    await api.replaceChapterFromDraft(projectId, opened.id, { expectedRevision: edited.revision, title: edited.title });
    const { navigate } = renderEditor(api, { draftId: opened.id });
    fireEvent.click(await screen.findByRole('button', { name: 'Действия главы' }));
    const menu = await screen.findByRole('menu', { name: 'Действия главы' });
    expect(within(menu).getAllByRole('menuitem').map((item) => item.textContent)).toEqual(['Версии текста', 'Вернуть как было', 'Отказаться от правки…']);

    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Отказаться от правки…' }));
    const question = await screen.findByRole('dialog', { name: /^Отказаться от правки главы «.+»\?$/u });
    expect(within(question).getByText('Вернуть будет нельзя.')).toBeTruthy();
    expect(within(question).getByText('Глава в книге останется.')).toBeTruthy();
    const remove = vi.spyOn(api, 'deleteChapterDraft');
    fireEvent.click(within(question).getByRole('button', { name: 'Отказаться от правки' }));
    await waitFor(() => expect(remove).toHaveBeenCalledWith(projectId, opened.id, expect.any(Number)));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId, bookId, chapterId, mode: 'read' }, { replace: true }));
  });

  it('shows the versions of the text and gives one back', async () => {
    const api = editorApi();
    const opened = await openEdit(api);
    // The author's text kept before the chapter was opened again: «Ваша прежняя правка».
    await api.updateChapterDraft(projectId, opened.id, { expectedRevision: opened.revision, variant: { text: `${opened.text}\n\nПрежний абзац.` } });
    renderEditor(api, { draftId: opened.id });
    fireEvent.click(await screen.findByRole('button', { name: 'Действия главы' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Версии текста' }));
    const window = await screen.findByRole('dialog', { name: 'Версии текста' });
    const row = await within(window).findByRole('button', { name: /Сегодня, \d\d:\d\d.*Ваша прежняя правка/u });
    fireEvent.click(row);
    await waitFor(() => expect(within(window).getByRole('textbox', { name: /^Текст главы/u }).textContent).toContain('Прежний абзац.'));
    const take = vi.spyOn(api, 'takeChapterDraftVersion');
    fireEvent.click(within(window).getByRole('button', { name: 'Вернуть эту версию' }));
    await waitFor(() => expect(take).toHaveBeenCalledWith(projectId, opened.id, expect.any(Number), expect.objectContaining({ whole: true })));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Версии текста' })).toBeNull());
    await waitFor(() => expect(textField().textContent).toContain('Прежний абзац.'));
  });

  // ED1.5: the refusal of a chapter changed in the book: the line with «Открыть заново»; the text in the field stays, and
  // «Открыть заново» keeps it as a version before the text of the book takes its place.
  it('says the chapter changed in the book, keeps the text and opens the chapter again', async () => {
    const api = editorApi();
    const draft = await openEdit(api);
    vi.spyOn(api, 'replaceChapterFromDraft').mockRejectedValue(new MockApiError(409, 'The chapter changed.', 'replaceChapterFromDraft', 'chapter_draft.chapter_changed'));
    renderEditor(api, { draftId: draft.id });
    await screen.findByRole('button', { name: 'Заменить главу в книге' });
    fill(`${words(draft.text)}\n\nСава ждал у причала.`);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Заменить главу в книге' }).getAttribute('aria-disabled')).toBeNull());
    fireEvent.click(screen.getByRole('button', { name: 'Заменить главу в книге' }));
    expect(await screen.findByText('Глава в книге изменилась с тех пор, как вы её открыли')).toBeTruthy();
    expect(textField().textContent).toContain('Сава ждал у причала.');
    expect(reasonOf(screen.getByRole('button', { name: 'Заменить главу в книге' }))).toBe('Глава в книге изменилась — откройте её заново.');

    const reopen = vi.spyOn(api, 'reopenChapterFromBook');
    fireEvent.click(screen.getByRole('button', { name: 'Открыть заново' }));
    await waitFor(() => expect(reopen).toHaveBeenCalled());
    const versions = await api.listChapterDraftVersions(projectId, draft.id);
    expect(versions.versions[0]).toMatchObject({ reason: 'author_variant' });
    const kept = await api.getChapterDraftVersion(projectId, draft.id, versions.versions[0]!.number);
    expect(kept.text).toContain('Сава ждал у причала.');
    await waitFor(() => expect(screen.queryByText('Глава в книге изменилась с тех пор, как вы её открыли')).toBeNull());
  });

  // ED1.6: no action of the helper, no tab, plan, finding or price; the marks of what the model wrote stay as they are.
  it('has no helper and keeps the marks of a text written by the model', async () => {
    const api = editorApi();
    const opened = await openEdit(api);
    const marked = await api.updateChapterDraft(projectId, opened.id, { expectedRevision: opened.revision, text: `${opened.text}\n\n⟦Ночь стояла тихая.⟧` });
    const update = vi.spyOn(api, 'updateChapterDraft');
    renderEditor(api, { draftId: opened.id });
    await waitFor(() => expect(textField().querySelector('[data-written]')?.textContent).toBe('Ночь стояла тихая.'));
    expect(screen.queryByRole('tablist')).toBeNull();
    expect(screen.queryByText(/помощник|Помощник|Замысел|Замечания|План|₽|\$/u)).toBeNull();
    act(() => { textField().focus(); });
    fireEvent.change(screen.getByRole('textbox', { name: 'Название главы' }), { target: { value: 'Белый порт, ночь' } });
    await waitFor(() => expect(update).toHaveBeenCalled());
    expect(update.mock.calls[0]![2]).toEqual({ expectedRevision: marked.revision, title: 'Белый порт, ночь' });
    expect((await api.getChapterDraft(projectId, opened.id)).text).toBe(marked.text);
  });
});

describe('ED2 — a new chapter of a book', () => {
  // ED2.2: the way in writes nothing down; the first save does, after the last chapter by default; the same item opens it again.
  it('writes the new chapter down by its first save and opens it again by the same item', async () => {
    const api = editorApi();
    const create = vi.spyOn(api, 'createChapterDraft');
    const { navigate } = renderEditor(api, { bookId });
    const add = await screen.findByRole('button', { name: 'Добавить в книгу' });
    expect(add.getAttribute('aria-disabled')).toBe('true');
    expect(reasonOf(add)).toBe('Сначала напишите текст главы.');
    const chapters = (await api.listChapters(bookId)).data.filter((item) => item.status === 'published').sort((left, right) => left.order - right.order);
    const last = chapters.at(-1)!;
    expect(screen.getByRole('button', { name: /^Место в книге/u }).textContent).toContain(`После главы ${last.displayNumber}`);
    expect(create).not.toHaveBeenCalled();
    expect((await api.listChapterDrafts(projectId)).data).toHaveLength(0);

    fireEvent.change(screen.getByRole('textbox', { name: 'Название главы' }), { target: { value: 'Северная пристань' } });
    fill('Мара первой заметила дым.');
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    expect(create.mock.calls[0]![1]).toMatchObject({ kind: 'sketch', place: { bookId, afterChapterId: last.id } });
    const made = await create.mock.results[0]!.value;
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/edit', { projectId, draftId: made.id }, { replace: true }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Добавить в книгу' }).getAttribute('aria-disabled')).toBeNull());
    await waitFor(async () => expect((await api.getChapterDraft(projectId, made.id))).toMatchObject({ title: 'Северная пристань', text: 'Мара первой заметила дым.' }));

    cleanup();
    const again = renderEditor(api, { bookId });
    await waitFor(() => expect(again.navigate).toHaveBeenCalledWith('/manuscript/edit', { projectId, draftId: made.id }, { replace: true }));
    expect(await screen.findByDisplayValue('Северная пристань')).toBeTruthy();
    expect(create).toHaveBeenCalledTimes(1);
  });

  // ED2.3, ED2.4: «Добавить в книгу» on the place chosen leads to the new chapter in the reader; «⋯» has one item.
  it('adds the chapter to the book and has one item in «⋯»', async () => {
    const api = editorApi();
    const { navigate } = renderEditor(api, { bookId });
    await screen.findByRole('button', { name: 'Добавить в книгу' });
    fireEvent.click(screen.getByRole('button', { name: 'Действия главы' }));
    expect(within(await screen.findByRole('menu', { name: 'Действия главы' })).getAllByRole('menuitem').map((item) => item.textContent)).toEqual(['Отказаться от главы…']);
    fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' });

    fireEvent.change(screen.getByRole('textbox', { name: 'Название главы' }), { target: { value: 'Северная пристань' } });
    fill('Мара первой заметила дым.');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Добавить в книгу' }).getAttribute('aria-disabled')).toBeNull());
    const add = vi.spyOn(api, 'addChapterDraftToBook');
    fireEvent.click(screen.getByRole('button', { name: 'Добавить в книгу' }));
    await waitFor(() => expect(add).toHaveBeenCalled());
    const added = await add.mock.results[0]!.value;
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId, bookId, chapterId: added.added.chapterId, mode: 'read' }, { replace: true }));
    expect(within(toasts()).getByText('Глава добавлена в книгу')).toBeTruthy();
    expect((await api.listChapters(bookId)).data.some((item) => item.title === 'Северная пристань')).toBe(true);
  });
});
