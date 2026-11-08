import { createContext, useContext, type ReactNode } from 'react';
import type { ReaderChapter } from '../../../entities/manuscript/api';
import type { SourceView } from '../../../entities/saga/api';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import type { ToastInput } from '../../../ui/feedback/useToastQueue';
import type { ReaderParagraphDrawings } from './ReaderCore';

export interface ReaderDrawingsOptions {
  projectId: string;
  bookId: string;
  chapter: ReaderChapter | null;
  sourceView: SourceView;
  /** An editor of the saga: may ask for drawings. */
  canEdit: boolean;
  route: { query: URLSearchParams };
  navigate: RouteNavigate;
  toasts: { push: (toast: ToastInput) => number };
}

/** The drawings of the reader on the site (illustrations R8.9, R8.10): what the open chapter shows of them. */
export interface ReaderDrawings {
  /** The key of the open chapter in the saga: the place of the voice call (specs/2026-11-03-voice-assistant.md V2.4). */
  chapterKey: string;
  drawings?: ReaderParagraphDrawings;
  /** «Иллюстрированная книга» in the chapter header. */
  toggle: ReactNode;
  /** On a phone the chapter header is hidden: the switch stands above the text. */
  mobileToggle: ReactNode;
  drawingCount: number;
  drawLocked: boolean;
  /** «Нарисовать эту сцену» with the selected paragraphs as the source of the question. */
  onDrawSelection?: (selected: string[]) => void;
  notice: ReactNode;
  overlays: ReactNode;
}

/** The closed blocks of the reader (specs/2026-11-07-desktop-app.md DA16.4); without them the reader shows no drawings. */
export interface ReaderSlot {
  useDrawings: (options: ReaderDrawingsOptions) => ReaderDrawings;
}

const ReaderSlotContext = createContext<ReaderSlot | null>(null);

export const ReaderSlotProvider = ReaderSlotContext.Provider;

export function useReaderSlot() {
  return useContext(ReaderSlotContext);
}
