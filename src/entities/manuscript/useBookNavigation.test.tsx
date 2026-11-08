import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { bookId, projectId } from '../../api/mock/repository/fixtures';
import { useBookNavigation } from './useBookNavigation';

function Navigation(props: Parameters<typeof useBookNavigation>[0]) {
  const navigation = useBookNavigation(props);
  return <><button onClick={navigation.onOpenReader}>Чтение</button>
    {navigation.picker}</>;
}

function setup(overrides: Partial<Parameters<typeof useBookNavigation>[0]> = {}) {
  const props = { api: createMockApiClient(), navigate: vi.fn(), projectId, ...overrides };
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={queryClient}><Navigation {...props} /></QueryClientProvider>);
  return { ...view, ...props, rerenderNavigation: (next: Partial<typeof props>) => {
    view.rerender(<QueryClientProvider client={queryClient}><Navigation {...props} {...next} /></QueryClientProvider>);
  } };
}

describe('book navigation', () => {
  it('dismisses the picker when project changes and does not revive stale selection', () => {
    const { rerenderNavigation } = setup();
    fireEvent.click(screen.getByRole('button', { name: 'Чтение' }));
    expect(screen.getByRole('dialog')).toBeTruthy();
    rerenderNavigation({ projectId: 'another-project' });
    expect(screen.queryByRole('dialog')).toBeNull();
    rerenderNavigation({ projectId });
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('uses a known book and chapter through the supplied guarded navigator', () => {
    const { navigate } = setup({ bookId, chapterId: 'chapter-2' });
    fireEvent.click(screen.getByRole('button', { name: 'Чтение' }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId, bookId, chapterId: 'chapter-2', mode: 'read' });
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('requires explicit selection when no current book is known', async () => {
    const { navigate, api } = setup();
    const books = await api.listBooks(projectId);
    fireEvent.click(screen.getByRole('button', { name: 'Чтение' }));
    const picker = await screen.findByRole('dialog', { name: 'Книга для чтения' });
    expect(navigate).not.toHaveBeenCalled();
    fireEvent.click(await within(picker).findByRole('button', { name: books.data[0]!.title }));
    expect(navigate).toHaveBeenCalledWith('/manuscript/books', { projectId, bookId: books.data[0]!.id, mode: 'read' });
  });

  it('keeps empty, loading and failed lists from choosing a fallback book', async () => {
    const api = createMockApiClient();
    vi.spyOn(api, 'listBooks').mockRejectedValue(new Error('offline'));
    const { navigate } = setup({ api });
    fireEvent.click(screen.getByRole('button', { name: 'Чтение' }));
    expect(await screen.findByRole('alert')).toBeTruthy();
    expect(navigate).not.toHaveBeenCalled();
    api.listBooks = vi.fn().mockResolvedValue({ data: [], meta: { hasMore: false, nextCursor: null } });
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
    expect(await screen.findByText('В этой саге пока нет книг.')).toBeTruthy();
    expect(navigate).not.toHaveBeenCalled();
  });
});
