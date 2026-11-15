// SPDX-License-Identifier: AGPL-3.0-only
import { Button } from '../../../ui/controls/Button';
import { Dialog } from '../../../ui/overlays/Dialog';
import { releaseNoteItems, updateWords, whatsNewTitle } from './update-words';
import styles from './Updates.module.css';

/**
 * «Что нового в версии 1.3.0» (specs/2026-11-15-desk-updates-web.md UW2.2; board 44·34, `Pattern/Desktop/Whats New`): the one
 * window of «Что нового» of the part, the strip and the line «обновлён». Each line of the notes is an item; one line stands
 * as a paragraph; a long text scrolls inside the window — by the keyboard too — and «Закрыть» stays in its place.
 */
export function WhatsNew({ notes, onClose, version }: { notes: string; onClose: () => void; version: string | null }) {
  const title = whatsNewTitle(version);
  const items = releaseNoteItems(notes);
  return (
    <Dialog label={title} onDismiss={onClose} width={520}>
      <section className={styles.whatsNew}>
        <h2 className={styles.whatsNewHead} data-overlay-heading="">{title}</h2>
        {/* The region of the text scrolls by the keyboard too: it takes the focus, a list or a long line alike. */}
        <div aria-label={title} className={styles.whatsNewBody} role="region" tabIndex={0}>
          {items.length === 1 ? (
            <p>{items[0]}</p>
          ) : (
            <ul>
              {items.map((item, index) => <li key={`${index}-${item}`}>{item}</li>)}
            </ul>
          )}
        </div>
        <footer className={styles.whatsNewFooter}>
          <Button onClick={onClose} variant="outline">{updateWords.close}</Button>
        </footer>
      </section>
    </Dialog>
  );
}
