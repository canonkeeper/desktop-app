import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState, type ComponentProps } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Book } from '../../../entities/manuscript/api';
import type { BookState } from '../../../entities/manuscript/preparation-copy';
import { BookActionMenu, placeMenu } from './BookActionMenu';
import { ShelfBooks, type ShelfBooksProps } from './ShelfBooks';

function book(index: number, overrides: Partial<Book> = {}): Book {
  return {
    id: `book-${index}`, projectId: 'project-1', title: `Том ${index}`, subtitle: null, order: index, place: index, displayNumber: String(index),
    coverColor: '#285f58', coverUrl: null, status: 'ready', chapterCount: 14, wordCount: 84_000,
    createdAt: '2026-11-11T10:00:00Z', updatedAt: '2026-11-11T10:00:00Z', ...overrides,
  };
}

const ready: BookState = { kind: 'ready', word: 'Готова', finish: false };

function renderShelf(overrides: Partial<ShelfBooksProps> = {}) {
  const props: ShelfBooksProps = {
    books: [book(2, { title: 'Белый порт' })],
    finishing: false,
    hrefOf: (item) => `/manuscript/books?projectId=project-1&bookId=${item.id}&mode=read`,
    onFinish: vi.fn(),
    onOpen: vi.fn(),
    onOpenMenu: vi.fn(),
    onRename: vi.fn(async () => null),
    onRenameEnd: vi.fn(),
    renaming: null,
    stateOf: () => ready,
    ...overrides,
  };
  return { props, ...render(<ShelfBooks {...props} />) };
}

afterEach(() => window.localStorage.clear());

// specs/2026-11-11-ux7-shelf.md SF1: the card of a book is one link «Читать «…»».
describe('the card of a book', () => {
  it('is one link to the reader that opens the book, the whole card wide, with no chosen book', () => {
    const { props } = renderShelf();
    const card = screen.getByRole('article', { name: 'Белый порт' });
    const link = within(card).getByRole('link', { name: 'Читать «Белый порт»' });
    expect(link.getAttribute('href')).toBe('/manuscript/books?projectId=project-1&bookId=book-2&mode=read');
    expect(card.getAttribute('aria-current')).toBeNull();
    fireEvent.click(link);
    expect(props.onOpen).toHaveBeenCalledWith(props.books[0]);
    // A new tab keeps the link of the browser (the test stops the browser itself from going there).
    document.addEventListener('click', (event) => event.preventDefault(), { once: true });
    fireEvent.click(link, { ctrlKey: true });
    expect(props.onOpen).toHaveBeenCalledOnce();
    // The link stretches over the card; «⋯» stands over it.
    const rules = [...document.styleSheets].flatMap((sheet) => [...sheet.cssRules])
      .filter((rule): rule is CSSStyleRule => rule instanceof CSSStyleRule);
    const stretch = rules.find((rule) => rule.selectorText.endsWith('::before') && link.matches(rule.selectorText.replace('::before', '')));
    expect(stretch?.style.position).toBe('absolute');
    expect(stretch?.style.inset).toBe('0');
    const menu = within(card).getByRole('button', { name: 'Действия книги Белый порт' });
    expect(Number(getComputedStyle(menu).zIndex)).toBeGreaterThan(Number(stretch?.style.getPropertyValue('z-index')));
  });

  it('goes by Tab to the card first and then to its «⋯», and «⋯» says where it stands', () => {
    const { props } = renderShelf();
    const card = screen.getByRole('article', { name: 'Белый порт' });
    const link = within(card).getByRole('link');
    const menu = within(card).getByRole('button', { name: 'Действия книги Белый порт' });
    const focusable = [...card.querySelectorAll<HTMLElement>('a[href], button')];
    expect(focusable.indexOf(link)).toBe(0);
    expect(focusable.indexOf(menu)).toBe(1);
    fireEvent.click(menu);
    expect(props.onOpenMenu).toHaveBeenCalledWith(props.books[0], menu);
    expect(props.onOpen).not.toHaveBeenCalled();
  });

  it('shows the volume apart from the title, as in the book, and one word of its state', () => {
    renderShelf({ books: [book(3, { title: 'Книга о северном суде', chapterCount: 14, wordCount: 96_000 })] });
    const card = screen.getByRole('article', { name: 'Книга о северном суде' });
    expect(within(card).getByText('Книга 3 · 14 глав · 96 тыс. слов')).toBeTruthy();
    expect(within(within(card).getByRole('link')).getByText('Книга о северном суде')).toBeTruthy();
    expect(within(card).getByText('Готова')).toBeTruthy();
  });

  it('puts «Дочитать» at the book the reading stopped on, and has no word while the status is loading', () => {
    const { props } = renderShelf({
      books: [book(1), book(2)],
      stateOf: (item) => (item.id === 'book-2' ? { kind: 'unfinished', word: 'Прочитана не полностью', finish: true } : null),
    });
    const stopped = screen.getByRole('article', { name: 'Том 2' });
    expect(within(stopped).getByText('Прочитана не полностью')).toBeTruthy();
    const focusable = [...stopped.querySelectorAll<HTMLElement>('a[href], button')];
    expect(focusable.map((element) => element.textContent || element.getAttribute('aria-label')))
      .toEqual(['Том 2', 'Действия книги Том 2', 'Дочитать']);
    fireEvent.click(within(stopped).getByRole('button', { name: 'Дочитать' }));
    expect(props.onFinish).toHaveBeenCalledOnce();
    expect(props.onOpen).not.toHaveBeenCalled();
    const loading = screen.getByRole('article', { name: 'Том 1' });
    expect(within(loading).queryByText(/Готова|Читается|Ждёт/u)).toBeNull();
  });

  // specs/2026-11-12-ux10-book-revert.md BR3.1: for 7 days after an update from a file the card says it with «Что изменилось»,
  // by the update the server keeps — on a card and on a row of the list.
  it('says the last update of the book from a file with «Что изменилось»', () => {
    const at = new Date();
    at.setHours(9, 5, 0, 0);
    const update = { at: at.toISOString(), revertUntil: new Date(at.getTime() + 7 * 86_400_000).toISOString(), jobId: null, changed: 1, added: 0, removed: 0 };
    const onChanges = vi.fn();
    const { props } = renderShelf({ books: [book(1, { update }), book(2)], onChanges });
    const updated = screen.getByRole('article', { name: 'Том 1' });
    expect(updated.textContent).toContain('Обновлена из файла сегодня в 09:05 · Что изменилось');
    fireEvent.click(within(updated).getByRole('button', { name: 'Что изменилось' }));
    expect(onChanges).toHaveBeenCalledWith(props.books[0]);
    expect(props.onOpen).not.toHaveBeenCalled();
    expect(screen.getByRole('article', { name: 'Том 2' }).textContent).not.toContain('Обновлена');
  });
});

// SF3.1: «Переименовать» — the title in a field in its place.
describe('the title in place', () => {
  function Renaming({ onRename, onRenameEnd }: { onRename: ShelfBooksProps['onRename']; onRenameEnd: () => void }) {
    const [renaming, setRenaming] = useState<ShelfBooksProps['renaming']>({ bookId: 'book-2', saving: false });
    return <ShelfBooks books={[book(2, { title: 'Белый порт' })]} finishing={false} hrefOf={() => '/'} onFinish={vi.fn()} onOpen={vi.fn()}
      onOpenMenu={vi.fn()} onRename={onRename} onRenameEnd={() => { onRenameEnd(); setRenaming(null); }} renaming={renaming} stateOf={() => ready} />;
  }

  // specs/2026-11-11-ux9-shelf-chat.md SC2.3 (П-19): the keys of the field are named only where there is a keyboard.
  it.each([[true, 1], [false, 0]] as const)('names Enter and Esc of the field only with a keyboard at hand (%s)', (keyboard, count) => {
    vi.stubGlobal('matchMedia', vi.fn((query: string) => ({ matches: keyboard && query === '(hover: hover) and (pointer: fine)', addEventListener: vi.fn(), removeEventListener: vi.fn() })));
    render(<Renaming onRename={vi.fn(async () => null)} onRenameEnd={vi.fn()} />);
    expect(screen.queryAllByText('Enter — сохранить, Esc — отменить')).toHaveLength(count);
    vi.unstubAllGlobals();
  });

  it('saves by Enter and by leaving the field, puts the title back by Esc and saves no empty name', async () => {
    const onRename = vi.fn(async () => null);
    const onRenameEnd = vi.fn();
    const view = render(<Renaming onRename={onRename} onRenameEnd={onRenameEnd} />);
    const field = screen.getByRole('textbox', { name: 'Название книги «Белый порт»' });
    expect(document.activeElement).toBe(field);
    expect(screen.queryByRole('link')).toBeNull();

    fireEvent.change(field, { target: { value: '   ' } });
    fireEvent.keyDown(field, { key: 'Enter' });
    expect(await screen.findByRole('alert')).toHaveProperty('textContent', 'Название не может быть пустым');
    expect(onRename).not.toHaveBeenCalled();

    fireEvent.change(field, { target: { value: '  Белый порт Харальда ' } });
    await act(async () => fireEvent.keyDown(field, { key: 'Enter' }));
    expect(onRename).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'book-2' }), 'Белый порт Харальда');
    view.unmount();

    const left = render(<Renaming onRename={onRename} onRenameEnd={onRenameEnd} />);
    const second = screen.getByRole('textbox');
    fireEvent.change(second, { target: { value: 'Белый порт II' } });
    await act(async () => fireEvent.blur(second));
    expect(onRename).toHaveBeenLastCalledWith(expect.anything(), 'Белый порт II');
    left.unmount();

    render(<Renaming onRename={onRename} onRenameEnd={onRenameEnd} />);
    const third = screen.getByRole('textbox');
    fireEvent.change(third, { target: { value: 'Что-то другое' } });
    fireEvent.keyDown(third, { key: 'Escape' });
    expect(onRenameEnd).toHaveBeenCalled();
    expect(onRename).toHaveBeenCalledTimes(2);
    expect(await screen.findByRole('link', { name: 'Читать «Белый порт»' })).toBeTruthy();
  });

  it('keeps the field and says why the server refused', async () => {
    render(<Renaming onRename={async () => 'Переименовать книгу может владелец саги, администратор или редактор.'} onRenameEnd={vi.fn()} />);
    const field = screen.getByRole('textbox');
    fireEvent.change(field, { target: { value: 'Новое' } });
    await act(async () => fireEvent.keyDown(field, { key: 'Enter' }));
    expect(screen.getByRole('alert').textContent).toBe('Переименовать книгу может владелец саги, администратор или редактор.');
    expect(screen.getByRole('textbox')).toBeTruthy();
  });
});

// SF4.2: from 13 books — «Обложки / Списком» and «Найти книгу».
describe('a large shelf', () => {
  const many = (count: number) => Array.from({ length: count }, (_, index) => book(index + 1, {
    title: index === 4 ? 'Соль и пепел' : index === 5 ? 'Дорога на Келл' : `Том ${index + 1}`,
  }));

  it('has neither the view nor the search up to 12 books', () => {
    renderShelf({ books: many(12) });
    expect(screen.queryByRole('radiogroup', { name: 'Вид книг' })).toBeNull();
    expect(screen.queryByRole('searchbox', { name: 'Найти книгу' })).toBeNull();
  });

  it('finds a book by its title on the page, says when nothing is found and gives all back', () => {
    renderShelf({ books: many(13) });
    const find = screen.getByRole('searchbox', { name: 'Найти книгу' });
    fireEvent.change(find, { target: { value: 'ПЕПЕЛ' } });
    expect(screen.getAllByRole('article').map((card) => card.getAttribute('aria-label'))).toEqual(['Соль и пепел']);
    fireEvent.change(find, { target: { value: 'Келлермарк' } });
    expect(screen.queryAllByRole('article')).toHaveLength(0);
    expect(screen.getByText('Ничего не нашлось по «Келлермарк»')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Сбросить поиск' }));
    expect(screen.getAllByRole('article')).toHaveLength(13);
  });

  it('shows the books as a list with the same links and words, and the browser keeps the choice', () => {
    const view = renderShelf({ books: many(14) });
    fireEvent.click(within(screen.getByRole('radiogroup', { name: 'Вид книг' })).getByRole('radio', { name: 'Списком' }));
    const list = screen.getByRole('list', { name: 'Книги саги списком' });
    const row = within(list).getByRole('article', { name: 'Соль и пепел' });
    expect(within(row).getByRole('link', { name: 'Читать «Соль и пепел»' })).toBeTruthy();
    expect(within(row).getByText('Книга 5')).toBeTruthy();
    expect(within(row).getByText('Готова')).toBeTruthy();
    view.unmount();
    renderShelf({ books: many(14) });
    expect(screen.getByRole('list', { name: 'Книги саги списком' })).toBeTruthy();
  });
});

// SF1.4: «⋯» of a book.
describe('the menu of a book', () => {
  function renderMenu(overrides: Partial<ComponentProps<typeof BookActionMenu>> = {}) {
    const props: ComponentProps<typeof BookActionMenu> = {
      anchor: { bottom: 228, left: 930, right: 958, top: 200 }, book: book(4, { title: 'Последний свет' }), canEdit: true, deleteReason: null,
      onChangeCover: vi.fn(), onClose: vi.fn(), onDelete: vi.fn(), onDownload: vi.fn(), onReadFromStart: vi.fn(), onRename: vi.fn(),
      onUpdateFromFile: vi.fn(), state: 'open', updateReason: null, ...overrides,
    };
    render(<BookActionMenu {...props} />);
    return { props, menu: screen.getByRole('menu', { name: 'Действия книги' }) };
  }

  // specs/2026-11-11-ux9-shelf-chat.md SC1.3, SC1.5: «Новая версия из файла…» second, one «Скачать книгу…».
  it('lists its items: «Новая версия из файла…» second, one «Скачать книгу…», «Удалить книгу…» last', () => {
    const { menu, props } = renderMenu({ onMove: vi.fn() });
    expect(within(menu).getAllByRole('menuitem').map((item) => item.textContent)).toEqual([
      'Читать с начала', 'Новая версия из файла…', 'Переименовать', 'Переставить…', 'Обложка…', 'Скачать книгу…', 'Удалить книгу…',
    ]);
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Переставить…' }));
    expect(props.onMove).toHaveBeenCalledOnce();
    expect(document.activeElement).toBe(within(menu).getByRole('menuitem', { name: 'Читать с начала' }));
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Переименовать' }));
    expect(props.onRename).toHaveBeenCalledOnce();
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Скачать книгу…' }));
    expect(props.onDownload).toHaveBeenCalledOnce();
  });

  // specs/2026-11-11-ux7-reorder.md RK2.1, RK2.2: a saga of one book has nowhere to move it; while the books are read the
  // item stays, dimmed, with its reason.
  it('moves no book of a saga of one book, and says why «Переставить…» waits while the books are read', () => {
    const { menu } = renderMenu();
    expect(within(menu).queryByRole('menuitem', { name: /Переставить/u })).toBeNull();
    cleanup();
    const onMove = vi.fn();
    const waiting = renderMenu({ moveReason: 'Пока помощник читает книги, переставить нельзя.', onMove });
    const item = within(waiting.menu).getByRole('menuitem', { name: /Переставить…/u });
    expect(item.getAttribute('aria-disabled')).toBe('true');
    expect(item.textContent).toContain('Пока помощник читает книги, переставить нельзя.');
    fireEvent.click(item);
    expect(onMove).not.toHaveBeenCalled();
  });

  it('gives a reader of the saga only the reading', () => {
    const { menu, props } = renderMenu({ canEdit: false });
    expect(within(menu).getAllByRole('menuitem').map((item) => item.textContent)).toEqual(['Читать с начала']);
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Читать с начала' }));
    expect(props.onReadFromStart).toHaveBeenCalledOnce();
  });

  it('keeps an item that waits in its place, dimmed, with its reason, and does not run it', () => {
    const { menu, props } = renderMenu({ deleteReason: 'Пока книга обновляется из файла, удалить её нельзя.', updateReason: 'Книга уже обновляется из файла.' });
    const update = within(menu).getByRole('menuitem', { name: /Новая версия из файла…/u });
    expect(update.getAttribute('aria-disabled')).toBe('true');
    expect(update.textContent).toContain('Книга уже обновляется из файла.');
    fireEvent.click(update);
    expect(props.onUpdateFromFile).not.toHaveBeenCalled();
    const remove = within(menu).getByRole('menuitem', { name: 'Удалить книгу…' });
    expect(remove.getAttribute('aria-disabled')).toBe('true');
    expect(within(menu).getByText('Пока книга обновляется из файла, удалить её нельзя.')).toBeTruthy();
    fireEvent.click(remove);
    expect(props.onDelete).not.toHaveBeenCalled();
  });

  // specs/2026-11-12-ux10-book-revert.md BR3.1: the last update of the book stands first in «⋯» for its 7 days, with when it was.
  it('offers «Что изменилось» of the last update first, with when it was', () => {
    const onOpen = vi.fn();
    const { menu } = renderMenu({ updated: { summary: 'Обновлена из файла сегодня в 14:20', onOpen } });
    const items = within(menu).getAllByRole('menuitem');
    expect(items[0]!.textContent).toBe('Что изменилосьОбновлена из файла сегодня в 14:20');
    fireEvent.click(items[0]!);
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it('returns the focus to its «⋯» by Esc', async () => {
    function Harness() {
      const [open, setOpen] = useState(false);
      return <>
        <button onClick={() => setOpen(true)} type="button">Действия книги</button>
        {open ? <BookActionMenu anchor={{ bottom: 40, left: 0, right: 28, top: 12 }} book={book(1)} canEdit deleteReason={null} onChangeCover={vi.fn()}
          onClose={() => setOpen(false)} onDelete={vi.fn()} onDownload={vi.fn()} onReadFromStart={vi.fn()} onRename={vi.fn()} state="open" updateReason={null} /> : null}
      </>;
    }
    render(<Harness />);
    const trigger = screen.getByRole('button', { name: 'Действия книги' });
    trigger.focus();
    fireEvent.click(trigger);
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' });
    await waitFor(() => expect(document.activeElement).toBe(trigger));
  });

  it('stands under its button, over it, or scrolls inside — whole in the window by its own height', () => {
    const window = { width: 1440, height: 640 };
    const anchor = { bottom: 228, left: 930, right: 958, top: 200 };
    expect(placeMenu(anchor, 300, window)).toEqual({ left: 700, top: 236, maxHeight: 396 });
    expect(placeMenu({ ...anchor, top: 560, bottom: 588 }, 420, window)).toEqual({ left: 700, top: 132, maxHeight: 544 });
    expect(placeMenu({ ...anchor, top: 300, bottom: 328 }, 420, window)).toEqual({ left: 700, top: 336, maxHeight: 296 });
    expect(placeMenu({ ...anchor, top: 400, bottom: 428 }, 420, window)).toEqual({ left: 700, top: 8, maxHeight: 384 });
    expect(placeMenu({ ...anchor, right: 40 }, 300, window).left).toBe(8);
  });
});
