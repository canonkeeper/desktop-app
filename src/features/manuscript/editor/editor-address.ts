/**
 * The address of the editor of a chapter without the helper (specs/2026-11-13-desk-editor.md ED1.1, ED2.1): a draft by
 * its id, or a new chapter of a book until its first save writes it down. Only the edition of the app routes it.
 */
export const editorPath = '/manuscript/edit';

export function editAt(projectId: string, draftId: string) {
  return { path: editorPath, query: { projectId, draftId } };
}

export function newChapterAt(book: { id: string; projectId: string }) {
  return { path: editorPath, query: { projectId: book.projectId, bookId: book.id } };
}
