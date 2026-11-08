import { CircleAlert, CircleCheck, CircleX, TriangleAlert } from 'lucide-react';
import type { ReactNode } from 'react';
import styles from './BookIndexingCard.module.css';

type IndexingOutcomeTone = 'success' | 'partial' | 'failed' | 'canceled';

interface IndexingOutcomeBannerProps {
  actions: ReactNode;
  desktopBody?: ReactNode;
  desktopTitle: string;
  facts?: ReactNode;
  mobileBody?: ReactNode;
  mobileTitle: string;
  notice?: string;
  tone: IndexingOutcomeTone;
}

export function IndexingOutcomeBanner({
  actions,
  desktopBody,
  desktopTitle,
  facts,
  mobileBody,
  mobileTitle,
  notice,
  tone,
}: IndexingOutcomeBannerProps) {
  const icon = tone === 'success'
    ? <CircleCheck aria-hidden="true" size={22} />
    : tone === 'partial'
      ? <TriangleAlert aria-hidden="true" size={22} />
      : tone === 'failed'
        ? <CircleAlert aria-hidden="true" size={22} />
        : <CircleX aria-hidden="true" size={22} />;

  return (
    <div
      className={styles.terminalOutcome}
      data-tone={tone}
    >
      <div className={styles.terminalOutcomeHeader}>
        <span className={styles.terminalOutcomeIcon}>{icon}</span>
        <div className={styles.terminalOutcomeText}>
          <h2>
            <span className={styles.terminalOutcomeDesktop}>{desktopTitle}</span>
            <span className={styles.terminalOutcomeMobile}>{mobileTitle}</span>
          </h2>
          {facts}
        </div>
        <div className={styles.terminalOutcomeActions}>{actions}</div>
      </div>
      {desktopBody || mobileBody || notice ? (
        <div className={styles.terminalOutcomePanel}>
          {desktopBody ? <div className={styles.terminalOutcomeDesktop}>{desktopBody}</div> : null}
          {mobileBody ? <div className={styles.terminalOutcomeMobile}>{mobileBody}</div> : null}
          {notice ? <p className={styles.terminalOutcomeError} role="alert">{notice}</p> : null}
        </div>
      ) : null}
    </div>
  );
}
