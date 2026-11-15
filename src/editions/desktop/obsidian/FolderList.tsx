// SPDX-License-Identifier: AGPL-3.0-only
import { ChevronDown, ChevronRight, Folder, FolderRoot } from 'lucide-react';
import { useId, useState } from 'react';
import { Button } from '../../../ui/controls/Button';
import { Select } from '../../../ui/controls/Select';
import { ownChoicesBelow, type ChoiceState, type FolderChoice } from './choice';
import { notesLine, words } from './words';
import styles from './Obsidian.module.css';

/** The folders below the second level stand folded under the folder above them (OW2.1). */
const foldedFrom = 2;

/**
 * The folders of the look as lists in lists (specs/2026-11-15-desk-obsidian-web.md OW2.1, OW7.4; board 44·50, `Obsidian Folder
 * Row`): the root first, by the name of the folder; each row — its name, its notes, the word of its book and the choice of
 * what to take, a `Select` named «Что взять из папки «…»» whose closed variant says why by its line; inside the folder of a
 * book of the saga there is no choice (OW2.5). The order is the order of the folders of the look.
 */
export function FolderList({ onChoose, state }: { onChoose: (path: string, choice: FolderChoice) => void; state: ChoiceState }) {
  const [opened, setOpened] = useState<ReadonlySet<string>>(() => new Set());
  const idBase = useId();
  const root = state.order[0];
  if (!root) return null;
  // The choice of a folder is named by its path when another folder of the look has its name (a reader of the screen tells them).
  const named = new Map<string, number>();
  for (const path of state.order) named.set(state.rows.get(path)!.folder.name, (named.get(state.rows.get(path)!.folder.name) ?? 0) + 1);
  const nameOf = (path: string, name: string) => (path !== '.' && (named.get(name) ?? 0) > 1 ? path : name);
  const toggle = (path: string) => setOpened((now) => {
    const next = new Set(now);
    if (next.has(path)) next.delete(path);
    else next.add(path);
    return next;
  });

  function item(path: string) {
    const row = state.rows.get(path)!;
    const { folder } = row;
    const folded = folder.depth >= foldedFrom && row.children.length > 0;
    const open = !folded || opened.has(path);
    // An id of the place of the folder in the look: a path has spaces and slashes an id may not.
    const listId = `${idBase}-${state.order.indexOf(path)}`;
    const inside = folded && !open ? ownChoicesBelow(state, path) : 0;
    return (
      <li key={path}>
        <div className={styles.folderRow}>
          {folder.path === '.' ? <FolderRoot aria-hidden="true" size={16} /> : <Folder aria-hidden="true" size={16} />}
          <span className={styles.folderText}>
            <strong>{folder.name}</strong>
            <span className={styles.muted}>{notesLine(folder.notes, folder.below)}</span>
            {row.book ? <span className={styles.muted}>{row.book.live ? words.bookIn(row.book.title) : words.bookTrash(row.book.title)}</span> : null}
          </span>
          {row.fixed ? <span className={`${styles.folderChoice} ${styles.muted}`}>{row.fixedWord}</span> : (
            <span className={styles.folderChoice}>
              <Select
                label={words.choiceOf(nameOf(folder.path, folder.name))}
                labelHidden
                onValueChange={(value) => onChoose(path, value)}
                options={row.options.map((option) => ({
                  value: option.value, label: option.label, disabled: Boolean(option.reason), ...(option.reason ? { mark: option.reason } : {}),
                }))}
                value={row.choice}
              />
            </span>
          )}
        </div>
        {folded ? (
          <div className={styles.nested}>
            <Button aria-controls={open ? listId : undefined} aria-expanded={open} onClick={() => toggle(path)} size="sm" variant="ghost">
              {open ? <ChevronDown aria-hidden="true" size={14} /> : <ChevronRight aria-hidden="true" size={14} />}
              {words.nested(folder.name, row.children.length)}
            </Button>
            {inside > 0 ? <span className={styles.muted}>{words.inside(inside)}</span> : null}
          </div>
        ) : null}
        {row.children.length > 0 && open ? <ul id={listId}>{row.children.map(item)}</ul> : null}
      </li>
    );
  }

  return <ul aria-label={words.choiceTitle(state.rows.get(root)!.folder.name)} className={styles.folders}>{item(root)}</ul>;
}
