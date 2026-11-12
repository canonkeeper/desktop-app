import { FileText, FileUp, SkipForward } from 'lucide-react';
import type { AccountQuota } from '../../../entities/account-quota/api';
import { refusalWords } from '../../../entities/account-quota/refusal-words';
import { QuotaRefusal } from '../../../entities/account-quota/ui/QuotaRefusal';
import type { ManuscriptApiClient } from '../../../entities/manuscript/api';
import { Button } from '../../../ui/controls/Button';
import { Badge } from '../../../ui/feedback/Badge';
import { Progress } from '../../../ui/feedback/Progress';
import { SupportReference } from '../../../ui/feedback/SupportReference';
import { useManuscriptJobQuery } from '../api';
import { importFailureMessage } from './import-failure';
import { volumeWord, type VolumeView } from './upload-files';
import styles from './UploadCard.module.css';

export interface UploadCardProps {
  api: Pick<ManuscriptApiClient, 'getJob'>;
  volume: VolumeView;
  shape: 'card' | 'row';
  quota: AccountQuota | undefined;
  /** «Заменить файл» of a volume that waits, «Выбрать файл» of a file that did not reach the server (UP3.2). */
  onFile: (itemId: string, file: File) => void;
  onSkip: (itemId: string) => void;
  skipping: boolean;
}

/**
 * A volume of the upload in the place of its book on the shelf (specs/2026-11-11-ux7-upload.md UP3.1, UP3.2; board
 * 35·23, 35·24): its file and one word — «Ждёт загрузки», «Загружается · 40 %», «Не загрузилась» with its reason and «Заменить
 * файл» · «Пропустить», «Файл не дошёл» with «Выбрать файл». A refusal by a limit after the reading of the file is the one
 * block of refusals (UP3.4).
 */
export function UploadCard({ api, onFile, onSkip, quota, shape, skipping, volume }: UploadCardProps) {
  const job = useManuscriptJobQuery(api, volume.failedJobId ?? '', Boolean(volume.failedJobId)).data;
  const jobCode = job?.error?.code ?? null;
  const code = volume.reasonCode ?? jobCode;
  const limit = code?.startsWith('quota.') ? refusalWords(code, quota) : null;
  const reason = volume.state === 'lost'
    ? 'Файл не дошёл до сервера. Выберите его снова — он встанет на это же место.'
    : volume.state === 'failed'
      ? importFailureMessage(code) ?? (limit ? null : volume.reasonCode
        ? 'Файл не прошёл проверку. Выберите другой файл книги.'
        : 'Не удалось загрузить книгу — это сбой на нашей стороне. Замените файл тем же, чтобы повторить; не поможет — напишите нам.')
      : null;
  const word = volumeWord(volume);
  const fileInput = volume.itemId && (volume.state === 'failed' || volume.state === 'lost') ? (
    <label className={styles.file}>
      <FileUp aria-hidden="true" size={15} />{volume.state === 'lost' ? 'Выбрать файл' : 'Заменить файл'}
      <input
        aria-label={`${volume.state === 'lost' ? 'Выбрать файл' : 'Заменить файл'} «${volume.fileName}»`}
        onChange={(event) => {
          const file = event.target.files?.[0];
          event.target.value = '';
          if (file && volume.itemId) onFile(volume.itemId, file);
        }}
        type="file"
      />
    </label>
  ) : null;
  const body = <>
    <strong className={styles.name}>{volume.fileName}</strong>
    <span><Badge tone={volume.state === 'failed' || volume.state === 'lost' ? 'warning' : volume.state === 'waiting' ? 'neutral' : 'indexing'}>{word}</Badge></span>
    {volume.state === 'transfer' ? (
      <Progress aria-label={`${word}: «${volume.fileName}»`} className={styles.progress} value={volume.progress ?? undefined} />
    ) : null}
    {limit && code ? <QuotaRefusal code={code} place="shelf" words={limit} /> : null}
    {reason ? <p className={styles.reason}>{reason}</p> : null}
    {volume.state === 'failed' && !importFailureMessage(code) && !limit && !volume.reasonCode
      ? <SupportReference code={job?.error?.requestId ?? null} /> : null}
    {fileInput || volume.state === 'failed' ? (
      <div className={styles.actions}>
        {fileInput}
        {volume.state === 'failed' && volume.itemId ? (
          <Button loading={skipping} onClick={() => onSkip(volume.itemId!)} size="sm" variant="ghost">
            <SkipForward aria-hidden="true" size={14} />Пропустить
          </Button>
        ) : null}
      </div>
    ) : null}
  </>;
  if (shape === 'row') {
    return (
      <article aria-label={volume.fileName} className={styles.row} data-state={volume.state} data-upload-volume="">
        <FileText aria-hidden="true" size={18} />
        {body}
      </article>
    );
  }
  return (
    <article aria-label={volume.fileName} className={styles.card} data-state={volume.state} data-upload-volume="">
      <span aria-hidden="true" className={styles.cover}><FileText size={28} /></span>
      {body}
    </article>
  );
}
