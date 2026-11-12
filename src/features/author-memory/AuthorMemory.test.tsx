import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { MockApiError } from '../../api/mock/scenarios';
import { createIdempotencyKey } from '../../entities/author-memory/api';
import { projectId } from '../../api/mock/repository/fixtures';
import { createAppRoute } from '../../shared/navigation/app-route';
import { AuthorMemory } from './AuthorMemory';

type Api = ReturnType<typeof createMockApiClient>;

afterEach(cleanup);

function renderMemory(api: Api, { canEdit = true, query = {} }: { canEdit?: boolean; query?: Record<string, string> } = {}) {
  const navigate = vi.fn();
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <AuthorMemory api={api} canEdit={canEdit} navigate={navigate} projectId={projectId} route={createAppRoute('/memory', { projectId, ...query })} />
    </QueryClientProvider>,
  );
  return navigate;
}

async function addRecord(api: Api, text: string, kind: 'plan' | 'correction' = 'plan') {
  return api.createAuthorMemory(projectId, { kind, text }, { idempotencyKey: createIdempotencyKey('createAuthorMemory') });
}

function fillAndSave(text: string) {
  fireEvent.change(within(screen.getByRole('dialog')).getByLabelText('Текст'), { target: { value: text } });
  fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Сохранить' }));
}

describe('AuthorMemory (R4.5)', () => {
  it('says the memory is empty and how it fills', async () => {
    renderMemory(createMockApiClient());
    expect(await screen.findByText('Пока ничего нет')).toBeTruthy();
    expect(screen.getByText(/Скажите помощнику в чате/)).toBeTruthy();
  });

  it('adds a record, and what is listed comes from the server', async () => {
    const api = createMockApiClient();
    const create = vi.spyOn(api, 'createAuthorMemory');
    renderMemory(api);
    fireEvent.click(await screen.findByRole('button', { name: 'Записать самому' }));
    fillAndSave('  Кира узнает правду  ');
    const list = await screen.findByRole('list', { name: 'Записи' });
    expect(within(list).getByText('Кира узнает правду')).toBeTruthy();
    expect(within(list).getByText('План')).toBeTruthy();
    expect(create.mock.calls[0]?.[1]).toEqual({ kind: 'plan', text: 'Кира узнает правду' });
    expect(create.mock.calls[0]?.[2].idempotencyKey).toBeTruthy();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('keeps one Idempotency-Key while the same record is sent again and says what failed', async () => {
    const api = createMockApiClient();
    const create = vi.spyOn(api, 'createAuthorMemory');
    create.mockRejectedValueOnce(new Error('network'));
    renderMemory(api);
    fireEvent.click(await screen.findByRole('button', { name: 'Записать самому' }));
    fillAndSave('Кира узнает правду');
    expect(await within(screen.getByRole('dialog')).findByText('Не удалось сохранить запись.')).toBeTruthy();
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Сохранить' }));
    await screen.findByRole('list', { name: 'Записи' });
    expect(create.mock.calls[1]?.[2].idempotencyKey).toBe(create.mock.calls[0]?.[2].idempotencyKey);
  });

  /** Chooses an option of a select of the dialog by the names the author reads. */
  async function choose(select: string, option: string) {
    const trigger = within(screen.getByRole('dialog')).getByRole('button', { name: new RegExp(`^${select} `, 'u') });
    await waitFor(() => expect(trigger.hasAttribute('disabled')).toBe(false));
    fireEvent.click(trigger);
    fireEvent.click(await screen.findByRole('option', { name: option }));
  }

  /** MR1.1: «Выбрать главу» opens the contents of the saga; the chapter is chosen in its book by its place. */
  async function chooseChapter(book: string, chapter: string) {
    const dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: /^(Выбрать главу|Другая глава)$/u }));
    const books = await waitFor(() => {
      const trigger = within(dialog).getAllByRole('button', { name: /^Книга /u }).find((button) => button.getAttribute('aria-haspopup') === 'listbox');
      expect(trigger?.hasAttribute('disabled')).toBe(false);
      return trigger!;
    });
    fireEvent.click(books);
    fireEvent.click(await screen.findByRole('option', { name: book }));
    fireEvent.click(await within(dialog).findByRole('button', { name: chapter }));
  }

  // specs/2026-11-10-ux1-names.md N5.2: the chapter is chosen among the chapters of the saga by its place; the author
  // neither sees nor types its key.
  it('says when the chosen chapter has left the saga meanwhile', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'createAuthorMemory').mockRejectedValueOnce(new MockApiError(422, 'No such chapter.', 'createAuthorMemory', 'author_memory.no_chapter'));
    renderMemory(api);
    fireEvent.click(await screen.findByRole('button', { name: 'Записать самому' }));
    const dialog = screen.getByRole('dialog');
    await chooseChapter('Книга II. Карта приливов', 'Книга 2, гл. 12 „Белый порт“');
    fillAndSave('Кира узнает правду');
    expect(await within(dialog).findByText('Этой главы в саге уже нет. Выберите другую.')).toBeTruthy();
    expect(dialog.textContent).not.toMatch(/b\d+c\d+/u);
  });

  it('changes a record at the revision it saw, with its chapter and name', async () => {
    const api = createMockApiClient();
    await addRecord(api, 'Кира узнает правду');
    const update = vi.spyOn(api, 'updateAuthorMemory');
    renderMemory(api);
    fireEvent.click(await screen.findByRole('button', { name: 'Действия записи' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Изменить' }));
    const dialog = screen.getByRole('dialog');
    await chooseChapter('Книга II. Карта приливов', 'Книга 2, гл. 12 „Белый порт“');
    // The chosen chapter stands by its place.
    expect(within(dialog).getByText('Книга 2, гл. 12 „Белый порт“')).toBeTruthy();
    fireEvent.change(within(dialog).getByRole('combobox', { name: /^Имя/u }), { target: { value: 'Кира' } });
    fillAndSave('Кира узнает всё');
    const list = await screen.findByRole('list', { name: 'Записи' });
    await within(list).findByText('Кира узнает всё');
    // N5.1, MR1.2: the record names its chapter by its place, never by its key, as a link into the reader.
    const place = within(list).getByRole('link', { name: 'Книга 2, гл. 12 „Белый порт“' });
    expect(place.getAttribute('href')).toMatch(/^\/manuscript\/books\?.*chapterId=/u);
    expect(within(list).getByText('Кира')).toBeTruthy();
    expect(list.textContent).not.toMatch(/b\d+c\d+/u);
    expect(update.mock.calls[0]?.[2]).toEqual({ kind: 'plan', text: 'Кира узнает всё', chapter: 'b2c12', name: 'Кира', expectedRevision: 1 });
  });

  // specs/2026-11-10-ux2-patterns-actions.md PA3.1, PA3.2: «Удалить запись…» last in «⋯», after a line, and the question
  // says that a record does not come back.
  it('removes a record only after asking', async () => {
    const api = createMockApiClient();
    await addRecord(api, 'Кира узнает правду');
    renderMemory(api);
    const openDelete = async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Действия записи' }));
      const items = within(screen.getByRole('menu', { name: 'Действия записи' })).getAllByRole('menuitem');
      expect(items.map((item) => item.textContent)).toEqual(['Изменить', 'Удалить запись…']);
      expect(items[1]!.previousElementSibling?.hasAttribute('data-delete-rule')).toBe(true);
      fireEvent.click(items[1]!);
      return screen.getByRole('dialog', { name: 'Удалить запись «Кира узнает правду»?' });
    };
    fireEvent.click(within(await openDelete()).getByRole('button', { name: 'Отмена' }));
    expect(screen.getByText('Кира узнает правду')).toBeTruthy();
    const dialog = await openDelete();
    expect(dialog.textContent).toContain('Вернуть будет нельзя.');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить запись' }));
    expect(await screen.findByText('Пока ничего нет')).toBeTruthy();
  });

  // MR1.1: «Убрать» takes the chapter off; the search finds a chapter of any book by its title.
  it('finds a chapter by its title and takes it off', async () => {
    const api = createMockApiClient();
    const create = vi.spyOn(api, 'createAuthorMemory');
    renderMemory(api);
    fireEvent.click(await screen.findByRole('button', { name: 'Записать самому' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Выбрать главу' }));
    fireEvent.change(within(dialog).getByRole('searchbox', { name: 'Найти главу по названию' }), { target: { value: 'Белый' } });
    fireEvent.click(await within(dialog).findByRole('button', { name: 'Книга 2, гл. 12 „Белый порт“' }, { timeout: 2000 }));
    expect(within(dialog).queryByRole('searchbox', { name: 'Найти главу по названию' })).toBeNull();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Убрать' }));
    expect(within(dialog).getByText('Не выбрана — запись о всей саге')).toBeTruthy();
    fillAndSave('Кира узнает правду');
    await screen.findByRole('list', { name: 'Записи' });
    expect(create.mock.calls[0]?.[1]).toEqual({ kind: 'plan', text: 'Кира узнает правду' });
  });

  // MR1.4: a correction says what it stands over; the kinds are calm marks, none red or yellow.
  it('says what a correction stands over and marks the kinds calmly', async () => {
    const api = createMockApiClient();
    await addRecord(api, 'Ей двадцать лет', 'correction');
    renderMemory(api);
    const list = await screen.findByRole('list', { name: 'Записи' });
    expect(within(list).getByText('Поправка').className).not.toMatch(/warning|error/u);
    fireEvent.click(screen.getByRole('button', { name: 'Записать самому' }));
    await choose('Что это', 'Поправка');
    expect(within(screen.getByRole('dialog')).getByText(/Важнее того, что помощник сам вывел из книги, но не самого текста книги/u)).toBeTruthy();
  });

  // MR1.2, MR1.5: a gone chapter is said without a link; each record says where it came from; the author is named only in a
  // saga of several members.
  it('says where a record came from and that its chapter is gone', async () => {
    const api = createMockApiClient();
    const at = new Date().toISOString();
    const base = { revision: 1, author: 'Мира Волкова', createdAt: at, updatedAt: at };
    vi.spyOn(api, 'listAuthorMemory').mockResolvedValue({
      data: [
        { ...base, id: 'r1', kind: 'plan', text: 'Кира узнает правду', origin: { kind: 'chat', chatId: 'chat-1', title: 'Что Мара знает' } },
        { ...base, id: 'r2', kind: 'plan', text: 'Брат вернётся', chapterGone: true, origin: { kind: 'chat' } },
        { ...base, id: 'r3', kind: 'style_rule', text: 'Без наречий', origin: { kind: 'manual' } },
        { ...base, id: 'r4', kind: 'rejected', text: 'Без драконов', origin: { kind: 'draft', title: 'Белый порт' } },
      ],
      meta: { hasMore: false, nextCursor: null, totalCount: 4 }, shared: false,
    });
    const navigate = renderMemory(api);
    const list = await screen.findByRole('list', { name: 'Записи' });
    const chat = within(list).getByRole('link', { name: '„Что Мара знает“' });
    expect(chat.closest('p')?.textContent).toMatch(/^Из чата „Что Мара знает“ · \d+ [а-я]+$/u);
    fireEvent.click(chat);
    expect(navigate).toHaveBeenCalledWith('/chat', { projectId, chatId: 'chat-1' });
    expect(within(list).getByText('глава удалена')).toBeTruthy();
    expect(within(list).getAllByText(/^Из чата/u).map((line) => line.textContent?.replace(/ · .*/u, ''))).toEqual(['Из чата „Что Мара знает“', 'Из чата']);
    expect(within(list).getByText(/^Добавлено вручную · /u)).toBeTruthy();
    expect(within(list).getByText(/^Из разговора с помощником главы „Белый порт“ · /u)).toBeTruthy();
    expect(list.textContent).not.toContain('Мира Волкова');
    cleanup();
    vi.spyOn(api, 'listAuthorMemory').mockResolvedValue({
      data: [{ ...base, id: 'r5', kind: 'plan', text: 'Кира узнает правду', origin: { kind: 'manual' } }],
      meta: { hasMore: false, nextCursor: null, totalCount: 1 }, shared: true,
    });
    renderMemory(api);
    expect(await screen.findByText(/^Добавлено вручную · .* · Мира Волкова$/u)).toBeTruthy();
  });

  it('a reader reads and is offered nothing to change', async () => {
    const api = createMockApiClient();
    await addRecord(api, 'Кира узнает правду');
    renderMemory(api, { canEdit: false });
    expect(await screen.findByText('Кира узнает правду')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Записать самому' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Действия записи' })).toBeNull();
  });

  // specs/2026-11-12-ux12-shell.md SH4.3 (W-82, П-22): «владелец саги», the word of the line of the role.
  it('a reader without records is told who adds them', async () => {
    renderMemory(createMockApiClient(), { canEdit: false });
    expect(await screen.findByText('Записи появятся, когда владелец саги их добавит.')).toBeTruthy();
  });

  it('the filters live in the URL', async () => {
    const api = createMockApiClient();
    await addRecord(api, 'Кира узнает правду');
    await addRecord(api, 'Ей двадцать лет', 'correction');
    const navigate = renderMemory(api, { query: { kind: 'correction' } });
    expect(await screen.findByText('Ей двадцать лет')).toBeTruthy();
    expect(screen.queryByText('Кира узнает правду')).toBeNull();
    fireEvent.change(screen.getByRole('searchbox', { name: 'Найти в записях' }), { target: { value: 'двадцать' } });
    fireEvent.submit(screen.getByRole('search'));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/memory', { projectId, kind: 'correction', q: 'двадцать' }, { replace: true }));
  });

  it('says nothing is found under the filters', async () => {
    const api = createMockApiClient();
    await addRecord(api, 'Кира узнает правду');
    renderMemory(api, { query: { q: 'дракон' } });
    expect(await screen.findByText('Ничего не найдено')).toBeTruthy();
  });

  it('offers to try again when the memory does not load', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'listAuthorMemory').mockRejectedValueOnce(new Error('network'));
    renderMemory(api);
    fireEvent.click(await screen.findByRole('button', { name: 'Повторить' }));
    expect(await screen.findByText('Пока ничего нет')).toBeTruthy();
  });
});
