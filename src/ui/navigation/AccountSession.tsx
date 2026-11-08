import { ClockAlert, FileText, KeyRound, LayoutDashboard, LifeBuoy, LogOut, MailWarning, Megaphone, Send, Undo2, UserCog } from 'lucide-react';
import { useAccountSession, type EmailVerificationState } from '../../shared/session/account-session';
import { Button } from '../controls/Button';
import { emailVerificationStatusText } from '../feedback/EmailVerificationNotice';
import styles from './AccountSession.module.css';

const moscowTime = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit', timeZone: 'Europe/Moscow' });

/** The accessible name of an account button; it says when the admin works as another user or support answered. */
export function useAccountButtonLabel() {
  const session = useAccountSession();
  const actingAs = session?.actingAs;
  if (actingAs) return `Аккаунт: вы работаете как ${actingAs.name}`;
  return session?.contactUnread ? 'Аккаунт: есть ответ поддержки' : 'Аккаунт';
}

/** The mark on the account button while a reply of support waits to be opened (specs/2026-11-07-contacts.md F3.2). */
export function AccountUnreadMark({ className }: { className?: string }) {
  const unread = useAccountSession()?.contactUnread ?? 0;
  if (unread <= 0) return null;
  return <span aria-hidden="true" className={className ? `${styles.unread} ${className}` : styles.unread} data-account-unread="" />;
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
 * The account part of a menu before «Выйти»: the admin items («Админка» and «Работать как
 * пользователь», or who the admin works as and the way back), the security items and «Связаться с нами».
 * `onDone` closes the menu and returns focus to the account button before the action opens a
 * dialog or changes the identity.
 */
export function AccountSessionItems({ itemRole, onDone }: { itemRole?: 'menuitem'; onDone?: () => void }) {
  const session = useAccountSession();
  // An admin working as another user does not write as that user (R4.2.6): the context has no onOpenContact then.
  const unread = session?.contactUnread ?? 0;
  const supportItem = session?.onOpenContact && !session.actingAs ? (
    <Button className={styles.item} onClick={() => { onDone?.(); session.onOpenContact?.(); }} role={itemRole} size="touch" variant="ghost">
      <LifeBuoy aria-hidden="true" size={17} />
      Связаться с нами
      {unread > 0 ? <span className={styles.count}>{unread}<span className={styles.hidden}> {unread === 1 ? 'новый ответ' : 'новых ответа'}</span></span> : null}
    </Button>
  ) : null;
  if (!session) return supportItem;
  const { onChangePassword, onLogoutEverywhere, onOpenDocuments, onOpenPromoPermissions } = session;
  return (
    <>
      {session.emailVerification ? <EmailVerificationItems itemRole={itemRole} verification={session.emailVerification} /> : null}
      <AdminSessionItems itemRole={itemRole} onDone={onDone} />
      {onChangePassword ? (
        <Button className={styles.item} onClick={() => { onDone?.(); onChangePassword(); }} role={itemRole} size="touch" variant="ghost">
          <KeyRound aria-hidden="true" size={17} />
          Сменить пароль
        </Button>
      ) : null}
      {onLogoutEverywhere ? (
        <Button className={styles.item} onClick={() => { onDone?.(); onLogoutEverywhere(); }} role={itemRole} size="touch" variant="ghost">
          <LogOut aria-hidden="true" size={17} />
          Выйти на всех устройствах
        </Button>
      ) : null}
      {onOpenPromoPermissions ? (
        <Button className={styles.item} onClick={() => { onDone?.(); onOpenPromoPermissions(); }} role={itemRole} size="touch" variant="ghost">
          <Megaphone aria-hidden="true" size={17} />
          Разрешения на продвижение
        </Button>
      ) : null}
      {onOpenDocuments ? (
        <Button className={styles.item} onClick={() => { onDone?.(); onOpenDocuments(); }} role={itemRole} size="touch" variant="ghost">
          <FileText aria-hidden="true" size={17} />
          Документы
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
          {verification.status === 'sending' ? 'Отправляем…' : 'Отправить письмо ещё раз'}
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
