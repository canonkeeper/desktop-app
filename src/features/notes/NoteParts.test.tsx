import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { createAppRoute } from '../../shared/navigation/app-route';
import { useNotes, type NoteCapture } from '../../shared/notes/notes-context';
import { ToastsProvider } from '../../ui/feedback/Toast';
import { noteCatalogItem } from './NotesList';
import { NotesProvider } from './NotesProvider';

vi.mock('@tanstack/react-router', () => ({ useBlocker: vi.fn(() => ({ status: 'idle' })) }));

function Capture({ draft }: { draft: NoteCapture }) {
  const notes = useNotes()!;
  return <><button onClick={() => notes.capture(draft)}>Заметка из выделения</button><button onClick={notes.open}>Открыть заметки</button></>;
}

function setup(draft: NoteCapture, api = createMockApiClient()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const navigate = vi.fn();
  render(<QueryClientProvider client={client}><ToastsProvider>
    <NotesProvider accountId="user-mira" api={api} navigate={navigate} route={createAppRoute('/manuscript/books', { mode: 'read' })}><Capture draft={draft} /></NotesProvider>
  </ToastsProvider></QueryClientProvider>);
  return { api, navigate };
}

async function chapterLocator(api: ReturnType<typeof createMockApiClient>) {
  const entry = (await api.listNotes()).data.find((item) => item.sourceKind === 'chapter')!;
  const detail = await api.getPersonalNote(entry.id);
  if (detail.source?.kind !== 'chapter') throw new Error('missing chapter note fixture');
  return { entry, detail, locator: detail.source.locator };
}

describe('a note of three parts (specs/2026-11-11-ux7-notes.md NT1)', () => {
  it('keeps the quote of a selection apart, starts with an empty thought in focus and saves without a thought', async () => {
    const api = createMockApiClient();
    const { locator } = await chapterLocator(api);
    const create = vi.spyOn(api, 'createPersonalNote');
    const words = 'Белая пыль над портом';
    const { navigate } = setup({ body: words, place: 'Книга II, гл. 12 «Белый порт», абзац 1', source: { kind: 'chapter', quote: words, locator } }, api);
    fireEvent.click(screen.getByText('Заметка из выделения'));
    const panel = screen.getByRole('dialog', { name: 'Заметки' });
    expect(within(panel).getByRole('blockquote', { name: 'Цитата' }).textContent).toBe('Белая пыль над портом');
    expect(within(panel).getByText('Книга II, гл. 12 «Белый порт», абзац 1')).toBeTruthy();
    const thought = within(panel).getByLabelText('Ваша мысль') as HTMLTextAreaElement;
    expect(thought.value).toBe('');
    expect(document.activeElement).toBe(thought);
    expect(within(panel).queryByLabelText(/Название/)).toBeNull();
    fireEvent.click(within(panel).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ body: '', source: expect.objectContaining({ quote: words }) }), expect.anything()));
    // The line of the root says what happened and opens the note (NT1.4).
    const toast = await screen.findByText('Заметка сохранена');
    const saved = await create.mock.results[0]!.value;
    fireEvent.click(within(toast.closest('[data-tone]')!).getByRole('button', { name: 'Открыть' }));
    expect(navigate).toHaveBeenCalledWith('/notes', { note: saved.id });
  });

  it('shows an older note whose quote lies in its text as it is: the text is the thought', async () => {
    const api = createMockApiClient();
    const { locator } = await chapterLocator(api);
    const older = await api.createPersonalNote({ body: 'Белая пыль над портом\n\nМоя приписка', source: { kind: 'chapter', quote: 'Белая пыль над портом', locator } }, { idempotencyKey: 'idem-older-note' });
    setup({ body: '' }, api);
    fireEvent.click(screen.getByText('Открыть заметки'));
    fireEvent.click(await screen.findByRole('option', { name: /Белая пыль над портом/ }));
    expect((await screen.findByLabelText('Ваша мысль') as HTMLTextAreaElement).value).toBe(older.body);
    expect(screen.getByRole('blockquote', { name: 'Цитата' }).textContent).toBe('Белая пыль над портом');
  });

  it('says a place the book no longer has, keeps the quote and closes the way to it', async () => {
    const api = createMockApiClient();
    const { entry } = await chapterLocator(api);
    vi.spyOn(api, 'resolvePersonalNoteSource').mockResolvedValue({ state: 'unavailable' });
    setup({ body: '' }, api);
    fireEvent.click(screen.getByText('Открыть заметки'));
    fireEvent.click(await screen.findByRole('option', { name: (name) => name.includes(entry.preview) }));
    await screen.findByText('Этого места в книге больше нет — цитата сохранена');
    expect(screen.getByRole('blockquote', { name: 'Цитата' })).toBeTruthy();
    expect((screen.getByRole('button', { name: /К месту в книге/ }) as HTMLButtonElement).disabled).toBe(true);
  });
});

describe('a row of the list (NT1.3)', () => {
  const base = { id: 'n1', title: '', updatedAt: '2026-09-19T15:40:00.000Z' };
  const dust = 'Белая пыль над портом';
  const answer = 'Ответ';
  it('is the first line of the thought, without it the beginning of the quote; under it the place and the date', () => {
    const thought = noteCatalogItem({ ...base, sourceKind: 'chapter', preview: 'Сверить с судом\nи с письмом', quote: dust, place: 'Книга 2, гл. 12 „Белый порт“' });
    expect(thought).toMatchObject({ line: 'Сверить с судом', place: 'Книга 2, гл. 12 „Белый порт“' });
    const quoted = noteCatalogItem({ ...base, sourceKind: 'chapter', preview: '', quote: dust, place: 'Книга 2, гл. 12 „Белый порт“' });
    expect(quoted.line).toBe('«Белая пыль над портом»');
    expect(noteCatalogItem({ ...base, sourceKind: 'chapter', preview: '', quote: dust }).place).toBe('Места в книге больше нет');
    expect(noteCatalogItem({ ...base, sourceKind: 'chat', preview: 'Мысль', quote: answer, chatTitle: 'Кто первым заметил дым' }).place).toBe('Чат «Кто первым заметил дым»');
    expect(noteCatalogItem({ ...base, sourceKind: 'free', preview: 'Мысль' }).place).toBe('Своя мысль');
    // «Все саги» names the saga of a note (NT3.2); a saga's own list does not.
    expect(noteCatalogItem({ ...base, sourceKind: 'free', preview: 'Мысль' }, true).place).toBe('Своя мысль');
    const other = { ...base, sourceKind: 'chapter' as const, preview: 'Мысль', place: 'Книга 1, гл. 3', projectTitle: 'Северный архив' };
    expect(noteCatalogItem(other, true).place).toBe('Сага «Северный архив» · Книга 1, гл. 3');
    expect(noteCatalogItem(other).place).toBe('Книга 1, гл. 3');
  });
});
