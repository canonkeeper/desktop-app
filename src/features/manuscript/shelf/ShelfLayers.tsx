import type { Book, Project } from '../../../entities/manuscript/api';
import { Confirm, deletionLines } from '../../../ui/overlays/Confirm';
import type { PresenceStatus } from '../../../ui/overlays/use-presence';

/** «Удалить книгу…» of the menu of a book (specs/2026-11-10-ux2-patterns-actions.md PA3.1): it comes back in 7 days. */
export function ConfirmDelete({
  book,
  error = null,
  isDeleting,
  onClose,
  onDelete,
  state,
}: {
  book: Book;
  /** Why the book was not deleted, said in the window. */
  error?: string | null;
  isDeleting: boolean;
  onClose: () => void;
  onDelete: () => void;
  state: PresenceStatus;
}) {
  return (
    <Confirm action="Удалить книгу" also="Чаты и заметки останутся." busy={isDeleting} error={error} onCancel={onClose} onConfirm={onDelete}
      state={state} title={`Удалить книгу «${book.title}»?`}>
      {deletionLines.returns}
    </Confirm>
  );
}

/** What comes back with a saga and what does not: the table of specs/2026-11-11-ux7-trash.md (UXS4.4, TB2.1, TB2.3). */
const sagaGoesWith = 'Вернутся книги, главы «Письма», рисунки, карта, «Что помнит помощник» и ваши материалы. '
  + 'Чаты и разговоры с помощником удалятся сразу и не вернутся. Заметки останутся.';

/**
 * «Удалить сагу» of «Настроить сагу» (specs/2026-11-10-ux1-shelf.md UXS4.3, PA3.1): the saga comes back in 7 days with
 * all it holds but the chats, which go at once (the table of UXS4.4).
 */
export function ConfirmProjectDelete({
  error = null,
  isDeleting,
  onClose,
  onDelete,
  project,
  state,
}: {
  /** Why the saga was not deleted, said in the window. */
  error?: string | null;
  isDeleting: boolean;
  onClose: () => void;
  onDelete: () => void;
  project: Project;
  state: PresenceStatus;
}) {
  return (
    <Confirm action="Удалить сагу" also={sagaGoesWith} busy={isDeleting} error={error}
      onCancel={onClose} onConfirm={onDelete} state={state} title={`Удалить сагу «${project.title}»?`}>
      {deletionLines.returns}
    </Confirm>
  );
}
