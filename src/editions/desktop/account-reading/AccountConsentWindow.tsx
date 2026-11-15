// SPDX-License-Identifier: AGPL-3.0-only
import { BookOpen, Gauge, X } from 'lucide-react';
import { useState } from 'react';
import { isApiStatusFailure, loadFailureWords, publicApiErrorMessage } from '../../../entities/api-errors';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { Alert } from '../../../ui/feedback/Alert';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { SkeletonArea } from '../../../ui/feedback/Skeleton';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { Dialog } from '../../../ui/overlays/Dialog';
import { RelinkButton, TariffsLink } from '../chat-ways';
import { useReadingEstimate, useSetConsent, type ReadingEstimate } from '../reading/reading-api';
import styles from '../reading/Reading.module.css';
import {
  accountConsentMessage, accountReadingWords, accountRefusalWords, bringTogetherWords, endsFirstLine, volumeLine,
} from './account-reading-words';

export interface AccountConsentWindowProps {
  projectId: string;
  onClose: () => void;
}

/**
 * «Прочитать книги с помощником CanonKeeper?» (specs/2026-11-15-desk-account-words-web.md AW5.2; board 44·44): what the helper
 * does, that the text goes through the server, that the books added later are read the same way; how many characters of the tier
 * it takes and how many were used in 30 days, the time; which limit ends first. No field of a sum, no sample, no prices.
 * «Прочитать книги» gives the consent without a sum and closes the window once the server says the reading goes; only the names
 * and the threads left — «Свести …»; nothing to read — «Закрыть».
 */
export function AccountConsentWindow({ onClose, projectId }: AccountConsentWindowProps) {
  const estimate = useReadingEstimate(projectId, true);
  // A write of the window runs: the window does not close under it.
  const [busy, setBusy] = useState(false);
  return (
    <Dialog canDismiss={!busy} label={accountReadingWords.title} onDismiss={onClose} width={720}>
      <div className={styles.window}>
        <div className={styles.head}>
          <h2 data-overlay-heading="">{accountReadingWords.title}</h2>
          <IconButton aria-label={accountReadingWords.close} disabled={busy} onClick={onClose} size="sm"><X aria-hidden="true" size={16} /></IconButton>
        </div>
        {estimate.isPending ? <SkeletonArea count={3} shape="lines" /> : null}
        {estimate.isError ? (
          <ErrorBlock {...loadFailureWords(estimate.error)} onRetry={() => void estimate.refetch()} retrying={estimate.isRefetching} title={accountReadingWords.loadFailed} />
        ) : null}
        {estimate.data ? <Consent estimate={estimate.data} onBusy={setBusy} onClose={onClose} projectId={projectId} /> : null}
      </div>
    </Dialog>
  );
}

function Consent({ estimate, onBusy, onClose, projectId }: {
  estimate: ReadingEstimate;
  projectId: string;
  onBusy: (busy: boolean) => void;
  onClose: () => void;
}) {
  const toasts = useToasts();
  const consent = useSetConsent(projectId);
  // The refusal of the server to the write of the window: the link broke while it was open, or another reason in its words.
  const [refusal, setRefusal] = useState<{ link: true } | { words: string } | null>(null);
  const namesOnly = estimate.chapters === 0 && (estimate.namesWait || estimate.threadsWait);
  const nothing = estimate.chapters === 0 && !namesOnly;
  const account = estimate.account;
  const label = namesOnly ? bringTogetherWords(estimate.namesWait, estimate.threadsWait) : accountReadingWords.readBooks;
  // Which limit ends first is said of reading the chapters: with only the names and the threads left there is nothing to read,
  // and the window says only what is left to bring together.
  const short = account && estimate.chapters > 0 ? endsFirstLine(account) : null;

  function read() {
    if (consent.isPending) return;
    setRefusal(null);
    onBusy(true);
    consent.mutate({ change: { state: 'allowed' }, label }, {
      onSettled: () => onBusy(false),
      onSuccess: () => {
        onClose();
        toasts.push({ message: accountReadingWords.started, detail: accountReadingWords.startedDetail, tone: 'success' });
      },
      onError: (error) => setRefusal(isApiStatusFailure(error) && error.problem.code === 'helper.not_connected'
        ? { link: true }
        : { words: publicApiErrorMessage(error, accountReadingWords.notStarted, { codes: accountRefusalWords }) }),
    });
  }

  return (
    <div className={styles.consent}>
      <p className={styles.message}>{accountConsentMessage(estimate)}</p>
      {account && estimate.chapters > 0 ? (
        <p className={styles.line}><Gauge aria-hidden="true" size={14} /> {volumeLine(estimate, account)}</p>
      ) : null}
      {short ? (
        <div className={styles.failure} role="note">
          <p className={styles.failureLine}>{short}</p>
          <TariffsLink />
        </div>
      ) : null}
      {refusal && 'link' in refusal ? (
        <Alert aria-label={accountReadingWords.linkBroken} tone="error">
          <strong>{accountReadingWords.linkBroken}</strong>
          <div className={styles.footer}><RelinkButton onShown={onClose} /></div>
        </Alert>
      ) : null}
      {refusal && 'words' in refusal ? <p className={styles.refusal} role="alert">{refusal.words}</p> : null}
      <div className={styles.footer}>
        {nothing ? (
          <Button onClick={onClose} variant="outline">{accountReadingWords.close}</Button>
        ) : (
          <>
            <Button disabled={consent.isPending} onClick={onClose} variant="outline">{accountReadingWords.notNow}</Button>
            <Button loading={consent.isPending} onClick={read} variant="primary"><BookOpen aria-hidden="true" size={14} />{label}</Button>
          </>
        )}
      </div>
    </div>
  );
}
