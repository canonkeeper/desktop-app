import { createContext, useContext, type ReactNode } from 'react';
import type { PersonalNoteSource } from '../api';
import type { RouteNavigate } from '../navigation/app-route';

export interface NoteCapture {
  /** The thought a note starts with; a note of a place starts without one — its words are the cited part (NT1.2). */
  body: string;
  source?: PersonalNoteSource | null;
  /** The place of a selection of the book in words, «Книга II, гл. 12 «Белый порт», абзац 3», while the note is new. */
  place?: string;
}

/** A note beside the text of the reader (specs/2026-11-11-ux7-notes.md NT2.2, NT2.3). */
export interface NoteBeside {
  noteId: string;
  /** ← of the note: back to the notes of the chapter. */
  onBack: () => void;
  /** The navigation of the page: «К месту в книге» of the note goes to its paragraph without a new step of the history. */
  navigate: RouteNavigate;
}

export interface NotesActions {
  open: () => void;
  openWithNavigation: (navigate: RouteNavigate) => void;
  capture: (draft: NoteCapture, navigate?: RouteNavigate) => void;
  /** The window of a note for a place of a page — the panel «Заметки» of the reader; the notes of the root give it. */
  renderNote?: (note: NoteBeside) => ReactNode;
}

export const NotesContext = createContext<NotesActions | null>(null);
export function useNotes() { return useContext(NotesContext); }
