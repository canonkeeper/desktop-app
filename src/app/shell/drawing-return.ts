import { createContext, useContext } from 'react';

/**
 * «Вернуть» of a drawing of «Недавно удалённые» (specs/2026-11-11-ux7-trash.md TB3.1): the drawings are the site's, so the
 * site gives the window the return with what it refreshes after it; the desktop has no drawings, and its window none.
 */
export interface DrawingReturn {
  restore: (projectId: string, drawingId: string) => Promise<void>;
}

const DrawingReturnContext = createContext<DrawingReturn | null>(null);

export const DrawingReturnProvider = DrawingReturnContext.Provider;

export function useDrawingReturn() {
  return useContext(DrawingReturnContext);
}
