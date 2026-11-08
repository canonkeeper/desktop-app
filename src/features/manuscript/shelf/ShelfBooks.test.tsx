import type { ComponentProps } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import type { Book, ProjectJob } from '../../../entities/manuscript/api';
import { BookActionMenu, BookCard, ShelfBooks } from './ShelfBooks';
import { useShelfBookActions } from './useShelfBookActions';

async function testBook() {
  const response = await createMockApiClient().listBooks('project-white-port');
  const book = response.data[0];
  if (!book) throw new Error('В mock-проекте нет книги для теста полки.');
  return book;
}

function openRealBookActionMenu(book: Book) {
  const api = createMockApiClient();
  const queryClient = new QueryClient();

  function Harness() {
    const [selectedBookId, setSelectedBookId] = useState(book.id);
    const [, setNotice] = useState('');
    const actions = useShelfBookActions({
      api,
      books: [book],
      canEditManuscript: true,
      jobs: [],
      navigate: vi.fn(),
      navigateManuscript: vi.fn(),
      routeMenuBookId: null,
      selectedBook: book,
      selectedBookId,
      setNotice,
      setSelectedBookId,
    });

    return (
      <>
        <ShelfBooks
          indexingJobFor={() => null}
          books={[book]}
          onOpenMenu={actions.openBookMenu}
          onOpenReader={vi.fn()}
          onSelectBook={setSelectedBookId}
          selectedBookId={selectedBookId}
        />
        {actions.menuBookId ? (
          <BookActionMenu
            book={book}
            canEditManuscript
            onChangeCover={vi.fn()}
            onClose={actions.closeBookMenu}
            onDelete={vi.fn()}
            onDownload={vi.fn()}
            onOpenRead={vi.fn()}
            position={actions.menuPosition}
            state="open"
          />
        ) : null}
      </>
    );
  }

  render(
    <QueryClientProvider client={queryClient}>
      <Harness />
    </QueryClientProvider>,
  );

  const trigger = screen.getByRole('button', { name: `Действия книги ${book.title}` });
  fireEvent.click(trigger);
  return {
    menu: screen.getByRole('menu', { name: 'Действия книги' }),
    trigger,
  };
}

describe('BookCard', () => {
  it.each([false, true])('paints the %s selected card border above its cover without blocking actions', async (isActive) => {
    const book = await testBook();
    const onSelect = vi.fn();
    const onOpenMenu = vi.fn();
    render(<BookCard book={book} indexingJob={null} isActive={isActive}
      onOpenRead={vi.fn()} onOpenMenu={onOpenMenu} onSelect={onSelect} />);
    const card = screen.getByRole('article', { name: book.title });
    const cover = card.querySelector('[data-has-image]')!;
    const rules = [...document.styleSheets].flatMap((sheet) => [...sheet.cssRules])
      .filter((rule): rule is CSSStyleRule => rule instanceof CSSStyleRule
        && rule.selectorText.endsWith('::after') && card.matches(rule.selectorText.replaceAll('::after', '')));
    const overlay = rules.find((rule) => rule.style.getPropertyValue('z-index') === '1');
    expect(overlay).toBeDefined();
    expect(overlay!.style.position).toBe('absolute');
    expect(overlay!.style.inset).toBe('0');
    expect(overlay!.style.getPropertyValue('pointer-events')).toBe('none');
    expect(rules.map((rule) => rule.style.border).filter(Boolean).at(-1))
      .toBe(isActive ? '2px solid var(--ck-primary)' : '1px solid var(--ck-border)');
    expect(getComputedStyle(card).width).toBe('220px');
    expect(getComputedStyle(cover).height).toBe('150px');
    fireEvent.click(cover);
    expect(onSelect).toHaveBeenCalledOnce();
    const menu = screen.getByRole('button', { name: `Действия книги ${book.title}` });
    expect(getComputedStyle(menu).right).toBe('14px');
    expect(getComputedStyle(menu).top).toBe('12px');
    fireEvent.click(menu);
    expect(onOpenMenu).toHaveBeenCalledWith(menu);
    expect(onSelect).toHaveBeenCalledOnce();
  });

  it('разделяет выбор карточки, переход в чтение и меню действий', async () => {
    const book = await testBook();
    const onOpen = vi.fn();
    const onOpenMenu = vi.fn();
    const onSelect = vi.fn();

    render(
      <ShelfBooks
        indexingJobFor={() => null}
        books={[book]}
        onOpenMenu={onOpenMenu}
        onOpenReader={onOpen}
        onSelectBook={onSelect}
        selectedBookId={book.id}
      />,
    );

    const card = screen.getByRole('article', { name: book.title });
    expect(card.getAttribute('aria-current')).toBe('true');
    fireEvent.click(card);
    expect(onSelect).toHaveBeenCalledWith(book.id);

    const title = screen.getByRole('button', { name: `Открыть чтение ${book.title}` });
    expect(title.textContent).toBe(book.title);
    fireEvent.click(title);
    expect(onOpen).toHaveBeenCalledWith(book);
    expect(onSelect).toHaveBeenCalledOnce();

    fireEvent.click(screen.getByRole('button', { name: `Действия книги ${book.title}` }));
    expect(onOpenMenu).toHaveBeenCalledOnce();
    expect(onOpenMenu.mock.calls[0]?.[0]).toBe(book.id);
    expect(onOpenMenu.mock.calls[0]?.[1]).toBeInstanceOf(HTMLElement);
    expect(onSelect).toHaveBeenCalledOnce();
  });

  it('показывает идущую индексацию и скрывает успешно завершённую', async () => {
    const book = await testBook();
    const textJob = {
      id: 'job-text',
      kind: 'canon_text_index',
      status: 'running',
      progress: null,
      phase: null,
      subject: { type: 'canon_text_index', projectId: book.projectId, sourceView: 'draft', catalogRevision: 2 },
      result: null,
      error: null,
      canCancel: false,
      expiresAt: null,
      createdAt: '2026-08-09T11:55:48.000Z',
      updatedAt: '2026-08-09T12:00:00.000Z',
      links: { self: '/api/v1/jobs/job-text', cancel: null, timeline: null },
    } satisfies ProjectJob;
    const props = {
      book,
      isActive: false,
      onOpenMenu: vi.fn(),
      onOpenRead: vi.fn(),
      onSelect: vi.fn(),
    };
    const view = render(<BookCard {...props} indexingJob={textJob} />);

    expect(screen.getByText('Подготовка')).toBeTruthy();

    view.rerender(<BookCard {...props} indexingJob={{ ...textJob, status: 'succeeded' }} />);
    expect(screen.queryByText('Подготовка')).toBeNull();
    view.rerender(<BookCard {...props} book={{ ...book, status: 'ready' }} indexingJob={{ ...textJob, status: 'succeeded' }} />);
    expect(screen.getByText('Готова')).toBeTruthy();
  });

  it('shows no status for a book that is neither indexing, ready nor failed', async () => {
    const book = await testBook();
    const view = render(<BookCard
      book={{ ...book, status: 'draft' }}
      indexingJob={null}
      isActive={false}
      onOpenMenu={vi.fn()}
      onOpenRead={vi.fn()}
      onSelect={vi.fn()}
    />);

    expect(view.container.textContent).not.toMatch(/Черновик|Подготовка|Опубликован/);
    expect(screen.getByText(book.title)).toBeTruthy();
  });

  it('показывает ошибку для книги со статусом error', async () => {
    const book = await testBook();
    render(<BookCard
      book={{ ...book, status: 'error' }}
      indexingJob={null}
      isActive={false}
      onOpenMenu={vi.fn()}
      onOpenRead={vi.fn()}
      onSelect={vi.fn()}
    />);

    expect(screen.getByText('Подготовка не удалась')).toBeTruthy();
  });
});

describe('BookActionMenu', () => {
  it('возвращает фокус точной кнопке книги после закрытия через Escape', async () => {
    const book = await testBook();
    const { menu, trigger } = openRealBookActionMenu(book);
    expect(document.activeElement).toBe(within(menu).getByRole('menuitem', { name: 'Открыть чтение' }));

    fireEvent.keyDown(menu, { key: 'Escape' });

    await waitFor(() => expect(document.activeElement).toBe(trigger));
  });

  it('возвращает фокус точной кнопке книги после закрытия через фон', async () => {
    const book = await testBook();
    const { trigger } = openRealBookActionMenu(book);

    fireEvent.pointerDown(document.body);
    // jsdom не воспроизводит нативный pointer focus Chromium после React-handler.
    trigger.blur();
    expect(document.activeElement).toBe(document.body);

    await waitFor(() => expect(document.activeElement).toBe(trigger));
  });

  it('использует канонический roving focus для пунктов меню', async () => {
    const book = await testBook();
    const { menu } = openRealBookActionMenu(book);
    const items = within(menu).getAllByRole('menuitem');

    expect(document.activeElement).toBe(items[0]);
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(items[1]);
    fireEvent.keyDown(document.activeElement!, { key: 'End' });
    expect(document.activeElement).toBe(items.at(-1));
  });

  it('сохраняет позицию якоря и ограничения роли', async () => {
    const book = await testBook();
    const onOpenRead = vi.fn();

    render(
      <BookActionMenu
        book={book}
        canEditManuscript={false}
        onChangeCover={vi.fn()}
        onClose={vi.fn()}
        onDelete={vi.fn()}
        onDownload={vi.fn()}
        onOpenRead={onOpenRead}
        position={{ left: 24, top: 48 }}
        state="open"
      />,
    );

    const menu = screen.getByRole('menu', { name: 'Действия книги' });
    expect(menu.style.left).toBe('24px');
    expect(menu.style.top).toBe('48px');
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Открыть чтение' }));
    expect(onOpenRead).toHaveBeenCalledOnce();
    expect(within(menu).queryByRole('menuitem', { name: 'Изменить обложку' })).toBeNull();
    expect(within(menu).queryByRole('menuitem', { name: 'Удалить книгу' })).toBeNull();
  });

  it('передаёт разрешённые команды', async () => {
    const book = await testBook();
    const onChangeCover = vi.fn();
    const onDelete = vi.fn();

    render(
      <BookActionMenu
        book={book}
        canEditManuscript
        onChangeCover={onChangeCover}
        onClose={vi.fn()}
        onDelete={onDelete}
        onDownload={vi.fn()}
        onOpenRead={vi.fn()}
        position={null}
        state="open"
      />,
    );

    const menu = screen.getByRole('menu', { name: 'Действия книги' });
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Изменить обложку' }));
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Удалить книгу' }));

    expect(onChangeCover).toHaveBeenCalledOnce();
    expect(onDelete).toHaveBeenCalledOnce();
  });

  it('offers «Обновить из файла» to editors and holds it while an import runs', async () => {
    const book = await testBook();
    const onUpdateFromFile = vi.fn();
    const menu = (canEditManuscript: boolean, updateDisabled: boolean) => (
      <BookActionMenu
        book={book}
        canEditManuscript={canEditManuscript}
        onChangeCover={vi.fn()}
        onClose={vi.fn()}
        onDelete={vi.fn()}
        onDownload={vi.fn()}
        onOpenRead={vi.fn()}
        onUpdateFromFile={onUpdateFromFile}
        position={null}
        state="open"
        updateDisabled={updateDisabled}
      />
    );
    const view = render(menu(true, false));

    fireEvent.click(screen.getByRole('menuitem', { name: 'Обновить из файла' }));
    expect(onUpdateFromFile).toHaveBeenCalledOnce();
    view.rerender(menu(true, true));
    expect(screen.getByRole('menuitem', { name: 'Обновить из файла' })).toHaveProperty('disabled', true);
    view.rerender(menu(false, false));
    expect(screen.queryByRole('menuitem', { name: 'Обновить из файла' })).toBeNull();
  });

  it('блокирует удаление книги во время активной индексации', async () => {
    const book = await testBook();
    const indexingJob = {
      id: 'indexing-active',
      kind: 'import',
      status: 'running',
      progress: null,
      phase: null,
      subject: { type: 'import', id: 'indexing-active', projectId: book.projectId },
      result: null,
      error: null,
      canCancel: true,
      expiresAt: null,
      createdAt: '2026-08-09T11:55:48.000Z',
      updatedAt: '2026-08-09T12:00:00.000Z',
      links: { self: '/api/v1/jobs/indexing-active', cancel: '/api/v1/jobs/indexing-active/cancel', timeline: null },
    } satisfies ProjectJob;

    render(
      <BookActionMenu
        book={book}
        canEditManuscript
        indexingJob={indexingJob}
        onChangeCover={vi.fn()}
        onClose={vi.fn()}
        onDelete={vi.fn()}
        onDownload={vi.fn()}
        onOpenRead={vi.fn()}
        position={null}
        state="open"
      />,
    );

    expect(screen.getByRole('menuitem', { name: 'Удалить книгу' })).toHaveProperty('disabled', true);
  });

  it('offers the three files of a book to an editor, and nothing to a reader or for a book without text', async () => {
    const book = await testBook();
    const onDownload = vi.fn();
    const menu = (overrides: Partial<ComponentProps<typeof BookActionMenu>>) => (
      <BookActionMenu
        book={book}
        canEditManuscript
        onChangeCover={vi.fn()}
        onClose={vi.fn()}
        onDelete={vi.fn()}
        onDownload={onDownload}
        onOpenRead={vi.fn()}
        state="open"
        {...overrides}
      />
    );
    const { rerender } = render(menu({}));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Скачать в Word' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Скачать в EPUB' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Скачать в FB2' }));
    expect(onDownload.mock.calls).toEqual([['docx'], ['epub'], ['fb2']]);

    rerender(menu({ canEditManuscript: false }));
    expect(screen.queryByRole('menuitem', { name: /Скачать/u })).toBeNull();

    rerender(menu({ book: { ...book, chapterCount: 0 } }));
    expect(screen.queryByRole('menuitem', { name: /Скачать/u })).toBeNull();
  });
});
