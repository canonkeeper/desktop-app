import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
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
    fireEvent.click(await screen.findByRole('button', { name: 'Добавить запись' }));
    fillAndSave('  Кира узнает правду  ');
    const list = await screen.findByRole('list', { name: 'Записи памяти' });
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
    fireEvent.click(await screen.findByRole('button', { name: 'Добавить запись' }));
    fillAndSave('Кира узнает правду');
    expect(await within(screen.getByRole('dialog')).findByText('Не удалось сохранить запись.')).toBeTruthy();
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Сохранить' }));
    await screen.findByRole('list', { name: 'Записи памяти' });
    expect(create.mock.calls[1]?.[2].idempotencyKey).toBe(create.mock.calls[0]?.[2].idempotencyKey);
  });

  it('names a chapter the saga does not have', async () => {
    renderMemory(createMockApiClient());
    fireEvent.click(await screen.findByRole('button', { name: 'Добавить запись' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText(/^Глава/), { target: { value: '99' } });
    fillAndSave('Кира узнает правду');
    expect(await within(dialog).findByText('В саге нет такой главы. Укажите её номер, например 481.')).toBeTruthy();
  });

  it('changes a record at the revision it saw, with its chapter and name', async () => {
    const api = createMockApiClient();
    await addRecord(api, 'Кира узнает правду');
    const update = vi.spyOn(api, 'updateAuthorMemory');
    renderMemory(api);
    fireEvent.click(await screen.findByRole('button', { name: 'Изменить запись' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText(/^Глава/), { target: { value: '312' } });
    fireEvent.change(within(dialog).getByLabelText(/^Имя/), { target: { value: 'Кира' } });
    fillAndSave('Кира узнает всё');
    const list = await screen.findByRole('list', { name: 'Записи памяти' });
    await within(list).findByText('Кира узнает всё');
    expect(within(list).getByText('Глава 312')).toBeTruthy();
    expect(within(list).getByText('Кира')).toBeTruthy();
    expect(update.mock.calls[0]?.[2]).toEqual({ kind: 'plan', text: 'Кира узнает всё', chapter: '312', name: 'Кира', expectedRevision: 1 });
  });

  it('removes a record only after asking', async () => {
    const api = createMockApiClient();
    await addRecord(api, 'Кира узнает правду');
    renderMemory(api);
    fireEvent.click(await screen.findByRole('button', { name: 'Удалить запись' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Отмена' }));
    expect(screen.getByText('Кира узнает правду')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Удалить запись' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Удалить' }));
    expect(await screen.findByText('Пока ничего нет')).toBeTruthy();
  });

  it('a reader reads and is offered nothing to change', async () => {
    const api = createMockApiClient();
    await addRecord(api, 'Кира узнает правду');
    renderMemory(api, { canEdit: false });
    expect(await screen.findByText('Кира узнает правду')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Добавить запись' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Изменить запись' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Удалить запись' })).toBeNull();
  });

  it('a reader without records is told who adds them', async () => {
    renderMemory(createMockApiClient(), { canEdit: false });
    expect(await screen.findByText('Записи появятся, когда редактор саги их добавит.')).toBeTruthy();
  });

  it('the filters live in the URL', async () => {
    const api = createMockApiClient();
    await addRecord(api, 'Кира узнает правду');
    await addRecord(api, 'Ей двадцать лет', 'correction');
    const navigate = renderMemory(api, { query: { kind: 'correction' } });
    expect(await screen.findByText('Ей двадцать лет')).toBeTruthy();
    expect(screen.queryByText('Кира узнает правду')).toBeNull();
    fireEvent.change(screen.getByRole('searchbox', { name: 'Поиск по записям' }), { target: { value: 'двадцать' } });
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
