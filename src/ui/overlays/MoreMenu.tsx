import { Check, Ellipsis, type LucideIcon } from 'lucide-react';
import { useState } from 'react';
import type { ControlSize } from '../controls/Button';
import { IconButton } from '../controls/IconButton';
import { DeleteMenuItem } from './DeleteMenuItem';
import { onMenuButtonKey, Popover } from './Popover';
import styles from './MoreMenu.module.css';

export interface MoreMenuItem {
  /** What the item does: «Изменить», «Загрузить новую версию файла». */
  label: string;
  icon: LucideIcon;
  run: () => void;
  /** A switch of the menu, «Показывать рисунки в тексте»: on or off. */
  checked?: boolean;
}

/**
 * «⋯» of an object (specs/2026-11-10-ux2-patterns-actions.md PA3.2, П-17): its ordinary actions on top, «Удалить …»
 * last and apart. The menu stands in place, under its button.
 */
export function MoreMenu({ buttonClassName, disabled = false, items = [], label, remove, size = 'sm' }: {
  /** The look of the button where the page draws its own, a dark bar of a picture. */
  buttonClassName?: string;
  disabled?: boolean;
  items?: readonly MoreMenuItem[];
  /** The name of the button and of the menu: «Действия записи». */
  label: string;
  remove?: { label: `Удалить ${string}…`; run: () => void };
  size?: ControlSize;
}) {
  const [open, setOpen] = useState(false);
  const run = (action: () => void) => () => {
    setOpen(false);
    action();
  };
  return (
    <div className={styles.wrap}>
      <IconButton aria-expanded={open} aria-haspopup="menu" aria-label={label} className={buttonClassName} disabled={disabled} onClick={() => setOpen((shown) => !shown)}
        onKeyDown={onMenuButtonKey(open, () => setOpen(true))} size={size} title={label}>
        <Ellipsis aria-hidden="true" size={16} />
      </IconButton>
      <Popover className={styles.menu} inPlace label={label} onDismiss={() => setOpen(false)} open={open} role="menu">
        {items.map(({ checked, icon: Icon, label: name, run: action }) => (
          <button aria-checked={checked} className={styles.item} key={name} onClick={run(action)} role={checked === undefined ? 'menuitem' : 'menuitemcheckbox'} type="button">
            <Icon aria-hidden="true" size={16} />{name}
            {checked ? <Check aria-hidden="true" className={styles.check} size={16} /> : null}
          </button>
        ))}
        {remove ? <DeleteMenuItem label={remove.label} onClick={run(remove.run)} /> : null}
      </Popover>
    </div>
  );
}
