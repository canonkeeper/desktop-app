// SPDX-License-Identifier: AGPL-3.0-only
import { FileUp, RotateCcw, Save, Square } from 'lucide-react';
import { forwardRef, useId, useRef, useState, type ReactNode } from 'react';
import { loadFailureWords } from '../../../entities/api-errors';
import { useManuscriptShelfQuery } from '../../../entities/manuscript/api';
import { useSagaFileSave } from '../../../entities/saga-file/api';
import type { CanonKeeperApiClient, Project } from '../../../shared/api';
import { Button } from '../../../ui/controls/Button';
import { formatCount } from '../../../ui/copy';
import { LongWork } from '../../../ui/feedback/LongWork';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { Confirm } from '../../../ui/overlays/Confirm';
import settings from '../settings/Settings.module.css';
import { useSagaFileOpen } from './use-saga-file-open';
import { saveWords, sagaFileWords as words } from './words';
import styles from './SagaFile.module.css';

/** The window of the browser for a saga file (SW2.1): opened by its button; the voice does not open it. */
export const SagaFilePicker = forwardRef<HTMLInputElement, { onFile: (file: File) => void }>(function SagaFilePicker({ onFile }, ref) {
  return (
    <input accept=".cksaga" aria-hidden="true" className={styles.picker} ref={ref} tabIndex={-1} type="file"
      onChange={(event) => {
        const file = event.currentTarget.files?.[0];
        event.currentTarget.value = '';
        if (file) onFile(file);
      }} />
  );
});

/** The local author owns every saga of the app; a saga of another owner is not his to save (SF2.1). */
function ownedOf(projects: readonly Project[] | undefined) {
  return (projects ?? []).filter((project) => project.currentMembership.role === 'owner');
}

/** A button of the file with the reason it is closed standing under it, as the board draws it (`Disabled Reason`). */
export function FileButton({ icon, label, loading = false, onClick, reason }: {
  icon: ReactNode; label: string; loading?: boolean; onClick: () => void; reason: string | null;
}) {
  const why = useId();
  return (
    <div className={styles.action}>
      <Button data-voice-consent="no" disabledReason={reason ? { text: reason, at: why } : null} loading={loading} onClick={onClick} variant="outline">
        {icon}{label}
      </Button>
      {reason ? <p className={styles.why} id={why}>{reason}</p> : null}
    </div>
  );
}

/**
 * «Файл саги» of «Данные» (specs/2026-11-13-desk-sagafile-web.md SW1, SW2; board 44·11, 44·30, 44·31): «Сохранить сагу в
 * файл…» saves the one saga at once and asks which of several; «Открыть файл саги…» opens the window of files. While a file
 * goes, its line of a long work stands over the buttons — the save with «Остановить» — and its button says why it waits.
 */
export function SagaFileSection({ api }: { api: CanonKeeperApiClient }) {
  const shelf = useManuscriptShelfQuery(api, { projectId: null });
  const sagas = ownedOf(shelf.data?.projects);
  const save = useSagaFileSave(api, saveWords);
  const opening = useSagaFileOpen(api);
  const toasts = useToasts();
  const [picking, setPicking] = useState<string | null>(null);
  const picker = useRef<HTMLInputElement>(null);
  const label = useId();
  const saveReason = save.saving ? words.savingReason : shelf.data && sagas.length === 0 ? words.noSagaReason : null;
  const openReason = opening.opening ? words.openingReason : null;

  function saveOneOf(list: readonly Project[]) {
    if (list.length === 1) save.start({ projectId: list[0]!.id, title: list[0]!.title });
    else if (list.length > 1) setPicking(list[0]!.id);
  }

  function startSave() {
    if (shelf.data) {
      saveOneOf(sagas);
      return;
    }
    // The list of sagas did not read: the press reads it again and goes on by it, or says that it did not come.
    void shelf.refetch().then((result) => {
      if (result.data) {
        saveOneOf(ownedOf(result.data.projects));
        return;
      }
      toasts.push({
        message: words.sagasUnread, detail: loadFailureWords(result.error).cause || undefined, tone: 'error',
        action: <Button onClick={startSave} size="sm" variant="ghost"><RotateCcw aria-hidden="true" size={14} />Повторить</Button>,
      });
    });
  }

  const chosen = sagas.find((saga) => saga.id === picking);
  return (
    <div aria-labelledby={label} className={settings.section} role="group">
      <p className={settings.label} id={label}>{words.section}</p>
      {save.saving ? (
        <LongWork action={<Button onClick={save.stop} size="sm" variant="outline"><Square aria-hidden="true" size={14} />{words.stop}</Button>}
          announcement={words.saving} detail={words.savingLong} label={words.saving} title={words.saving} />
      ) : null}
      {opening.opening ? (
        <LongWork announcement={words.opening(opening.opening.file.name)} detail={words.openingLong} label={words.opening(opening.opening.file.name)}
          title={words.opening(opening.opening.file.name)} />
      ) : null}
      <div className={styles.actions}>
        {/* Which sagas there are comes with the list of sagas: until then — and while it is read again — the button waits. */}
        <FileButton icon={<Save aria-hidden="true" size={16} />} label={words.save} loading={!shelf.data && shelf.isFetching} onClick={startSave}
          reason={saveReason} />
        <FileButton icon={<FileUp aria-hidden="true" size={16} />} label={words.open} onClick={() => picker.current?.click()} reason={openReason} />
      </div>
      <p className={settings.note}>{words.note}</p>
      <SagaFilePicker onFile={opening.open} ref={picker} />
      {chosen ? (
        <Confirm action={words.pickAction} consent="no" onCancel={() => setPicking(null)} title={words.pick}
          actionIcon={<Save aria-hidden="true" size={16} />}
          onConfirm={() => {
            setPicking(null);
            save.start({ projectId: chosen.id, title: chosen.title });
          }}>
          <fieldset className={styles.sagas}>
            <legend className={styles.picker}>{words.pick}</legend>
            {sagas.map((saga) => (
              <label className={styles.saga} key={saga.id}>
                <input checked={saga.id === chosen.id} name="saga-file-saga" onChange={() => setPicking(saga.id)} type="radio" value={saga.id} />
                <span className={styles.sagaText}>
                  <span className={styles.sagaTitle}>{saga.title}</span>
                  <span className={styles.sagaMeta}>{formatCount(saga.bookCount, 'книга', 'книги', 'книг')}</span>
                </span>
              </label>
            ))}
          </fieldset>
        </Confirm>
      ) : null}
    </div>
  );
}
