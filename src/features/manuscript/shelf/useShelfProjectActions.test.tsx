import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook } from '@testing-library/react';
import type { PropsWithChildren } from 'react';
import { describe, expect, it, vi } from 'vitest';
import type { ManuscriptApiClient, Project, RestoredSource } from '../../../entities/manuscript/api';
import { useShelfProjectActions } from './useShelfProjectActions';

describe('useShelfProjectActions restore destinations', () => {
  it('opens a restored chapter in the published Reader', async () => {
    const navigate = vi.fn();
    const queryClient = new QueryClient();
    const wrapper = ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useShelfProjectActions({
      api: {} as ManuscriptApiClient,
      navigate,
      project: { id: 'project-1' } as Project,
      resetForProject: vi.fn(),
      selectedBookId: 'book-1',
      setNotice: vi.fn(),
      setProjectMenuOpen: vi.fn(),
      setSelectedBookId: vi.fn(),
    }), { wrapper });

    await act(() => result.current.handleRestoredSource({
      kind: 'chapter',
      resource: { id: 'chapter-1', bookId: 'book-1' },
    } as RestoredSource));

    expect(navigate).toHaveBeenCalledWith('/manuscript/books', {
      projectId: 'project-1',
      bookId: 'book-1',
      chapterId: 'chapter-1',
      mode: 'read',
      sourceView: 'published',
    });
  });
});
