import { X } from 'lucide-react';
import { useState } from 'react';
import { useAccountQuota } from '../../entities/account-quota/api';
import { refusalWords } from '../../entities/account-quota/refusal-words';
import { QuotaRefusal } from '../../entities/account-quota/ui/QuotaRefusal';
import type { ManuscriptApiClient } from '../../entities/manuscript/api';
import { FirstProjectPanel } from '../../features/manuscript/shelf/ShelfHeader';
import { useCreateSaga, type SagaRefusal } from '../../features/manuscript/shelf/useSagaActions';
import type { RouteNavigate } from '../../shared/navigation/app-route';
import { IconButton } from '../../ui/controls/IconButton';
import { Alert } from '../../ui/feedback/Alert';
import { Dialog } from '../../ui/overlays/Dialog';
import styles from './NewSagaDialog.module.css';

/**
 * «Новая сага» of the menu of the saga (specs/2026-11-10-ux2-shell-core.md SC3.2): the name of the saga — the same form as
 * the first step of the shelf — and the author on the shelf of the new saga once it is created.
 */
export function NewSagaDialog({ api, navigate, onClose }: { api: ManuscriptApiClient; navigate: RouteNavigate; onClose: () => void }) {
  const createSaga = useCreateSaga(api);
  const [refusal, setRefusal] = useState<SagaRefusal | null>(null);
  // No room for a new saga by the counter of the account: the block stands before the press, the server still decides
  // (specs/2026-11-09-quota-accounting.md Q1.5; specs/2026-11-10-ux2-patterns-refusals.md PR1.3).
  const quota = useAccountQuota(api).data;
  const full = quota && quota.projects.limit > 0 && quota.projects.used >= quota.projects.limit ? refusalWords('quota.projects', quota) : null;
  return (
    <Dialog label="Новая сага" onDismiss={onClose} width={460}>
      <div className={styles.dialog}>
        <header className={styles.head}>
          <h2>Новая сага</h2>
          <IconButton aria-label="Закрыть" onClick={onClose}><X aria-hidden="true" size={18} /></IconButton>
        </header>
        <FirstProjectPanel creating={createSaga.creating} onCreate={async (title) => {
          const created = await createSaga.create(title);
          if ('refusal' in created) {
            setRefusal(created.refusal);
            return false;
          }
          onClose();
          navigate('/manuscript/books', { projectId: created.workspace.project.id });
          return true;
        }} />
        {/* A refusal by a limit is the one block of every section; «Тариф» leads back here (PR2.2, PR2.4). */}
        {refusal?.code && refusal.words ? <QuotaRefusal announce code={refusal.code} place="new-saga" words={refusal.words} />
          : refusal ? <Alert tone="error">{refusal.message}</Alert>
            : full ? <QuotaRefusal code="quota.projects" place="new-saga" words={full} /> : null}
      </div>
    </Dialog>
  );
}
