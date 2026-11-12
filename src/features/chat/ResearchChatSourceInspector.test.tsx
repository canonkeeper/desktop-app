import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useRef, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiStatusError, type Chapter, type ChatChapterLocation } from '../../shared/api';
import type { ChatSourceTarget } from '../../shared/navigation/chat-source';
import { ResearchChatSourceInspector } from './ResearchChatSourceInspector';

const location: ChatChapterLocation = {
  chapterKey: '12', bookId: 'book-one', chapterId: 'chapter-one', bookTitle: 'Книга II', title: 'Глава 12. У ворот',
};
const texts = ['Глава 12. У ворот', 'Мара остановилась у ворот.', 'Стражник молчал.', 'Ветер сменился.'];
const chapter = {
  id: 'chapter-one', bookId: 'book-one', title: 'Глава 12. У ворот', order: 12, contentVariant: 'draft', status: 'draft',
  paragraphs: texts.map((text, index) => ({ id: `p-${index + 1}`, order: index + 1, kind: index === 0 ? 'heading' : 'paragraph', text, markdown: text })),
} as unknown as Chapter;

function notFound() {
  return new ApiStatusError(404, { type: 'about:blank', status: 404, title: 'Not found', code: 'not_found', requestId: 'synthetic-source' }, new Response(null, { status: 404 }));
}

const openInBook = vi.fn();

function mount(api: { resolveChatChapterKey: (chatId: string, key: string) => Promise<ChatChapterLocation>; getChapter: (chapterId: string) => Promise<Chapter>; getChatAttachment?: unknown }, initial: ChatSourceTarget = { chapterKey: '12', from: 2, to: 3 }, phrase: string | null = null) {
  function Harness() {
    const [target, setTarget] = useState<ChatSourceTarget | null>(null);
    const returnFocusRef = useRef<HTMLButtonElement>(null);
    return <>
      <textarea aria-label="Черновик сообщения" defaultValue="Моё следующее сообщение" />
      <button ref={returnFocusRef} onClick={() => setTarget(initial)}>Открыть</button>
      <button onClick={() => setTarget({ chapterKey: '999', from: 1, to: 1 })}>Другой источник</button>
      {target ? <ResearchChatSourceInspector
        api={api as never} chatId="chat-one" projectId="project-one" sourceView="draft"
        chatTitle="Кто открыл северные ворота?"
        navigation={{ target, phrase, returnFocusRef, open: vi.fn(), openInBook, close: () => setTarget(null), setMode: (mode) => setTarget({ ...target, mode }) }}
      /> : null}
    </>;
  }
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}><Harness /></QueryClientProvider>);
}

const originalScroll = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollIntoView');
beforeEach(() => {
  vi.stubGlobal('innerWidth', 1440);
  Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: vi.fn() });
});
afterEach(() => {
  if (originalScroll) Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', originalScroll);
  else Reflect.deleteProperty(HTMLElement.prototype, 'scrollIntoView');
  vi.restoreAllMocks(); vi.unstubAllGlobals();
});

describe('source inspector', () => {
  // specs/2026-11-11-ux9-shelf-chat.md SC3.7 (J-25): the words of the answer are marked inside the paragraph it cites.
  it('marks the words of the answer inside the cited paragraph', async () => {
    const api = { resolveChatChapterKey: vi.fn().mockResolvedValue(location), getChapter: vi.fn().mockResolvedValue(chapter) };
    mount(api, { chapterKey: '12', from: 2, to: 2 }, 'У ворот Мара остановилась у ворот и ждала (гл. 12).');
    fireEvent.click(screen.getByRole('button', { name: 'Открыть' }));
    const marked = await screen.findByText('Мара остановилась у ворот');
    expect(marked.closest('mark')).toBeTruthy();
  });

  it('shows the cited paragraphs of the chapter the key names and links Reader to the first one', async () => {
    const api = { resolveChatChapterKey: vi.fn().mockResolvedValue(location), getChapter: vi.fn().mockResolvedValue(chapter) };
    mount(api);
    const draft = screen.getByRole('textbox', { name: 'Черновик сообщения' });
    fireEvent.click(screen.getByRole('button', { name: 'Открыть' }));
    expect(await screen.findByText('Мара остановилась у ворот.')).toBeTruthy();
    expect(screen.getByText('Стражник молчал.')).toBeTruthy();
    // AA1.7 (Ч-43): «Это место» is the quote with one quiet paragraph before and after it, to read what it is about.
    expect(screen.getByText('Ветер сменился.').closest('[data-muted]')).toBeTruthy();
    expect(screen.getByText('Глава 12. У ворот', { selector: '[data-paragraph-id] *, [data-paragraph-id]' }).closest('[data-muted]')).toBeTruthy();
    expect(screen.getByText('Мара остановилась у ворот.').closest('[data-muted]')).toBeNull();
    expect(api.resolveChatChapterKey).toHaveBeenCalledWith('chat-one', '12', expect.anything());
    expect(api.getChapter).toHaveBeenCalledWith('chapter-one', 'draft');
    expect(screen.getByText('Книга II · абзацы 2–3')).toBeTruthy();
    // «Открыть в книге» in the same tab, a word on the button; the reader leads back to the chat (PS4.1, W-5B).
    const link = screen.getByRole('link', { name: 'Открыть в книге' });
    expect(link.getAttribute('target')).toBeNull();
    const destination = new URL(link.getAttribute('href')!, 'http://localhost');
    expect(destination.pathname).toBe('/manuscript/books');
    const query = { projectId: 'project-one', bookId: 'book-one', chapterId: 'chapter-one', paragraphId: 'p-2', mode: 'read' };
    expect(Object.fromEntries(destination.searchParams)).toMatchObject(query);
    fireEvent.click(link);
    expect(openInBook).toHaveBeenCalledWith(query, 'Кто открыл северные ворота?');
    expect(screen.getByRole('textbox', { name: 'Черновик сообщения' })).toBe(draft);
  });

  it('switches to the whole chapter and returns focus without remounting the draft', async () => {
    mount({ resolveChatChapterKey: vi.fn().mockResolvedValue(location), getChapter: vi.fn().mockResolvedValue(chapter) });
    const draft = screen.getByRole('textbox', { name: 'Черновик сообщения' });
    fireEvent.change(draft, { target: { value: 'Не терять этот текст' } });
    const trigger = screen.getByRole('button', { name: 'Открыть' });
    trigger.focus();
    fireEvent.click(trigger);
    expect(await screen.findByText('Мара остановилась у ворот.')).toBeTruthy();
    fireEvent.click(screen.getByRole('radio', { name: 'Вся глава' }));
    expect(await screen.findByText('Ветер сменился.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'К этому месту' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть место из ответа' }));
    expect(screen.queryByRole('document')).toBeNull();
    expect((screen.getByRole('textbox', { name: 'Черновик сообщения' }) as HTMLTextAreaElement).value).toBe('Не терять этот текст');
    expect(document.activeElement).toBe(trigger);
  });

  it('shows the whole chapter for a chapter citation and for a range the text no longer has', async () => {
    mount({ resolveChatChapterKey: vi.fn().mockResolvedValue(location), getChapter: vi.fn().mockResolvedValue(chapter) }, { chapterKey: '12', from: 40, to: 41 });
    fireEvent.click(screen.getByRole('button', { name: 'Открыть' }));
    expect(await screen.findByText(/нет абзацев 40–41/u)).toBeTruthy();
    expect(screen.getByText('Ветер сменился.')).toBeTruthy();
    expect(screen.queryByRole('radiogroup')).toBeNull();
  });

  it('never displays a late previous source after selecting an unavailable one', async () => {
    let resolveFirst!: (value: ChatChapterLocation) => void;
    const api = {
      resolveChatChapterKey: vi.fn((_chat: string, key: string) => key === '12'
        ? new Promise<ChatChapterLocation>((resolve) => { resolveFirst = resolve; })
        : Promise.reject(notFound())),
      getChapter: vi.fn().mockResolvedValue(chapter),
    };
    mount(api);
    fireEvent.click(screen.getByRole('button', { name: 'Открыть' }));
    await waitFor(() => expect(api.resolveChatChapterKey).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole('button', { name: 'Другой источник' }));
    expect(await screen.findByText(/Не нашёл это место в книге/u)).toBeTruthy();
    resolveFirst(location);
    await waitFor(() => expect(screen.queryByText('Мара остановилась у ворот.')).toBeNull());
  });

  // AA1.6 (Ч-42): a link of an answer to a place that is not there says so in the words of the helper, without the key.
  it('says a dead link is a place it did not find and asks where it came from', async () => {
    const onAskWhere = vi.fn();
    function Harness() {
      const returnFocusRef = useRef<HTMLButtonElement>(null);
      return <ResearchChatSourceInspector
        api={{ resolveChatChapterKey: vi.fn().mockRejectedValue(notFound()), getChapter: vi.fn() } as never} chatId="chat-one" projectId="project-one"
        sourceView="draft" onAskWhere={onAskWhere}
        navigation={{ target: { chapterKey: 'b2c16', from: 1, to: 1 }, phrase: null, returnFocusRef, open: vi.fn(), openInBook, close: vi.fn(), setMode: vi.fn() }} />;
    }
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}><Harness /></QueryClientProvider>);
    expect(await screen.findByText('Не нашёл это место в книге. Возможно, помощник ошибся ссылкой или глава изменилась.')).toBeTruthy();
    expect(document.body.textContent).not.toContain('b2c16');
    expect(screen.queryByText(/Место недоступно/u)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Спросить, откуда это' }));
    expect(onAskWhere).toHaveBeenCalledTimes(1);
  });

  it('uses the modal overlay on mobile and restores focus on Escape', async () => {
    vi.stubGlobal('innerWidth', 390);
    mount({ resolveChatChapterKey: vi.fn().mockResolvedValue(location), getChapter: vi.fn().mockResolvedValue(chapter) });
    const trigger = screen.getByRole('button', { name: 'Открыть' });
    trigger.focus();
    fireEvent.click(trigger);
    expect(await screen.findByRole('dialog', { name: 'Место из ответа' })).toBeTruthy();
    fireEvent.keyDown(screen.getByRole('dialog', { name: 'Место из ответа' }), { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });
});

// specs/2026-11-07-chat-attach-chapter.md CA4.5: a place of the chapter the author attached shows beside the chat as a
// chapter of the book does, signed as his and without the way into Reader.
describe('the source inspector of an attached chapter', () => {
  it('shows the cited paragraphs of the attachment, signed as the author\'s', async () => {
    const api = {
      resolveChatChapterKey: vi.fn(), getChapter: vi.fn(),
      getChatAttachment: vi.fn().mockResolvedValue({
        number: 1, title: 'Глава 634. Перевал', format: 'md', words: 7, createdAt: '2026-11-07T09:00:00Z',
        paragraphs: ['Кайл шёл к реке.', 'Ольм отстал у брода.', 'Ветер сменился.'],
      }),
    };
    mount(api, { chapterKey: 'a1', from: 2, to: 2 });
    fireEvent.click(screen.getByRole('button', { name: 'Открыть' }));
    expect(await screen.findByText('Ольм отстал у брода.')).toBeTruthy();
    // The neighbours of the quote stand quiet (AA1.7).
    expect(screen.getByText('Кайл шёл к реке.').closest('[data-muted]')).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Глава 634. Перевал' })).toBeTruthy();
    expect(screen.getByText('Глава автора, в книге её ещё нет · абзац 2')).toBeTruthy();
    expect(screen.queryByRole('link', { name: 'Открыть в книге' })).toBeNull();
    expect(api.getChatAttachment).toHaveBeenCalledWith('chat-one', 1, expect.anything());
    expect(api.resolveChatChapterKey).not.toHaveBeenCalled();
  });
});
