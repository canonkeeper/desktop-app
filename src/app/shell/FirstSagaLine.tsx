import { Pencil } from 'lucide-react';
import { Button } from '../../ui/controls/Button';
import styles from './FirstSagaLine.module.css';

/**
 * The line under the top bar of a saga the author did not name (specs/2026-11-10-ux3-first-screen.md FS1.4, FS2.1; board
 * 30·25): «Поговорить» or «Вычитать мою главу» of the first screen founded it as «Моя сага», and the screen says so with
 * «Переименовать» — the window «Настроить сагу».
 */
export function FirstSagaLine({ name, onRename }: { name: string; onRename: () => void }) {
  return (
    <p aria-label="Сага без вашего названия" className={styles.line} role="note">
      <span>Сага «{name}» создана — назовите её по-своему</span>
      <Button onClick={onRename} size="sm" variant="outline"><Pencil aria-hidden="true" size={14} />Переименовать</Button>
    </p>
  );
}
