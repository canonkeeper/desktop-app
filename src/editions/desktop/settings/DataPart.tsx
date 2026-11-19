// SPDX-License-Identifier: AGPL-3.0-only
import { Archive, FolderOpen } from 'lucide-react';
import { useEffect, useId, useRef, useState } from 'react';
import { isApiStatusFailure, loadFailureWords } from '../../../entities/api-errors';
import { desktopShell } from '../../../shared/desktop/shell';
import { Button } from '../../../ui/controls/Button';
import { formatFileSize } from '../../../ui/copy';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { SkeletonArea } from '../../../ui/feedback/Skeleton';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { Confirm, deletionLines } from '../../../ui/overlays/Confirm';
import { MoreMenu } from '../../../ui/overlays/MoreMenu';
import { SagaFileSection } from '../saga-file/SagaFileSection';
import { dayWords, useDeleteBackups, useDesktopState, type DesktopState } from './desktop-state';
import { EraseData } from './EraseData';
import type { SettingsPartProps } from './part-props';
import styles from './Settings.module.css';

/**
 * What «Данные» say of the files on the disk, of the removal of the program and of the copies, and «Удалить копии…»
 * (specs/2026-11-19-desk-trust.md TW3.1, TW3.3, TW3.4).
 */
export const dataWords = {
  notEncrypted: 'Файлы здесь не шифруются. Чтобы их не прочли с потерянного или украденного компьютера, включите шифрование диска',
  uninstall: 'Удаление программы эти данные оставляет — в Windows, если не отметить «Удалить данные приложения». Чтобы убрать всё, сначала нажмите «Стереть все данные…»',
  copies: 'В копии — саги, книги и чаты такими, какими они были в день обновления, и то, что вы удалили позже. Копий не больше двух: новая вытесняет самую старую',
  copiesMenu: 'Действия с копиями',
  deleteCopies: 'Удалить копии…',
  question: {
    title: 'Удалить копии перед обновлением?',
    also: 'Саги и книги останутся, но вернуться на прежнюю версию с прежними данными будет нельзя',
    action: 'Удалить копии',
  },
  notDeleted: 'Копии не удалились. Попробуйте ещё раз',
  updating: 'Сейчас ставится обновление — удалите копии после него',
  deleted: 'Копии удалены',
} as const;

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
 * update; no block of copies before the first one; the saga file (specs/2026-11-13-desk-sagafile-web.md SW1, SW2); last,
 * «Стереть все данные…» (specs/2026-11-13-desk-erase.md ER1.1). The files are not encrypted, the removal of the program leaves
 * them, the copies keep what was deleted later, and «Удалить копии…» of the «⋯» of the copies asks first and removes them —
 * the block goes with its «⋯», and the focus stands on «Где лежат данные» (specs/2026-11-19-desk-trust.md TW3).
 */
export function DataPart({ api }: SettingsPartProps) {
  const state = useDesktopState(api);
  const remove = useDeleteBackups(api);
  const toasts = useToasts();
  const shell = desktopShell();
  const where = useId();
  const copies = useId();
  const whereLabel = useRef<HTMLParagraphElement>(null);
  const [asking, setAsking] = useState(false);
  // The copies removed: once their block is gone with its «⋯», the focus stands on «Где лежат данные» (TW3.4).
  const focusWhere = useRef(false);
  const copiesShown = (state.data?.backups.length ?? 0) > 0;
  useEffect(() => {
    if (!focusWhere.current || copiesShown || asking) return;
    focusWhere.current = false;
    whereLabel.current?.focus();
  }, [asking, copiesShown]);
  if (state.isPending) return <SkeletonArea count={4} shape="lines" />;
  if (state.isError) {
    return <ErrorBlock {...loadFailureWords(state.error)} onRetry={() => void state.refetch()} retrying={state.isRefetching} title="Не открылись данные приложения" />;
  }
  const { backups, dataDir, usage } = state.data;
  return (
    <>
      <div aria-labelledby={where} className={styles.block} role="group">
        <p className={styles.label} id={where} ref={whereLabel} tabIndex={-1}>Где лежат данные</p>
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
        <p className={styles.note}>{dataWords.notEncrypted}</p>
        <p className={styles.note}>{dataWords.uninstall}</p>
      </div>
      {backups.length > 0 ? (
        <div className={styles.section}>
          <div className={styles.sectionHead}>
            <p className={styles.label} id={copies}>Копии перед обновлением</p>
            <MoreMenu label={dataWords.copiesMenu} remove={{ label: dataWords.deleteCopies, run: () => setAsking(true) }} />
          </div>
          <ul aria-labelledby={copies} className={styles.rows}>
            {backups.map((backup) => (
              <li className={styles.row} key={backup.madeAt}>
                <Archive aria-hidden="true" className={styles.rowIcon} size={18} />{copyWords(backup)}
              </li>
            ))}
          </ul>
          <p className={styles.note}>{dataWords.copies}</p>
        </div>
      ) : null}
      {asking ? (
        <Confirm
          action={dataWords.question.action}
          also={dataWords.question.also}
          busy={remove.isPending}
          error={remove.isError ? (isApiStatusFailure(remove.error) && remove.error.problem.code === 'desktop.updating' ? dataWords.updating : dataWords.notDeleted) : undefined}
          irreversible
          onCancel={() => {
            setAsking(false);
            remove.reset();
          }}
          onConfirm={() => remove.mutate(undefined, {
            onSuccess: () => {
              focusWhere.current = true;
              setAsking(false);
              toasts.push({ message: dataWords.deleted, tone: 'success' });
            },
          })}
          title={dataWords.question.title}
        >
          {deletionLines.gone}
        </Confirm>
      ) : null}
      {/* «Файл саги» before the erasure (specs/2026-11-13-desk-sagafile-web.md SW1, SW2; board 44·11). */}
      <SagaFileSection api={api} />
      <EraseData api={api} />
    </>
  );
}
