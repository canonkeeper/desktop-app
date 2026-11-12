import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { PropsWithChildren } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Book, ManuscriptApiClient } from '../../../entities/manuscript/api';
import { BookCover } from './BookCover';
import { BookCoverEditor } from './BookCoverEditor';
import { ShelfSlotProvider } from './shelf-slot';
import { ToastsProvider } from '../../../ui/feedback/Toast';

function book(overrides: Partial<Book> = {}): Book {
  return {
    id: 'book-1',
    projectId: 'project-1',
    title: 'Книга I. Белый порт',
    subtitle: 'Черновая редакция',
    order: 1,
    place: 1,
    displayNumber: 'I',
    coverColor: '#285f58',
    coverUrl: null,
    status: 'ready',
    chapterCount: 12,
    wordCount: 42_000,
    createdAt: '2026-08-20T10:00:00.000Z',
    updatedAt: '2026-08-20T10:00:00.000Z',
    ...overrides,
  };
}

function renderEditor(api: ManuscriptApiClient, source = book()) {
  const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  const wrapper = ({ children }: PropsWithChildren) => (
    <QueryClientProvider client={queryClient}><ToastsProvider>{children}</ToastsProvider></QueryClientProvider>
  );
  const onClose = vi.fn();
  render(<BookCoverEditor api={api} book={source} onClose={onClose} />, { wrapper });
  return { onClose, queryClient };
}

function imageFile(name = 'cover.png', type = 'image/png') {
  return new File(['cover-bytes'], name, { type });
}

function selectImage(file: File) {
  fireEvent.change(screen.getByLabelText('Картинка обложки'), { target: { files: [file] } });
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('BookCover', () => {
  it('uses the private returned URL and falls back to color until its version changes', () => {
    const source = book({ coverUrl: '/api/v1/books/book-1/cover/1' });
    const view = render(<BookCover book={source} />);
    const image = screen.getByRole('img', { name: 'Обложка Книга I. Белый порт' });
    expect(image.parentElement?.getAttribute('data-has-image')).toBe('true');

    expect(image.getAttribute('src')).toBe(source.coverUrl);
    expect(document.querySelectorAll(`img[src="${source.coverUrl}"]`)).toHaveLength(2);
    fireEvent.error(image);
    expect(screen.queryByRole('img', { name: /Обложка/ })).toBeNull();
    expect(screen.getByText('Белый порт').closest('[data-has-image]')?.getAttribute('data-has-image')).toBe('false');
    expect(screen.getByText('Белый порт')).toBeTruthy();

    view.rerender(<BookCover book={{ ...source, coverUrl: '/api/v1/books/book-1/cover/2' }} />);
    expect(screen.getByRole('img', { name: 'Обложка Книга I. Белый порт' }).getAttribute('src'))
      .toBe('/api/v1/books/book-1/cover/2');
  });

  it('keeps a solid cover when the authoritative book has no image', () => {
    render(<BookCover book={book()} />);
    expect(screen.queryByRole('img')).toBeNull();
    expect(screen.getByText('Белый порт').closest('[data-has-image]')?.getAttribute('data-has-image')).toBe('false');
  });
});

describe('BookCoverEditor', () => {
  // specs/2026-11-11-ux7-shelf.md SF4.1: the cover of the book shows the colour chosen before anything is saved.
  it('shows the chosen colour on the cover of the book at once and names the colours in words', async () => {
    const updateBook = vi.fn(async (_bookId: string, body: { coverColor?: string }) => book({ coverColor: body.coverColor ?? null }));
    const api = { updateBook } as unknown as ManuscriptApiClient;
    const { onClose } = renderEditor(api);
    const dialog = screen.getByRole('dialog', { name: 'Обложка «Книга I. Белый порт»' });
    const sample = dialog.querySelector<HTMLElement>('[data-cover-sample] [data-has-image]')!;
    expect(sample.style.backgroundColor).toBe('rgb(40, 95, 88)');
    const colours = within(dialog).getByRole('group', { name: 'Цвет' });
    expect(within(colours).getAllByRole('button').map((swatch) => swatch.getAttribute('aria-label')))
      .toEqual(['Синий', 'Сине-зелёный', 'Сливовый', 'Тёмно-зелёный', 'Охра']);
    expect(dialog.textContent).not.toMatch(/#[0-9a-f]{6}/iu);
    expect(within(colours).getByText('Сине-зелёный')).toBeTruthy();

    fireEvent.click(within(colours).getByRole('button', { name: 'Охра' }));
    expect(sample.style.backgroundColor).toBe('rgb(138, 101, 0)');
    expect(within(colours).getByRole('button', { name: 'Охра' }).getAttribute('aria-pressed')).toBe('true');
    expect(updateBook).not.toHaveBeenCalled();

    expect(within(dialog).getAllByRole('button', { name: /Сохранить/u })).toHaveLength(1);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(updateBook).toHaveBeenCalledWith('book-1', { coverColor: '#8a6500' }));
    expect(onClose).toHaveBeenCalledOnce();
    expect(await screen.findByText('Обложка «Книга I. Белый порт» сохранена')).toBeTruthy();
  });

  it('says a picture stands over the colour and offers to take it away', () => {
    renderEditor({} as ManuscriptApiClient, book({ coverUrl: '/api/v1/books/book-1/cover/1' }));
    expect(screen.getByText('Картинка стоит поверх цвета.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Убрать картинку' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Заменить картинку' })).toBeTruthy();
  });

  it('rejects explicit unsupported MIME and oversized files before decoding or upload', async () => {
    const createImageBitmap = vi.fn();
    vi.stubGlobal('createImageBitmap', createImageBitmap);
    const uploadBookCover = vi.fn();
    const api = { uploadBookCover } as unknown as ManuscriptApiClient;
    renderEditor(api);

    selectImage(imageFile('cover.gif', 'image/gif'));
    expect(await screen.findByText('Подходят картинки JPG, PNG и WEBP.')).toBeTruthy();

    const oversized = imageFile();
    Object.defineProperty(oversized, 'size', { value: 5 * 1024 * 1024 + 1 });
    fireEvent.change(screen.getByLabelText('Картинка обложки'), { target: { files: [oversized] } });
    expect(await screen.findByText('Картинка больше 5 МБ.')).toBeTruthy();
    expect(createImageBitmap).not.toHaveBeenCalled();
    expect(uploadBookCover).not.toHaveBeenCalled();
  });

  it('closes every decoded bitmap and rejects dimensions below 600×900', async () => {
    const close = vi.fn();
    vi.stubGlobal('createImageBitmap', vi.fn(async () => ({ width: 599, height: 900, close })));
    const uploadBookCover = vi.fn();
    const api = { uploadBookCover } as unknown as ManuscriptApiClient;
    renderEditor(api);

    selectImage(imageFile());

    expect(await screen.findByText('Картинка должна быть не меньше 600×900.')).toBeTruthy();
    expect(close).toHaveBeenCalledTimes(1);
    expect(uploadBookCover).not.toHaveBeenCalled();
  });

  it('lets an empty MIME reach server sniffing, reports bytes, and trusts the returned Book URL', async () => {
    const close = vi.fn();
    vi.stubGlobal('createImageBitmap', vi.fn(async () => ({ width: 600, height: 900, close })));
    const selected = imageFile('cover.bin', '');
    const updated = book({ coverUrl: '/api/v1/books/book-1/cover/7' });
    const uploadBookCover = vi.fn(async (
      _bookId: string,
      _file: File,
      options: { onUploadProgress?: (progress: { loadedBytes: number; totalBytes: number | null }) => void },
    ) => {
      options.onUploadProgress?.({ loadedBytes: 6, totalBytes: 12 });
      return updated;
    });
    const api = { uploadBookCover } as unknown as ManuscriptApiClient;
    const { onClose } = renderEditor(api);

    selectImage(selected);
    expect(await screen.findByText(/cover\.bin ·/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));

    await waitFor(() => expect(uploadBookCover).toHaveBeenCalledWith(
      'book-1',
      selected,
      expect.objectContaining({ onUploadProgress: expect.any(Function) }),
    ));
    expect(close).toHaveBeenCalledTimes(1);
    expect(await screen.findByText('Обложка «Книга I. Белый порт» сохранена')).toBeTruthy();
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('keeps a selected file for retry after a recoverable upload failure', async () => {
    vi.stubGlobal('createImageBitmap', vi.fn(async () => ({ width: 600, height: 900, close: vi.fn() })));
    const selected = imageFile();
    const updated = book({ coverUrl: '/api/v1/books/book-1/cover/2' });
    const uploadBookCover = vi.fn()
      .mockRejectedValueOnce(new Error('temporary'))
      .mockResolvedValueOnce(updated);
    const api = { uploadBookCover } as unknown as ManuscriptApiClient;
    renderEditor(api);
    selectImage(selected);
    await screen.findByText(/cover\.png ·/);

    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
    expect(await screen.findByText('Не удалось загрузить картинку.')).toBeTruthy();
    expect(screen.getByText(/cover\.png ·/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));

    await waitFor(() => expect(uploadBookCover).toHaveBeenCalledTimes(2));
    expect(uploadBookCover.mock.calls[1]?.[1]).toBe(selected);
  });

  it('requires irreversible confirmation before removing the active image', async () => {
    const source = book({ coverUrl: '/api/v1/books/book-1/cover/1' });
    const updated = { ...source, coverUrl: null };
    const deleteBookCover = vi.fn(async () => updated);
    const api = { deleteBookCover } as unknown as ManuscriptApiClient;
    renderEditor(api, source);

    fireEvent.click(screen.getByRole('button', { name: 'Убрать картинку' }));

    const confirm = screen.getByRole('dialog', { name: 'Убрать картинку обложки?' });
    expect(confirm.textContent).toContain('Вернуть будет нельзя.');
    expect(deleteBookCover).not.toHaveBeenCalled();
    fireEvent.click(within(confirm).getByRole('button', { name: 'Убрать картинку' }));

    await waitFor(() => expect(deleteBookCover).toHaveBeenCalledWith('book-1'));
    expect(await screen.findByText('Картинка обложки «Книга I. Белый порт» убрана')).toBeTruthy();
  });
});

// specs/2026-11-11-ux9-shelf-chat.md SC1.6 (L-11): the cover from a drawing of the saga is chosen in the window of the cover.
describe('«Выбрать из рисунков саги»', () => {
  it('shows the drawing chosen on the cover and saves it by «Сохранить»', async () => {
    const setBookCoverFromImage = vi.fn(async () => book({ coverUrl: '/covers/book-1?v=2' }));
    const api = { setBookCoverFromImage } as unknown as ManuscriptApiClient;
    const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
    render(<QueryClientProvider client={queryClient}><ToastsProvider>
      <ShelfSlotProvider value={{
        CoverDrawings: ({ onChoose }) => (
          <button onClick={() => onChoose({ number: 12, title: 'Мара у пристани', picture: '/drawings/12/screen' })} type="button">Мара у пристани</button>
        ),
      }}>
        <BookCoverEditor api={api} book={book()} onClose={vi.fn()} />
      </ShelfSlotProvider>
    </ToastsProvider></QueryClientProvider>);
    const dialog = screen.getByRole('dialog', { name: 'Обложка «Книга I. Белый порт»' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Выбрать из рисунков саги' }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Мара у пристани' }));
    expect(within(dialog).getByText('Рисунок «Мара у пристани» станет обложкой таким, какой он сейчас.')).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(setBookCoverFromImage).toHaveBeenCalledWith('book-1', 12));
  });

  it('is not offered by a build without drawings', () => {
    renderEditor({} as ManuscriptApiClient);
    expect(screen.queryByRole('button', { name: 'Выбрать из рисунков саги' })).toBeNull();
  });
});
