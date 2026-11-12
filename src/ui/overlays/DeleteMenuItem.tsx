import { Trash2 } from 'lucide-react';
import type { ButtonHTMLAttributes } from 'react';
import styles from './DeleteMenuItem.module.css';

export interface DeleteMenuItemProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children' | 'role' | 'type'> {
  /** What it deletes, ending in «…»: the item opens the confirmation (PA3.2) — «Удалить книгу…». */
  label: `Удалить ${string}…`;
  /** The voice asks the author's yes before it presses the item (WS10.2); by default the word decides. */
  consent?: 'yes' | 'no';
  /** An action of a sheet of a phone: a button of 44 px, not an item of a menu. */
  sheet?: boolean;
}

/**
 * The deletion of an object in its menu «⋯» (specs/2026-11-10-ux2-patterns-actions.md PA3.2, П-17): the last item, apart
 * from the ordinary ones by a line, red, with its word — it opens the confirmation, it deletes nothing itself.
 */
export function DeleteMenuItem({ className, consent, label, sheet = false, ...props }: DeleteMenuItemProps) {
  return (
    <>
      <div aria-hidden="true" className={styles.rule} data-delete-rule="" />
      <button {...props} className={`${styles.item}${className ? ` ${className}` : ''}`} data-danger="" data-delete-item=""
        data-size={sheet ? 'touch' : undefined} data-voice-consent={consent} role={sheet ? undefined : 'menuitem'} type="button">
        <Trash2 aria-hidden="true" size={16} />
        {label}
      </button>
    </>
  );
}
