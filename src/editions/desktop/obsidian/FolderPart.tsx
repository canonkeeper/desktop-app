// SPDX-License-Identifier: AGPL-3.0-only
import { useQueryClient } from '@tanstack/react-query';
import { FolderOpen, FolderSearch, RotateCw, Unlink } from 'lucide-react';
import { useEffect, useState } from 'react';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import { desktopShell } from '../../../shared/desktop/shell';
import { Button } from '../../../ui/controls/Button';
import { Alert } from '../../../ui/feedback/Alert';
import { MoreMenu } from '../../../ui/overlays/MoreMenu';
import { obsidianKeys, problemCode, vaultQueryOptions, type ObsidianClient, type ObsidianVault } from './obsidian-api';
import { choiceRefusalWords, words } from './words';
import styles from './Obsidian.module.css';

/**
 * The choice of the folder of the saga (specs/2026-11-15-desk-obsidian-web.md OW1.3, OW1.4): the number of the server, then
 * the window of the system by the shell. How it ended is said only by the answer of `pickFolder` in this tab — `choosing` and
 * `refused` of the folder of the saga are not read: the first stays after a cancel, the second does not know the refusals of
 * the shell. `expired` reads the folder of the saga again: the link may be written after the shell stopped waiting.
 */
export function useFolderChoice(api: Pick<ObsidianClient, 'beginObsidianVaultChoice' | 'getObsidianVault'>, projectId: string, onChanged: () => void) {
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const [said, setSaid] = useState<string | null>(null);
  const shell = desktopShell();

  async function choose(before: ObsidianVault | undefined) {
    if (!shell || open) return;
    const changing = before?.linked === true;
    setSaid(null);
    let request: string;
    try {
      request = (await api.beginObsidianVaultChoice(projectId)).request;
    } catch (error) {
      setSaid(problemCode(error) === 'obsidian.choice_unavailable' ? words.unavailable : publicApiErrorMessage(error, words.unavailable));
      return;
    }
    setOpen(true);
    let outcome;
    try {
      outcome = await shell.pickFolder(request);
    } catch {
      outcome = { outcome: 'expired' as const };
    } finally {
      setOpen(false);
    }
    switch (outcome.outcome) {
      case 'linked':
        onChanged();
        await client.invalidateQueries({ queryKey: obsidianKeys.vault(projectId) });
        return;
      case 'refused':
        setSaid(choiceRefusalWords[outcome.reason]);
        return;
      case 'canceled':
        return;
      default: {
        const fresh = await client.fetchQuery({ ...vaultQueryOptions(api, projectId), staleTime: 0 }).catch(() => before);
        if (fresh?.linked && (!changing || fresh.path !== before?.path)) {
          onChanged();
          return;
        }
        setSaid(changing ? words.expiredChange : words.expired);
      }
    }
  }

  return { available: shell !== null, open, said, choose };
}

type FolderChoiceControl = ReturnType<typeof useFolderChoice>;

/** The folder is not chosen (OW1.3; board 44·48): which folder, what the app reads in it, and «Выбрать папку Obsidian…». */
export function VaultStart({ choice, vault }: { choice: FolderChoiceControl; vault: ObsidianVault | undefined }) {
  return (
    <section aria-label={words.choose} className={styles.start}>
      <p className={styles.text}>{words.startText}</p>
      {choice.available ? (
        <Button disabledReason={choice.open ? words.windowOpen : null} onClick={() => void choice.choose(vault)} variant="primary">
          <FolderSearch aria-hidden="true" size={16} />{words.choose}
        </Button>
      ) : null}
      {choice.said ? <p className={styles.error} role="alert">{choice.said}</p> : null}
    </section>
  );
}

/**
 * The line of the chosen folder (OW1.4; board 44·49): its name, its whole path — the one absolute path the page shows, never
 * kept —, «Сменить папку…» with its note, and «⋯» → «Отвязать папку…».
 */
export function FolderLine({ choice, onUnlink, vault }: { choice: FolderChoiceControl; onUnlink: () => void; vault: ObsidianVault }) {
  return (
    <section aria-label={words.folderName(vault.name ?? '')} className={styles.folderLine}>
      <FolderOpen aria-hidden="true" size={18} />
      <div className={styles.folderName}>
        <strong>{words.folderName(vault.name ?? '')}</strong>
        <span className={styles.path}>{vault.path}</span>
      </div>
      <div className={styles.folderSide}>
        <div className={styles.folderButtons}>
          {choice.available ? (
            <Button disabledReason={choice.open ? words.windowOpen : null} onClick={() => void choice.choose(vault)} size="sm" variant="outline">
              <FolderSearch aria-hidden="true" size={14} />{words.change}
            </Button>
          ) : null}
          <MoreMenu items={[{ label: words.unlinkItem, icon: Unlink, run: onUnlink }]} label={words.folderActions} />
        </div>
        <p className={styles.muted}>{words.changeNote}</p>
        {choice.said ? <p className={styles.error} role="alert">{choice.said}</p> : null}
      </div>
    </section>
  );
}

const unknownRereads = 3;

/**
 * Whether the folder opens now (OW1.5): `missing` — the block «Папка не найдена» with «Проверить снова» and «Сменить
 * папку…»; `unknown` — «Проверяю папку…», read again in 2 s up to three times, then «Папка отвечает медленно» while the reads
 * stay open: their own terms and words say the rest.
 */
export function useAvailability(vault: ObsidianVault | undefined, reread: () => void) {
  const [rereads, setRereads] = useState(0);
  const unknown = vault?.availability === 'unknown';
  useEffect(() => {
    if (!unknown) {
      setRereads(0);
      return undefined;
    }
    if (rereads >= unknownRereads) return undefined;
    const timer = window.setTimeout(() => {
      setRereads((count) => count + 1);
      reread();
    }, 2_000);
    return () => window.clearTimeout(timer);
  }, [unknown, rereads, reread]);
  return unknown ? (rereads >= unknownRereads ? 'slow' : 'checking') : vault?.availability === 'missing' ? 'missing' : 'available';
}

export function Availability({ choice, onCheck, state, vault }: {
  choice: FolderChoiceControl;
  onCheck: () => void;
  state: ReturnType<typeof useAvailability>;
  vault: ObsidianVault;
}) {
  if (state === 'checking') return <p className={styles.muted} role="status">{words.checking}</p>;
  if (state === 'slow') return <p className={styles.muted} role="status">{words.slowFolder}</p>;
  if (state !== 'missing') return null;
  return (
    <Alert role="note" tone="warning">
      <p className={styles.text}>{words.missing}</p>
      <div className={styles.row}>
        <Button onClick={onCheck} size="sm" variant="outline"><RotateCw aria-hidden="true" size={14} />{words.checkAgain}</Button>
        {choice.available ? (
          <Button disabledReason={choice.open ? words.windowOpen : null} onClick={() => void choice.choose(vault)} size="sm" variant="ghost">
            <FolderSearch aria-hidden="true" size={14} />{words.change}
          </Button>
        ) : null}
      </div>
    </Alert>
  );
}
