import { useAccountQuota, type AccountQuotaClient } from '../../../entities/account-quota/api';
import { projectTextQuotaHint, uploadsQuotaHint } from './quota-messages';
import styles from './ShelfQuotaHints.module.css';

/** Uploads left today and the project text, from 80 % of their limits (quota spec R5.3). */
export function ShelfQuotaHints({ api, projectId }: { api: AccountQuotaClient; projectId: string | null }) {
  const quota = useAccountQuota(api, Boolean(projectId)).data;
  const hints = [uploadsQuotaHint(quota), projectTextQuotaHint(quota, projectId)].filter(Boolean);
  if (hints.length === 0) return null;
  return (
    <div aria-live="polite" className={styles.hints} role="status">
      {hints.map((hint) => <p key={hint}>{hint}</p>)}
    </div>
  );
}
