// SPDX-License-Identifier: AGPL-3.0-only
import { CircleCheck, ExternalLink, Info, List, RotateCcw } from 'lucide-react';
import { useEffect, useState } from 'react';
import type { CanonKeeperApiClient } from '../../shared/api';
import { desktopShell, type LaunchNotes as Notes } from '../../shared/desktop/shell';
import { Button, ButtonLink } from '../../ui/controls/Button';
import { useToasts } from '../../ui/feedback/useToastQueue';
import { external } from './external';
import { useAppUpdate } from './updates/app-update';
import { releasesUrl, unfinishedLine, updatedLine, updateWords } from './updates/update-words';
import { WhatsNew } from './updates/WhatsNew';

/**
 * The answer of the shell of this load of the page: asked once, however often the root mounts the notices, and said by the
 * notices that stand when it comes — once each: the lines of the launch and the line of an update.
 */
let answer: Promise<Notes> | null = null;
const said = { launch: false, update: false };

/**
 * The lines of a launch of the app (specs/2026-11-13-desk-settings.md ST4.4; board 44·22): once the window opened, the
 * page asks the shell once what happened — the backend fell and was started again, the address of the window changed —
 * and says it in the one area of the toasts. The shell gives the notes once a launch, so a reload says nothing again.
 * After an update (specs/2026-11-15-desk-updates-web.md UW3.3; board 44·34) the line «CanonKeeper обновлён до версии …» —
 * the version that runs, by the one query of the update — with «Что нового» when the notes of the release came; a package
 * of Linux that did not finish is its own line of an error instead. Both stay until closed: the server forgets the notes
 * after the start, and a line missed could not be read anywhere again.
 */
export function LaunchNotes({ api }: { api: Pick<CanonKeeperApiClient, 'getAppUpdate'> }) {
  const toasts = useToasts();
  const [notes, setNotes] = useState<Notes | null>(null);
  const [whatsNew, setWhatsNew] = useState<{ version: string | null; notes: string } | null>(null);
  const update = useAppUpdate(api);
  useEffect(() => {
    const shell = desktopShell();
    if (!shell) return;
    answer ??= shell.launchNotes();
    let standing = true;
    void answer.then((given) => {
      if (!standing) return;
      if (!said.launch) {
        said.launch = true;
        if (given.restartedAfterCrash) toasts.push({ icon: RotateCcw, message: 'CanonKeeper перезапущен после сбоя', tone: 'info' });
        if (given.portChanged) toasts.push({ icon: Info, message: 'Адрес окна сменился: место чтения и вид текста сброшены', tone: 'info' });
      }
      if (!said.update && (given.updatedFrom || given.updateUnfinished)) setNotes(given);
    }, () => undefined);
    return () => {
      standing = false;
    };
  }, [toasts]);
  // The version of «обновлён» is the one that runs: told once its read answered, or failed.
  const known = update.isFetched;
  const version = update.data?.version ?? null;
  useEffect(() => {
    if (!notes || said.update || (!notes.updateUnfinished && !known)) return;
    said.update = true;
    const unfinished = notes.updateUnfinished;
    if (unfinished) {
      toasts.push({
        message: unfinishedLine(unfinished), tone: 'error',
        ...(unfinished.package ? {} : {
          action: (
            <ButtonLink {...external(releasesUrl, desktopShell(), toasts)} size="sm" variant="outline">
              <ExternalLink aria-hidden="true" size={16} />{updateWords.releases}
            </ButtonLink>
          ),
        }),
      });
      return;
    }
    const releaseNotes = notes.releaseNotes?.trim() ? notes.releaseNotes : null;
    toasts.push({
      icon: CircleCheck, message: updatedLine(version, notes.bridgesStopped === true), persistent: true, tone: 'info',
      ...(releaseNotes ? {
        action: (
          <Button onClick={() => setWhatsNew({ version, notes: releaseNotes })} size="sm" variant="ghost">
            <List aria-hidden="true" size={16} />{updateWords.whatsNew}
          </Button>
        ),
      } : {}),
    });
  }, [known, notes, toasts, version]);
  return whatsNew ? <WhatsNew notes={whatsNew.notes} onClose={() => setWhatsNew(null)} version={whatsNew.version} /> : null;
}
