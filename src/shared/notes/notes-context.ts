import { createContext, useContext } from 'react';
import type { PersonalNoteSource } from '../api';
import type { RouteNavigate } from '../navigation/app-route';

export interface NoteCapture {
  body: string;
  source?: PersonalNoteSource | null;
}

export interface NotesActions {
  open: () => void;
  openWithNavigation: (navigate: RouteNavigate) => void;
  capture: (draft: NoteCapture, navigate?: RouteNavigate) => void;
}

export const NotesContext = createContext<NotesActions | null>(null);
export function useNotes() { return useContext(NotesContext); }
