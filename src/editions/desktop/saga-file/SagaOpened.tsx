// SPDX-License-Identifier: AGPL-3.0-only
import { useMutationState } from '@tanstack/react-query';
import { ArrowRight, Info, X } from 'lucide-react';
import { useState } from 'react';
import { sagaFileKeys, type SagaArchiveOpened } from '../../../entities/saga-file/api';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { Dialog } from '../../../ui/overlays/Dialog';
import { openedWords, sagaFileWords as words } from './words';
import styles from './SagaFile.module.css';

/**
 * The window «Сага открыта» (specs/2026-11-13-desk-sagafile-web.md SW2.3; board 44·31, `Pattern/Desktop/Opened Dialog`):
 * the end of an opening of a saga file, wherever the author went while it went — the result of the server, read from the
 * cache of the mutations, once. «Перейти к саге» opens the shelf of the new saga; closing the window leaves the author where
 * he is, the saga in the list of sagas. The notes stand in a body of their own height that scrolls, the button in its place.
 */
export function SagaOpened({ navigate }: { navigate: RouteNavigate }) {
  const ends = useMutationState({
    filters: { mutationKey: sagaFileKeys.open, status: 'success' },
    select: (mutation) => ({ id: mutation.mutationId, opened: mutation.state.data as SagaArchiveOpened }),
  });
  // Which ends the author has closed: a fact of this window only (an ephemeral state of the page).
  const [closed, setClosed] = useState<ReadonlySet<number>>(() => new Set());
  const end = [...ends].reverse().find((each) => !closed.has(each.id));
  if (!end) return null;
  const { message, notes } = openedWords(end.opened);
  const close = () => setClosed(new Set([...closed, ...ends.map((each) => each.id)]));
  return (
    <Dialog label={words.opened} onDismiss={close} width={520}>
      <section className={styles.opened}>
        <div className={styles.head}>
          <h2 data-overlay-heading="">{words.opened}</h2>
          <IconButton aria-label="Закрыть" onClick={close} size="sm"><X aria-hidden="true" size={16} /></IconButton>
        </div>
        <p className={styles.message}>{message}</p>
        {notes.length > 0 ? (
          // The body scrolls by the keyboard too: it takes the focus.
          <ul className={styles.notes} tabIndex={0}>
            {notes.map((note) => (
              <li className={styles.note} key={note.key}>
                <Info aria-hidden="true" className={styles.noteIcon} size={16} />
                <div className={styles.noteBody}>
                  <span>{note.text}</span>
                  {note.places?.length ? <ul className={styles.places}>{note.places.map((place) => <li key={place}>{place}</li>)}</ul> : null}
                </div>
              </li>
            ))}
          </ul>
        ) : null}
        <footer className={styles.footer}>
          <Button onClick={() => { close(); navigate('/manuscript/books', { projectId: end.opened.projectId }); }} variant="primary">
            {words.goToSaga}<ArrowRight aria-hidden="true" size={16} />
          </Button>
        </footer>
      </section>
    </Dialog>
  );
}
