import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { PropsWithChildren } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Book, ManuscriptApiClient } from '../../../entities/manuscript/api';
import { BookCover } from './BookCover';
import { BookCoverEditor } from './BookCoverEditor';

function book(overrides: Partial<Book> = {}): Book {
  return {
    id: 'book-1',
    projectId: 'project-1',
    title: 'Книга I. Белый порт',
    subtitle: 'Черновая редакция',
    order: 1,
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
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  const onClose = vi.fn();
  const onNotice = vi.fn();
  const onUpdated = vi.fn();
  render(<BookCoverEditor api={api} book={source} onClose={onClose} onNotice={onNotice} onUpdated={onUpdated} />, { wrapper });
  return { onClose, onNotice, onUpdated, queryClient };
}

function imageFile(name = 'cover.png', type = 'image/png') {
  return new File(['cover-bytes'], name, { type });
}

function selectImage(file: File) {
  fireEvent.click(screen.getByRole('tab', { name: 'Изображение' }));
  fireEvent.change(screen.getByLabelText('Изображение обложки'), { target: { files: [file] } });
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
    expect(screen.getByText('Белый порт').parentElement?.getAttribute('data-has-image')).toBe('false');
    expect(screen.getByText('Белый порт')).toBeTruthy();

    view.rerender(<BookCover book={{ ...source, coverUrl: '/api/v1/books/book-1/cover/2' }} />);
    expect(screen.getByRole('img', { name: 'Обложка Книга I. Белый порт' }).getAttribute('src'))
      .toBe('/api/v1/books/book-1/cover/2');
  });

  it('keeps a solid cover when the authoritative book has no image', () => {
    render(<BookCover book={book()} />);
    expect(screen.queryByRole('img')).toBeNull();
    expect(screen.getByText('Белый порт').parentElement?.getAttribute('data-has-image')).toBe('false');
  });
});

describe('BookCoverEditor', () => {
  it('rejects explicit unsupported MIME and oversized files before decoding or upload', async () => {
    const createImageBitmap = vi.fn();
    vi.stubGlobal('createImageBitmap', createImageBitmap);
    const uploadBookCover = vi.fn();
    const api = { uploadBookCover } as unknown as ManuscriptApiClient;
    renderEditor(api);

    selectImage(imageFile('cover.gif', 'image/gif'));
    expect(await screen.findByText('Поддерживаются только JPG, PNG и WEBP.')).toBeTruthy();

    const oversized = imageFile();
    Object.defineProperty(oversized, 'size', { value: 5 * 1024 * 1024 + 1 });
    fireEvent.change(screen.getByLabelText('Изображение обложки'), { target: { files: [oversized] } });
    expect(await screen.findByText('Файл больше 5 МБ.')).toBeTruthy();
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

    expect(await screen.findByText('Изображение должно быть не меньше 600×900.')).toBeTruthy();
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
    const { onClose, onNotice, onUpdated } = renderEditor(api);

    selectImage(selected);
    expect(await screen.findByText(/cover\.bin ·/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));

    await waitFor(() => expect(uploadBookCover).toHaveBeenCalledWith(
      'book-1',
      selected,
      expect.objectContaining({ onUploadProgress: expect.any(Function) }),
    ));
    expect(close).toHaveBeenCalledTimes(1);
    expect(onUpdated).toHaveBeenCalledWith(updated);
    expect(onNotice).toHaveBeenCalledWith('Обложка книги обновлена');
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
    expect(await screen.findByRole('button', { name: 'Повторить' })).toBeTruthy();
    expect(screen.getByText(/cover\.png ·/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));

    await waitFor(() => expect(uploadBookCover).toHaveBeenCalledTimes(2));
    expect(uploadBookCover.mock.calls[1]?.[1]).toBe(selected);
  });

  it('requires irreversible confirmation before removing the active image', async () => {
    const source = book({ coverUrl: '/api/v1/books/book-1/cover/1' });
    const updated = { ...source, coverUrl: null };
    const deleteBookCover = vi.fn(async () => updated);
    const api = { deleteBookCover } as unknown as ManuscriptApiClient;
    const { onUpdated } = renderEditor(api, source);
    fireEvent.click(screen.getByRole('tab', { name: 'Изображение' }));

    fireEvent.click(screen.getByRole('button', { name: 'Убрать' }));

    expect(screen.getByText('Изображение будет удалено без возможности восстановления.')).toBeTruthy();
    expect(deleteBookCover).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Удалить изображение' }));

    await waitFor(() => expect(deleteBookCover).toHaveBeenCalledWith('book-1'));
    expect(onUpdated).toHaveBeenCalledWith(updated);
  });
});
