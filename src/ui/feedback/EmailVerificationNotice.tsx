import { MailWarning, Pencil } from 'lucide-react';
import { emailUnverifiedReason, sendAgainWords, useAccountSession, type EmailVerificationState } from '../../shared/session/account-session';
import { Button } from '../controls/Button';
import { Alert } from './Alert';
import styles from './feedback.module.css';

/** What happened to the last request for a letter, or null. */
export function emailVerificationStatusText(verification: EmailVerificationState) {
  if (verification.status === 'sent') {
    return verification.sentTo ? `Письмо отправлено на ${verification.sentTo}. Проверьте почту, в том числе «Спам».` : 'Письмо отправлено. Проверьте почту, в том числе «Спам».';
  }
  if (verification.status === 'waiting') {
    return `Письмо уже отправлено — повторить можно через ${verification.retryAfterMinutes ?? 1} мин.`;
  }
  if (verification.status === 'failed') return verification.message ?? null;
  return null;
}

/** The reason, what became of the last letter and «Отправить письмо ещё раз»; no button while an admin works as the author. */
function EmailVerificationWords({ verification }: { verification: EmailVerificationState }) {
  const status = emailVerificationStatusText(verification);
  return (
    <>
      <MailWarning aria-hidden="true" className={styles.emailNoticeIcon} size={18} />
      <div className={styles.emailNoticeBody}>
        <strong>{emailUnverifiedReason(verification.email)}</strong>
        {status ? <span className={styles.emailNoticeStatus} role="status">{status}</span> : null}
      </div>
      {verification.onChangeEmail ? (
        <Button className={styles.emailNoticeAction} onClick={verification.onChangeEmail} size="sm" variant="ghost">
          <Pencil aria-hidden="true" size={14} />Изменить
        </Button>
      ) : null}
      {verification.onSend ? (
        <Button
          className={styles.emailNoticeAction}
          disabled={!verification.canSend && verification.status !== 'sending'}
          loading={verification.status === 'sending'}
          onClick={verification.onSend}
          size="sm"
          variant="outline"
        >
          {verification.status === 'sending' ? 'Отправить письмо ещё раз' : sendAgainWords(verification)}
        </Button>
      ) : null}
    </>
  );
}

/**
 * «Подтвердите почту» where the author meets a closed action — over the upload of the shelf, instead of the question field
 * of the chat (UC1.4, R5.5) — in the words of the line under the top bar (specs/2026-11-10-ux1-shell.md SH4.3).
 */
export function EmailVerificationNotice() {
  const verification = useAccountSession()?.emailVerification;
  if (!verification) return null;
  return (
    <Alert className={styles.emailNotice} tone="warning">
      <EmailVerificationWords verification={verification} />
    </Alert>
  );
}

/**
 * The line under the top bar of every page of the author while the address is not verified (SH4.2): what is closed and
 * what to press, before the author meets a closed action. It stands in the flow beside the strip of a call.
 */
export function EmailVerificationStrip() {
  const verification = useAccountSession()?.emailVerification;
  if (!verification) return null;
  return (
    <div aria-label="Почта не подтверждена" className={styles.emailStrip} role="note">
      <EmailVerificationWords verification={verification} />
    </div>
  );
}
