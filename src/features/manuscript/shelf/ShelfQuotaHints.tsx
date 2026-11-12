import { useAccountQuota, type AccountQuotaClient, type QuotaCounter } from '../../../entities/account-quota/api';
import { refusalWords } from '../../../entities/account-quota/refusal-words';
import { QuotaRefusal } from '../../../entities/account-quota/ui/QuotaRefusal';
import { SpaceLimitLine, spaceTaken } from '../../../entities/account-quota/ui/SpaceLimitLine';
import { projectTextQuotaHint, uploadsQuotaHint } from './quota-messages';
import styles from './ShelfQuotaHints.module.css';

const spent = (counter: QuotaCounter | undefined) => Boolean(counter && counter.limit > 0 && counter.used >= counter.limit);

/**
 * What stands at «Добавить книги» by the counters of the account (quota spec R5.3; specs/2026-11-10-ux2-patterns-refusals.md
 * PR1.3, PR2.2, PR2.7). The uploads of the day are over — the one block of refusals before the press: nothing can be
 * uploaded today. The books of this saga take their place — one quiet line, the ordinary state of a new author: the block
 * comes after the press, when the server refused. Else the uploads left and the text of the saga from 80 % of their
 * limits. «Добавить книги» still lets the server decide (specs/2026-11-09-quota-accounting.md Q1.5).
 */
export function ShelfQuotaHints({ api, projectId }: { api: AccountQuotaClient; projectId: string | null }) {
  const quota = useAccountQuota(api, Boolean(projectId)).data;
  const day = spent(quota?.imports) ? refusalWords('quota.imports_daily', quota) : null;
  if (day) return <QuotaRefusal className={styles.limit} code="quota.imports_daily" place="shelf" words={day} />;
  if (spaceTaken('quota.project_books', quota, projectId)) return <SpaceLimitLine className={styles.limit} code="quota.project_books" place="shelf" projectId={projectId} quota={quota} />;
  const hints = [uploadsQuotaHint(quota), projectTextQuotaHint(quota, projectId)].filter(Boolean);
  if (hints.length === 0) return null;
  return (
    <div aria-live="polite" className={styles.hints} role="status">
      {hints.map((hint) => <p key={hint}>{hint}</p>)}
    </div>
  );
}
