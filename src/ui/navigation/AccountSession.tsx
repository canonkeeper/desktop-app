import { ClockAlert, Gauge, LayoutDashboard, LifeBuoy, MailWarning, Megaphone, Send, Undo2, UserCog, UserRound } from 'lucide-react';
import { sendAgainWords, useAccountSession, type EmailVerificationState } from '../../shared/session/account-session';
import { Button } from '../controls/Button';
import { pluralize } from '../copy';
import { emailVerificationStatusText } from '../feedback/EmailVerificationNotice';
import styles from './AccountSession.module.css';

const moscowTime = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit', timeZone: 'Europe/Moscow' });

/** The accessible name of an account button; it says when the admin works as another user or support answered. */
export function useAccountButtonLabel() {
  const session = useAccountSession();
  const actingAs = session?.actingAs;
  if (actingAs) return `Аккаунт: вы работаете как ${actingAs.name}`;
  // A waiting request of CanonKeeper is said by its notice, a «status», and by its line of the menu (AC3.7), not here.
  return session?.contactUnread ? 'Аккаунт: есть ответ поддержки' : 'Аккаунт';
}

/**
 * The mark on the account button while a reply of support waits to be opened (specs/2026-11-07-contacts.md F3.2) or a
 * request of CanonKeeper waits for the answer (specs/2026-11-11-ux9-account.md AC3.7).
 */
export function AccountUnreadMark({ className }: { className?: string }) {
  const session = useAccountSession();
  const unread = session?.contactUnread ?? 0;
  if (unread <= 0 && !session?.promoRequest) return null;
  return <span aria-hidden="true" className={className ? `${styles.unread} ${className}` : styles.unread} data-account-unread="" />;
}

/**
 * The badge of the tier in force right before the account button of a top bar, where the build has plans
 * (specs/2026-11-08-subscription-tiers.md TR9.9); nothing while the plan loads or fails.
 */
export function AccountPlanBadge() {
  return <>{useAccountSession()?.plan?.badge}</>;
}

/** The mark on the account button while a platform admin works as another user. */
export function AccountActingMark({ className }: { className?: string }) {
  const actingAs = useAccountSession()?.actingAs;
  if (!actingAs) return null;
  const Icon = actingAs.expiresSoon ? ClockAlert : UserCog;
  return (
    <span aria-hidden="true" className={className ? `${styles.mark} ${className}` : styles.mark} data-expiring={actingAs.expiresSoon ? '' : undefined}>
      <Icon size={10} strokeWidth={2.5} />
    </span>
  );
}

/**
 * The account part of a menu before «Выйти» (specs/2026-11-10-ux2-tariff-account.md TA1.1, TA1.2): the lines of the
 * sign-in — the unverified address, the admin items («Админка» and «Работать как пользователь», or who the admin works as
 * and the way back) — then «Тариф · «Писатель» до …», «Аккаунт» and «Написать нам» with the replies not opened yet.
 * `onDone` closes the menu and returns focus to the account button before the action opens a dialog or changes the
 * identity.
 */
export function AccountSessionItems({ itemRole, onDone, support = true }: {
  itemRole?: 'menuitem';
  onDone?: () => void;
  /** «Написать нам»; the drawer of a phone has «Помощь» above it instead (specs/2026-11-10-ux2-shell.md S1.3). */
  support?: boolean;
}) {
  const session = useAccountSession();
  // An admin working as another user does not write as that user (R4.2.6): the context has no onOpenContact then.
  const unread = session?.contactUnread ?? 0;
  const supportItem = support && session?.onOpenContact && !session.actingAs ? (
    <Button className={styles.item} onClick={() => { onDone?.(); session.onOpenContact?.(); }} role={itemRole} size="touch" variant="ghost">
      <LifeBuoy aria-hidden="true" size={17} />
      Написать нам
      {unread > 0 ? <>{' '}<span className={styles.count}>· {unread} {pluralize(unread, 'ответ', 'ответа', 'ответов')}</span></> : null}
    </Button>
  ) : null;
  if (!session) return supportItem;
  const { onOpenAccount, onOpenTariff, plan } = session;
  return (
    <>
      {session.emailVerification ? <EmailVerificationItems itemRole={itemRole} verification={session.emailVerification} /> : null}
      <AdminSessionItems itemRole={itemRole} onDone={onDone} />
      {onOpenTariff ? (
        <Button className={styles.item} data-ending={plan?.ending ? '' : undefined} onClick={() => { onDone?.(); onOpenTariff(); }} role={itemRole} size="touch" variant="ghost">
          <Gauge aria-hidden="true" size={17} />
          <span className={styles.itemText}>{plan?.line ?? 'Тариф'}</span>
        </Button>
      ) : null}
      {onOpenAccount ? (
        <Button className={styles.item} onClick={() => { onDone?.(); onOpenAccount(); }} role={itemRole} size="touch" variant="ghost">
          <UserRound aria-hidden="true" size={17} />
          Аккаунт
        </Button>
      ) : null}
      {session.promoRequest ? (
        // While a request waits — the line the mark of the account button stands for (AC3.7).
        <Button className={styles.item} onClick={() => { onDone?.(); session.promoRequest?.onOpen(); }} role={itemRole} size="touch" variant="ghost">
          <Megaphone aria-hidden="true" size={17} />
          <span className={styles.itemText}>Запрос CanonKeeper ждёт ответа</span>
          <span aria-hidden="true" className={styles.itemMark} />
        </Button>
      ) : null}
      {supportItem}
    </>
  );
}

/** «Адрес не подтверждён» with the address and «Отправить письмо ещё раз» (UC1.3). */
function EmailVerificationItems({ itemRole, verification }: { itemRole?: 'menuitem'; verification: EmailVerificationState }) {
  const status = emailVerificationStatusText(verification);
  return (
    <div className={styles.section}>
      <p className={styles.identity}>
        <strong><MailWarning aria-hidden="true" className={styles.inlineIcon} size={13} />Адрес не подтверждён</strong>
        <span>{verification.email}</span>
        {status ? <span>{status}</span> : null}
      </p>
      {verification.onSend ? (
        <Button
          className={styles.item}
          disabled={!verification.canSend}
          onClick={verification.onSend}
          role={itemRole}
          size="touch"
          variant="ghost"
        >
          <Send aria-hidden="true" size={17} />
          {sendAgainWords(verification)}
        </Button>
      ) : null}
    </div>
  );
}

function AdminSessionItems({ itemRole, onDone }: { itemRole?: 'menuitem'; onDone?: () => void }) {
  const session = useAccountSession();
  if (!session) return null;
  const { actingAs, busy, onActAsUser, onReturnToAdmin } = session;
  if (actingAs && onReturnToAdmin) {
    return (
      <div className={styles.section}>
        <p className={styles.identity}>
          <strong>Вы работаете как {actingAs.name}</strong>
          <span>{actingAs.email}</span>
          <span>{`Администратор: ${actingAs.adminName} · до\u00a0${moscowTime.format(Date.parse(actingAs.expiresAt))}`}</span>
          {actingAs.expiresSoon ? <span className={styles.warning}>Сессия истекает через 2 мин — завершите действия</span> : null}
        </p>
        <Button className={styles.item} disabled={busy} onClick={() => { onDone?.(); onReturnToAdmin(); }} role={itemRole} size="touch" variant="ghost">
          <Undo2 aria-hidden="true" size={17} />
          {busy ? 'Возвращаемся…' : 'Вернуться к администратору'}
        </Button>
      </div>
    );
  }
  const { onOpenAdmin } = session;
  return (
    <>
      {onOpenAdmin ? (
        <Button className={styles.item} onClick={() => { onDone?.(); onOpenAdmin(); }} role={itemRole} size="touch" variant="ghost">
          <LayoutDashboard aria-hidden="true" size={17} />
          Админка
        </Button>
      ) : null}
      {onActAsUser ? (
        <Button className={styles.item} onClick={() => { onDone?.(); onActAsUser(); }} role={itemRole} size="touch" variant="ghost">
          <UserCog aria-hidden="true" size={17} />
          Работать как пользователь
        </Button>
      ) : null}
    </>
  );
}
