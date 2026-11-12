import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook } from '@testing-library/react';
import type { PropsWithChildren } from 'react';
import { describe, expect, it, vi } from 'vitest';
import type { ManuscriptApiClient, Project } from '../../../entities/manuscript/api';
import { useSagaSettings } from './useSagaActions';

function wrapper({ children }: PropsWithChildren) {
  return <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>;
}

const project = { id: 'project-1', title: 'Северный архив' } as Project;

// specs/2026-11-10-ux2-shell.md S3.2, specs/2026-11-10-ux1-shelf.md UXS4.2: «Настроить сагу» renames and deletes the saga.
describe('the settings of a saga', () => {
  it('tells the deleted saga to the page after the shelf leaves it', async () => {
    const navigate = vi.fn();
    const onDeleted = vi.fn();
    const api = { deleteProject: vi.fn(async () => undefined) } as unknown as ManuscriptApiClient;
    const { result } = renderHook(() => useSagaSettings({ api, navigate, onDeleted }), { wrapper });
    await act(async () => { expect(await result.current.remove(project)).toBeNull(); });
    expect(navigate).toHaveBeenCalledWith('/manuscript/books');
    expect(onDeleted).toHaveBeenCalledWith(project);
  });

  it('renames by the operation of the saga and says an empty name without a request', async () => {
    const updateProject = vi.fn(async () => project);
    const api = { updateProject } as unknown as ManuscriptApiClient;
    const { result } = renderHook(() => useSagaSettings({ api, navigate: vi.fn(), onDeleted: vi.fn() }), { wrapper });
    await act(async () => { expect(await result.current.rename(project, '   ')).toBe('Введите название саги.'); });
    expect(updateProject).not.toHaveBeenCalled();
    await act(async () => { expect(await result.current.rename(project, ' Южный архив ')).toBeNull(); });
    expect(updateProject).toHaveBeenCalledWith('project-1', { title: 'Южный архив' });
  });
});
