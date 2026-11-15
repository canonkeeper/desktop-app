import { FolderInput, Upload } from 'lucide-react';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import { Button, type ButtonProps } from '../../../ui/controls/Button';
import type { ShelfSlot } from './shelf-slot';
import styles from './ManuscriptShelfPage.module.css';

/** A second source of books the build gives beside «Добавить книги», ready to open from «Книги». */
export interface BookSource {
  label: string;
  caption: string;
  onOpen: () => void;
}

/**
 * The second source of books of the build — «Из Obsidian…» of the app (specs/2026-11-15-desk-obsidian-web.md OW1.2): for an author
 * who may change the saga, not for the example; it leads with the way back to the books. The site gives none.
 */
export function bookSourceOf(slot: ShelfSlot | null, projectId: string | null, navigate: RouteNavigate): BookSource | null {
  const source = slot?.bookSource;
  if (!source || !projectId) return null;
  return {
    label: source.label, caption: source.caption,
    onOpen: () => {
      const at = source.at(projectId);
      navigate(at.path, at.query, { from: { section: 'books' } });
    },
  };
}

/**
 * «Добавить книги» of the first steps, the same entry as the top of the shelf (UP1.1): with a second source of the build, the two
 * buttons side by side, «или перетащите…» and the caption of the source under them.
 */
export function AddBooks({ importReason, onChoose, source }: { importReason: ButtonProps['disabledReason']; onChoose: () => void; source: BookSource | null }) {
  return (
    <div className={styles.addBooks} data-source={source ? '' : undefined}>
      <Button disabledReason={importReason} onClick={onChoose} variant="primary">
        <Upload aria-hidden="true" size={16} />Добавить книги
      </Button>
      {source ? <Button onClick={source.onOpen} variant="outline"><FolderInput aria-hidden="true" size={16} />{source.label}</Button> : null}
      <span>или перетащите файлы на страницу</span>
      {source ? <p className={styles.sourceCaption}>{source.caption}</p> : null}
    </div>
  );
}
