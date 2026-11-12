import { useCallback } from 'react';
import { AccountSessionContext, useAccountSession, type ContactRequest } from '../../shared/session/account-session';
import { supportMailHref } from '../../shared/support/support-contact';
import { Button } from '../controls/Button';
import styles from './feedback.module.css';

interface WriteToUsProps extends ContactRequest {
  className?: string;
  /** `button` — a button of its own, under a refusal; `link` — a word of the line «Код для поддержки: …». */
  look?: 'button' | 'link';
}

/**
 * «Написать нам» (specs/2026-11-10-ux2-contact.md CT1.2): where the page has the window of the site — a signed-in author
 * — it opens the window with what its place prepared: the topic, the code for support and the first lines. Where there is
 * no window — a guest, the screen of an outage, the admin working as the author, the desktop build — a letter to support
 * with the code and the page path (R4.2.1, R4.2.2). One component: a page never decides which.
 */
export function WriteToUs({ className, code, draft, look = 'button', topic }: WriteToUsProps) {
  const open = useAccountSession()?.onOpenContact;
  const supportCode = code?.trim() || undefined;
  if (open) {
    const request: ContactRequest = { ...(topic ? { topic } : {}), ...(supportCode ? { code: supportCode } : {}), ...(draft ? { draft } : {}) };
    return look === 'link'
      ? <button className={`${styles.supportLink}${className ? ` ${className}` : ''}`} onClick={() => open(request)} type="button">Написать нам</button>
      : <Button className={className} onClick={() => open(request)} size="sm" variant="outline">Написать нам</Button>;
  }
  return (
    <a
      className={`${styles.supportLink}${className ? ` ${className}` : ''}`}
      href={supportMailHref(supportCode, typeof window === 'undefined' ? '/' : window.location.pathname)}
    >
      Написать нам
    </a>
  );
}

/**
 * «Написать нам» for a line of the one area of the toasts (specs/2026-11-12-ux12-shell.md SH2.3): the area lies above the
 * account of the pages, so the button takes the account of the place that puts the line along — it opens the window of
 * the site there too, not a letter.
 */
export function useWriteToUsAction() {
  const account = useAccountSession();
  return useCallback((request: ContactRequest & { look?: WriteToUsProps['look'] }) => (
    <AccountSessionContext.Provider value={account}><WriteToUs {...request} /></AccountSessionContext.Provider>
  ), [account]);
}

/** «Код для поддержки: …» (selectable) and «Написать нам» on «Ошибка» with the code under an error (R4.2.1, CT1.2). */
export function SupportReference({ className, code }: { className?: string; code?: string | null }) {
  const supportCode = code?.trim() || undefined;
  return (
    <span className={`${styles.supportReference}${className ? ` ${className}` : ''}`}>
      {supportCode ? <span className={styles.supportCode}>Код для поддержки: <code>{supportCode}</code></span> : null}
      <WriteToUs code={supportCode} look="link" topic="bug" />
    </span>
  );
}
