// SPDX-License-Identifier: AGPL-3.0-only
import { Archive, FolderOpen } from 'lucide-react';
import { useId } from 'react';
import { loadFailureWords } from '../../../entities/api-errors';
import { desktopShell } from '../../../shared/desktop/shell';
import { Button } from '../../../ui/controls/Button';
import { formatFileSize } from '../../../ui/copy';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { SkeletonArea } from '../../../ui/feedback/Skeleton';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { dayWords, useDesktopState, type DesktopState } from './desktop-state';
import type { SettingsPartProps } from './part-props';
import styles from './Settings.module.css';

/** «Занято на диске: …»: the place, and «Считаю…» while the server counts it (ST2.2). */
function usageWords({ bytes, counting }: DesktopState['usage']) {
  if (bytes === null) return 'Считаю…';
  return counting ? `${formatFileSize(bytes)} · Считаю…` : formatFileSize(bytes);
}

/** «12 ноября 2026 · версия 1.1.4»; a copy made before the copies named their version says its day alone (ST2.3). */
function copyWords({ madeAt, version }: DesktopState['backups'][number]) {
  return version ? `${dayWords(madeAt)} · версия ${version}` : dayWords(madeAt);
}

/**
 * «Данные» (specs/2026-11-13-desk-settings.md ST2.4; board 44·11, `Pattern/Desktop/Settings Data`): where the data lie with
 * «Открыть папку» of the shell — no such button in a browser without it — the place they take and the copies before an
 * update; no block of copies before the first one.
 */
export function DataPart({ api }: SettingsPartProps) {
  const state = useDesktopState(api);
  const toasts = useToasts();
  const shell = desktopShell();
  const where = useId();
  const copies = useId();
  if (state.isPending) return <SkeletonArea count={4} shape="lines" />;
  if (state.isError) {
    return <ErrorBlock {...loadFailureWords(state.error)} onRetry={() => void state.refetch()} retrying={state.isRefetching} title="Не открылись данные приложения" />;
  }
  const { backups, dataDir, usage } = state.data;
  return (
    <>
      <div aria-labelledby={where} className={styles.block} role="group">
        <p className={styles.label} id={where}>Где лежат данные</p>
        <div className={styles.pathRow}>
          <p className={styles.path}>{dataDir}</p>
          {shell ? (
            <Button onClick={() => void shell.revealDataDir().catch(() => toasts.push({ message: 'Не удалось открыть папку', tone: 'error' }))}
              size="sm" variant="outline">
              <FolderOpen aria-hidden="true" size={14} />Открыть папку
            </Button>
          ) : null}
        </div>
        <p className={styles.note}>{`Занято на диске: ${usageWords(usage)}`}</p>
      </div>
      {backups.length > 0 ? (
        <div className={styles.section}>
          <p className={styles.label} id={copies}>Копии перед обновлением</p>
          <ul aria-labelledby={copies} className={styles.rows}>
            {backups.map((backup) => (
              <li className={styles.row} key={backup.madeAt}>
                <Archive aria-hidden="true" className={styles.rowIcon} size={18} />{copyWords(backup)}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </>
  );
}
