import { MailWarning } from 'lucide-react';
import { useAccountSession, type EmailVerificationState } from '../../shared/session/account-session';
import { Button } from '../controls/Button';
import { Alert } from './Alert';
import styles from './feedback.module.css';

const blockedWork = {
  shelf: 'книги загрузить нельзя',
  chat: 'задавать вопросы о саге нельзя',
} as const;

/** What happened to the last request for a letter, or null. */
export function emailVerificationStatusText(verification: EmailVerificationState) {
  if (verification.status === 'sent') return 'Письмо отправлено. Проверьте почту, в том числе «Спам».';
  if (verification.status === 'waiting') {
    return `Письмо уже отправлено — повторить можно через ${verification.retryAfterMinutes ?? 1} мин.`;
  }
  if (verification.status === 'failed') return verification.message ?? null;
  return null;
}

/**
 * «Подтвердите адрес» over the shelf upload zone and instead of the chat question field:
 * shown while the signed-in address is not verified (UC1.4, R5.5).
 */
export function EmailVerificationNotice({ surface }: { surface: keyof typeof blockedWork }) {
  const verification = useAccountSession()?.emailVerification;
  if (!verification) return null;
  const status = emailVerificationStatusText(verification);
  return (
    <Alert className={styles.emailNotice} tone="warning">
      <MailWarning aria-hidden="true" className={styles.emailNoticeIcon} size={18} />
      <div className={styles.emailNoticeBody}>
        <strong>Подтвердите адрес {verification.email}</strong>
        <span>{`Мы отправили на него письмо со ссылкой. Пока адрес не подтверждён, ${blockedWork[surface]}.`}</span>
        {status ? <span className={styles.emailNoticeStatus}>{status}</span> : null}
      </div>
      {verification.onSend ? (
        <Button
          className={styles.emailNoticeAction}
          disabled={!verification.canSend && verification.status !== 'sending'}
          loading={verification.status === 'sending'}
          onClick={verification.onSend}
          size="sm"
          variant="outline"
        >
          Отправить письмо ещё раз
        </Button>
      ) : null}
    </Alert>
  );
}
