import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { ApiStatusError, NetworkApiError } from '../../shared/api';
import { noteQueryKeys } from '../../entities/notes/api';
import { createAppRoute } from '../../shared/navigation/app-route';
import { useNotes } from '../../shared/notes/notes-context';
import { NotesProvider } from './NotesProvider';

vi.mock('@tanstack/react-router', () => ({ useBlocker: vi.fn() }));

function Controls() {
  const notes = useNotes()!;
  return <><button onClick={notes.open}>Открыть заметки</button><input aria-label="Черновик чата" defaultValue="не потерять" /><button onClick={() => notes.capture({ body: 'Фрагмент ответа' })}>Сохранить фрагмент</button></>;
}
function setup(api = createMockApiClient()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const navigate = vi.fn();
  render(<QueryClientProvider client={client}><NotesProvider accountId="user-mira" api={api} navigate={navigate} route={createAppRoute('/chat')}><Controls /></NotesProvider></QueryClientProvider>);
  return { api, navigate, client };
}

describe('unified Notes', () => {
  it('preserves a draft across refetch and locks the form while saving', async () => {
    const api = createMockApiClient();
    const entry = (await api.listNotes()).data.find((item) => item.sourceKind === 'chapter')!;
    const original = await api.getPersonalNote(entry.id);
    const { client } = setup(api);
    fireEvent.click(screen.getByText('Открыть заметки'));
    fireEvent.click(await screen.findByRole('option', { name: (name) => name.includes(entry.preview) }));
    const input = await screen.findByLabelText('Текст заметки') as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: 'Мой незавершённый текст' } });
    await act(async () => { client.setQueryData(noteQueryKeys.note('user-mira', entry.id), { ...original, body: 'Новый серверный текст' }); });
    expect(input.value).toBe('Мой незавершённый текст');
    const update = api.updatePersonalNote.bind(api);
    let finish!: () => void;
    const gate = new Promise<void>((resolve) => { finish = resolve; });
    vi.spyOn(api, 'updatePersonalNote').mockImplementation(async (...args) => { await gate; return update(...args); });
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(input.disabled).toBe(true));
    expect((screen.getByRole('button', { name: 'Отмена' }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.keyDown(screen.getByRole('dialog', { name: 'Заметки' }), { key: 'Escape' });
    expect(screen.getByRole('dialog', { name: 'Заметки' })).toBeTruthy();
    expect(screen.queryByRole('dialog', { name: 'Не сохранять?' })).toBeNull();
    await act(async () => finish());
    await screen.findByText('Заметка сохранена');
  });

  it('retains a failed delete confirmation, never treats 404 as success, and blocks duplicate pending deletes', async () => {
    const api = createMockApiClient();
    const entry = (await api.listNotes()).data.find((item) => item.sourceKind === 'chapter')!;
    const remove = vi.spyOn(api, 'deletePersonalNote').mockRejectedValueOnce(new ApiStatusError(404,
      { type: 'about:blank', title: 'Not found', status: 404, code: 'not_found', requestId: 'note-delete' }, new Response(null, { status: 404 })));
    setup(api);
    fireEvent.click(screen.getByText('Открыть заметки'));
    fireEvent.click(await screen.findByRole('option', { name: (name) => name.includes(entry.preview) }));
    fireEvent.click(await screen.findByRole('button', { name: 'Удалить заметку' }));
    const dialog = await screen.findByRole('dialog', { name: 'Удалить заметку?' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить' }));
    const retry = await within(dialog).findByRole('button', { name: 'Повторить' });
    expect(screen.queryByText('Заметка удалена')).toBeNull();
    let finish!: () => void;
    remove.mockReturnValueOnce(new Promise<void>((resolve) => { finish = resolve; }));
    fireEvent.click(retry);
    await waitFor(() => expect((within(dialog).getByRole('button', { name: 'Удаляем…' }) as HTMLButtonElement).disabled).toBe(true));
    fireEvent.keyDown(dialog, { key: 'Escape' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Назад' }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Удаляем…' }));
    expect(screen.getByRole('dialog', { name: 'Удалить заметку?' })).toBeTruthy();
    expect(remove).toHaveBeenCalledTimes(2);
    await act(async () => finish());
    await screen.findByText('Заметка удалена');
  });

  it('guards dirty text before returning to the chapter', async () => {
    const api = createMockApiClient();
    const entry = (await api.listNotes()).data.find((item) => item.sourceKind === 'chapter')!;
    const { navigate } = setup(api);
    fireEvent.click(screen.getByText('Открыть заметки'));
    fireEvent.click(await screen.findByRole('option', { name: (name) => name.includes(entry.preview) }));
    fireEvent.change(await screen.findByLabelText('Текст заметки'), { target: { value: 'Несохранённая правка' } });
    fireEvent.click(screen.getByRole('button', { name: 'К фрагменту главы' }));
    const confirm = await screen.findByRole('dialog', { name: 'Не сохранять?' });
    expect(navigate).not.toHaveBeenCalled();
    fireEvent.click(within(confirm).getByRole('button', { name: 'Назад' }));
    expect((screen.getByLabelText('Текст заметки') as HTMLTextAreaElement).value).toBe('Несохранённая правка');
  });

  it('keeps a failed update in the form and deletes only after confirmation', async () => {
    const api = createMockApiClient();
    const entry = (await api.listNotes()).data.find((item) => item.sourceKind === 'chapter')!;
    const remove = vi.spyOn(api, 'deletePersonalNote');
    vi.spyOn(api, 'updatePersonalNote').mockRejectedValueOnce(new NetworkApiError(new Error('offline')));
    setup(api);
    fireEvent.click(screen.getByText('Открыть заметки'));
    fireEvent.click(await screen.findByRole('option', { name: (name) => name.includes(entry.preview) }));
    fireEvent.change(await screen.findByLabelText('Текст заметки'), { target: { value: 'Не потерять эту правку' } });
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
    await screen.findByRole('button', { name: 'Повторить' });
    expect((screen.getByLabelText('Текст заметки') as HTMLTextAreaElement).value).toBe('Не потерять эту правку');
    expect(screen.queryByText('Заметка сохранена')).toBeNull();
    fireEvent.click(screen.getByLabelText('Удалить заметку'));
    const confirm = await screen.findByRole('dialog', { name: 'Удалить заметку?' });
    expect(remove).not.toHaveBeenCalled();
    fireEvent.click(within(confirm).getByRole('button', { name: 'Удалить' }));
    await screen.findByText('Заметка удалена');
    expect(remove).toHaveBeenCalledWith(entry.id, 1);
    expect((await api.listNotes()).data.some((item) => item.id === entry.id)).toBe(false);
  });

  it('does not dismiss a captured draft when the source navigation menu closes', () => {
    const api = createMockApiClient();
    const client = new QueryClient();
    const view = (menu?: string) => <QueryClientProvider client={client}><NotesProvider accountId="user-mira" api={api} navigate={vi.fn()} route={createAppRoute('/manuscript/books', { sourceView: 'draft', menu })}><Controls /></NotesProvider></QueryClientProvider>;
    const { rerender } = render(view('open'));
    fireEvent.click(screen.getByText('Сохранить фрагмент'));
    rerender(view());
    expect((screen.getByLabelText('Текст заметки') as HTMLTextAreaElement).value).toBe('Фрагмент ответа');
    expect(screen.getByRole('dialog', { name: 'Заметки' })).toBeTruthy();
  });

  it('shows chapter notes with free notes and opens the exact source paragraph', async () => {
    const api = createMockApiClient();
    await api.createPersonalNote({ body: 'Произвольная запись' }, { idempotencyKey: 'idem-notes-free' });
    const chapterNote = (await api.listNotes()).data.find((entry) => entry.sourceKind === 'chapter')!;
    const detail = await api.getPersonalNote(chapterNote.id);
    if (detail.source?.kind !== 'chapter') throw new Error('missing chapter note fixture');
    const { locator } = detail.source;
    const { navigate } = setup(api);
    fireEvent.click(screen.getByText('Открыть заметки'));
    expect(await screen.findByRole('option', { name: /Произвольная запись/ })).toBeTruthy();
    fireEvent.click(screen.getByRole('option', { name: new RegExp(chapterNote.chapterTitle!) }));
    expect(navigate).not.toHaveBeenCalled();
    fireEvent.click(await screen.findByRole('button', { name: 'К фрагменту главы' }));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/manuscript/books', expect.objectContaining({
      chapterId: locator.chapterId, paragraphId: locator.paragraphId,
    })));
  });

  it('keeps captured text and composer draft; retries an ambiguous create with one key', async () => {
    const api = createMockApiClient();
    const create = api.createPersonalNote.bind(api);
    const spy = vi.spyOn(api, 'createPersonalNote').mockImplementationOnce(async (...args) => { await create(...args); throw new NetworkApiError(new Error('lost response')); });
    setup(api);
    fireEvent.click(screen.getByText('Сохранить фрагмент'));
    expect((screen.getByLabelText('Текст заметки') as HTMLTextAreaElement).value).toBe('Фрагмент ответа');
    screen.getByRole('button', { name: 'Сохранить' }).focus();
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Повторить' }));
    await screen.findByText('Заметка сохранена');
    expect(spy.mock.calls[0]?.[1].idempotencyKey).toBe(spy.mock.calls[1]?.[1].idempotencyKey);
    expect((await api.listNotes()).data.filter((entry) => entry.preview === 'Фрагмент ответа')).toHaveLength(1);
    expect((screen.getByLabelText('Черновик чата') as HTMLInputElement).value).toBe('не потерять');
    const list = screen.getByRole('dialog', { name: 'Заметки' });
    expect(list.contains(document.activeElement)).toBe(true);
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    expect(screen.queryByRole('dialog', { name: 'Заметки' })).toBeNull();
  });

  it('asks before Escape discards a captured draft and leaves it intact on cancel', async () => {
    setup();
    fireEvent.click(screen.getByText('Сохранить фрагмент'));
    fireEvent.keyDown(screen.getByRole('dialog', { name: 'Заметки' }), { key: 'Escape' });
    const confirm = await screen.findByRole('dialog', { name: 'Не сохранять?' });
    fireEvent.click(within(confirm).getByRole('button', { name: 'Назад' }));
    expect((screen.getByLabelText('Текст заметки') as HTMLTextAreaElement).value).toBe('Фрагмент ответа');
    fireEvent.click(screen.getByLabelText('Закрыть заметку'));
    fireEvent.click(within(await screen.findByRole('dialog', { name: 'Не сохранять?' })).getByRole('button', { name: 'Не сохранять' }));
    expect(screen.queryByRole('dialog', { name: 'Заметки' })).toBeNull();
  });

  it('does not overwrite a concurrent change and keeps the user draft', async () => {
    const api = createMockApiClient();
    const note = await api.createPersonalNote({ body: 'Исходная запись' }, { idempotencyKey: 'idem-notes-conflict' });
    setup(api);
    fireEvent.click(screen.getByText('Открыть заметки'));
    fireEvent.click(await screen.findByRole('option', { name: /Исходная запись/ }));
    fireEvent.change(await screen.findByLabelText('Текст заметки'), { target: { value: 'Мои изменения' } });
    await api.updatePersonalNote(note.id, { body: 'Другое окно', expectedRevision: 1 });
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
    await screen.findByText('Заметка изменена в другом окне. Ваш текст остался в поле.');
    expect((screen.getByLabelText('Текст заметки') as HTMLTextAreaElement).value).toBe('Мои изменения');
    expect((await api.getPersonalNote(note.id)).body).toBe('Другое окно');
  });

  it('guards a dirty draft when retrying a failed source read', async () => {
    const api = createMockApiClient();
    const note = await api.createPersonalNote({ body: 'Сохранённая запись' }, { idempotencyKey: 'idem-source-retry' });
    vi.spyOn(api, 'getPersonalNote').mockResolvedValue({ ...note, source: { kind: 'chat', chatId: 'chat-source', providerTurnId: 'turn-source', providerItemId: 'item-source', quote: 'Фрагмент' } });
    const resolve = vi.spyOn(api, 'resolvePersonalNoteSource').mockRejectedValue(new NetworkApiError(new Error('offline')));
    setup(api);
    fireEvent.click(screen.getByText('Открыть заметки'));
    fireEvent.click(await screen.findByRole('option', { name: /Сохранённая запись/ }));
    fireEvent.click(await screen.findByRole('button', { name: 'К сообщению' }));
    await screen.findByText('Не удалось открыть источник. Заметка сохранена.');
    fireEvent.change(screen.getByLabelText('Текст заметки'), { target: { value: 'Несохранённая правка' } });
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
    const confirm = await screen.findByRole('dialog', { name: 'Не сохранять?' });
    expect(resolve).toHaveBeenCalledTimes(1);
    fireEvent.click(within(confirm).getByRole('button', { name: 'Назад' }));
    expect((screen.getByLabelText('Текст заметки') as HTMLTextAreaElement).value).toBe('Несохранённая правка');
  });

  it('distinguishes list failure from empty and keeps create available without a project', async () => {
    const api = createMockApiClient();
    const create = vi.spyOn(api, 'createPersonalNote');
    vi.spyOn(api, 'listNotes').mockRejectedValue(new NetworkApiError(new Error('offline')));
    setup(api);
    fireEvent.click(screen.getByText('Открыть заметки'));
    await screen.findByText('Не удалось загрузить заметки.');
    expect(screen.queryByText('Пока нет заметок')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Новая заметка' }));
    expect((screen.getByRole('button', { name: 'Сохранить' }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Текст заметки'), { target: { value: 'Без саги' } });
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
    await screen.findByText('Заметка сохранена');
    expect(create).toHaveBeenCalledWith({ title: '', body: 'Без саги', source: null }, expect.objectContaining({ idempotencyKey: expect.any(String) }));
  });
});
