import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useAuthShellStore } from './auth-store';

describe('auth shell store', () => {
  beforeEach(() => {
    useAuthShellStore.getState().clearAuthMessage();
    useAuthShellStore.getState().setRequestedPath('/manuscript/books');
  });

  it('keeps failure message state without persisted session data', () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem');

    useAuthShellStore.getState().setFailure('forbidden', 'Нет доступа к этой странице.');
    expect(useAuthShellStore.getState()).toMatchObject({
      errorKind: 'forbidden',
      errorMessage: 'Нет доступа к этой странице.',
    });

    useAuthShellStore.getState().clearAuthMessage();
    expect(useAuthShellStore.getState()).toMatchObject({ errorKind: null, errorMessage: '' });
    expect(setItem).not.toHaveBeenCalled();
    setItem.mockRestore();
  });

  it('keeps only non-secret auth shell state in memory', () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem');
    useAuthShellStore.getState().setRequestedPath('/manuscript/books');

    const state = useAuthShellStore.getState();
    expect(state.requestedPath).toBe('/manuscript/books');
    expect(setItem).not.toHaveBeenCalled();
    expect(JSON.stringify(state)).not.toContain('ck_session');
    expect(JSON.stringify(state)).not.toContain('password');
    expect('user' in state).toBe(false);

    useAuthShellStore.getState().clearAuthMessage();
    setItem.mockRestore();
  });
});
