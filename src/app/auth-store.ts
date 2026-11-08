import { create } from 'zustand';
import type { AuthFailureKind } from '../entities/session/api';

export type AuthShellStatus = 'checking' | 'anonymous' | 'authenticated' | 'error';

interface AuthShellState {
  errorKind: AuthFailureKind | null;
  errorMessage: string;
  requestedPath: string;
  clearAuthMessage(): void;
  setFailure(kind: AuthFailureKind, message: string): void;
  setRequestedPath(path: string): void;
}

export const useAuthShellStore = create<AuthShellState>((set) => ({
  errorKind: null,
  errorMessage: '',
  requestedPath: '/manuscript/books',
  clearAuthMessage() {
    set({ errorKind: null, errorMessage: '' });
  },
  setFailure(errorKind, errorMessage) {
    set({ errorKind, errorMessage });
  },
  setRequestedPath(requestedPath) {
    set({ requestedPath });
  },
}));
