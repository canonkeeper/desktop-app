import { createContext, useContext, type ReactNode } from 'react';
import type { ReaderChapter } from '../../../entities/manuscript/api';
import type { SourceView } from '../../../entities/saga/api';
import type { BackFrom, RouteNavigate } from '../../../shared/navigation/app-route';
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
  /** The way back of a step from the reader: «← К главе «…»» over «Рисование» leads back to this place (RD3.2, PS4.1). */
  from: BackFrom;
}

/** «Нарисовать» of a selection (RD3.2): the words, where they stand on the screen, their paragraphs and their place in words. */
export interface DrawSelection {
  text: string;
  rect: DOMRect;
  paragraphIds: string[];
  /** «Книга 2, гл. 12 «Белый порт», абзацы 3–4». */
  place: string;
}

/** The drawings of the reader on the site (illustrations R8.9, R8.10; specs/2026-11-10-ux7-reader.md RD1.8, RD3.2, RD3.3). */
export interface ReaderDrawings {
  /** The key of the open chapter in the saga: the place of the voice call (specs/2026-11-03-voice-assistant.md V2.4). */
  chapterKey: string;
  drawings?: ReaderParagraphDrawings;
  /** «Показывать рисунки в тексте» of «⋯»; null while the saga has no drawings and the author may not draw. */
  inText: { shown: boolean; set: (shown: boolean) => void } | null;
  /** The tab «Рисунки» of «Содержание»; null — the chapter has none. */
  tab: ReactNode;
  drawingCount: number;
  drawLocked: boolean;
  /** «Нарисовать» of a selection: the window at the selection; a drawing is made only by its own «Нарисовать». */
  onDrawSelection?: (selection: DrawSelection) => void;
  notice: ReactNode;
  overlays: ReactNode;
}

/** The closed blocks of the reader (specs/2026-11-07-desktop-app.md DA16.4); without them the reader shows no drawings. */
export interface ReaderSlot {
  useDrawings?: (options: ReaderDrawingsOptions) => ReaderDrawings;
  /**
   * Where «Править главу» opens the draft of the chapter: the editor of the desktop app without the helper
   * (specs/2026-11-13-desk-editor.md ED1.1); without it — «Письмо» of the site, as before.
   */
  editAt?: (projectId: string, draftId: string) => { path: string; query: Record<string, string> };
}

const ReaderSlotContext = createContext<ReaderSlot | null>(null);

export const ReaderSlotProvider = ReaderSlotContext.Provider;

export function useReaderSlot() {
  return useContext(ReaderSlotContext);
}
