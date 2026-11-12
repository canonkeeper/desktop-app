import { useEffect, useId, useRef, useState } from 'react';
import { useKeyboardAtHand } from '../../../ui/use-narrow-screen';
import styles from './ShelfBooks.module.css';

/** The longest title of a book the server takes (UpdateBookRequest.title). */
const maxTitleLength = 180;
export const emptyTitleReason = 'Название не может быть пустым';

/**
 * «Переименовать» in place of the title of the card (specs/2026-11-11-ux7-shelf.md SF3.1): Enter and leaving the field
 * save, Esc puts the title back; an empty name is not saved and says why. The draft is the field's; the title is the
 * server's — `onSave` answers whether the field may close, or why not.
 */
export function BookTitleField({ onCancel, onSave, saving, title }: {
  onCancel: () => void;
  onSave: (title: string) => Promise<string | null>;
  saving: boolean;
  title: string;
}) {
  const [draft, setDraft] = useState(title);
  const [reason, setReason] = useState<string | null>(null);
  const field = useRef<HTMLInputElement>(null);
  const done = useRef(false);
  const reasonId = useId();
  // Enter and Esc are named only where there is a keyboard (specs/2026-11-11-ux9-shelf-chat.md SC2.3, П-19).
  const keyboard = useKeyboardAtHand();
  useEffect(() => {
    field.current?.focus();
    field.current?.select();
  }, []);

  async function save() {
    if (done.current || saving) return;
    const next = draft.trim();
    if (!next) {
      setReason(emptyTitleReason);
      return;
    }
    if (next === title) {
      done.current = true;
      onCancel();
      return;
    }
    done.current = true;
    const refusal = await onSave(next);
    if (refusal) {
      done.current = false;
      setReason(refusal);
    }
  }

  return (
    <span className={styles.titleField}>
      <input
        aria-describedby={reason ? reasonId : undefined}
        aria-invalid={reason ? true : undefined}
        aria-label={`Название книги «${title}»`}
        className={styles.titleInput}
        disabled={saving}
        maxLength={maxTitleLength}
        onBlur={() => void save()}
        onChange={(event) => {
          setDraft(event.target.value);
          if (reason) setReason(null);
        }}
        onKeyDown={(event) => {
          if (event.key === 'Enter') {
            event.preventDefault();
            void save();
          } else if (event.key === 'Escape') {
            event.preventDefault();
            event.stopPropagation();
            done.current = true;
            onCancel();
          }
        }}
        ref={field}
        value={draft}
      />
      {reason ? <span className={styles.titleReason} id={reasonId} role="alert">{reason}</span>
        : keyboard ? <span className={styles.titleKeys}>Enter — сохранить, Esc — отменить</span> : null}
    </span>
  );
}
