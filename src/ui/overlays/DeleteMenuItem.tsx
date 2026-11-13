import { Trash2, type LucideIcon } from 'lucide-react';
import type { ButtonHTMLAttributes } from 'react';
import styles from './DeleteMenuItem.module.css';

export type DeletionLabel = `Удалить ${string}…` | `Отказаться ${string}…`;

export interface DeleteMenuItemProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children' | 'role' | 'type'> {
  /**
   * What it deletes, ending in «…»: the item opens the confirmation (PA3.2) — «Удалить книгу…»; a chapter in work of the
   * editor of the app is given up — «Отказаться от правки…» (specs/2026-11-13-desk-editor.md ED1.4, ED2.4).
   */
  label: DeletionLabel;
  /** The sign before the word: the bin, or × of «Отказаться …» (board 44·20). */
  icon?: LucideIcon;
  /** The voice asks the author's yes before it presses the item (WS10.2); by default the word decides. */
  consent?: 'yes' | 'no';
  /** An action of a sheet of a phone: a button of 44 px, not an item of a menu. */
  sheet?: boolean;
}

/**
 * The deletion of an object in its menu «⋯» (specs/2026-11-10-ux2-patterns-actions.md PA3.2, П-17): the last item, apart
 * from the ordinary ones by a line, red, with its word — it opens the confirmation, it deletes nothing itself.
 */
export function DeleteMenuItem({ className, consent, icon: Icon = Trash2, label, sheet = false, ...props }: DeleteMenuItemProps) {
  return (
    <>
      <div aria-hidden="true" className={styles.rule} data-delete-rule="" />
      <button {...props} className={`${styles.item}${className ? ` ${className}` : ''}`} data-danger="" data-delete-item=""
        data-size={sheet ? 'touch' : undefined} data-voice-consent={consent} role={sheet ? undefined : 'menuitem'} type="button">
        <Icon aria-hidden="true" size={16} />
        {label}
      </button>
    </>
  );
}
