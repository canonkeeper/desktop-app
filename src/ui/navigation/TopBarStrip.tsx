import { createContext, useContext, type ReactNode } from 'react';
import { useAccountSession } from '../../shared/session/account-session';
import { EmailVerificationStrip } from '../feedback/EmailVerificationNotice';
import styles from './TopBarStrip.module.css';

const TopBarStripContext = createContext<ReactNode>(null);

/** The app says what stands under the top bar of every page: the strip of a call (specs/2026-11-03-voice-assistant.md V6.5). */
export const TopBarStripProvider = TopBarStripContext.Provider;

const TopBarNoticeContext = createContext<ReactNode>(null);

/**
 * The app says what notice of the saga stands under the top bar: «Книга прочитана — можно спрашивать»
 * (specs/2026-11-10-ux3-saga-state.md SS3.5) — in the place of the notices of the shell, not over the whole page.
 */
export const TopBarNoticeProvider = TopBarNoticeContext.Provider;

/**
 * Its place, right after a top bar in the flow of the page: the page below takes the rest of the window, so the strip
 * covers nothing, and a page with a shared top bar cannot lose «Закончить» of an open microphone. While the address of the
 * author is not verified, its line stands there too, under the strip of a call (specs/2026-11-10-ux1-shell.md SH4.2), and
 * the notice of the saga between them: all in one row of the page.
 */
export function TopBarStrip() {
  const call = useContext(TopBarStripContext);
  const notice = useContext(TopBarNoticeContext);
  const session = useAccountSession();
  const unverified = Boolean(session?.emailVerification);
  // The end of a paid tier (specs/2026-11-11-ux9-account.md AC2.4) stands in the same row.
  const account = session?.notice ?? null;
  if (!unverified && !notice && !account) return <>{call}</>;
  return (
    <div className={styles.strips} data-top-bar-strip="">
      {call}
      {notice}
      {account}
      {unverified ? <EmailVerificationStrip /> : null}
    </div>
  );
}

const TopBarTalkContext = createContext<ReactNode>(null);

/** The edition says what stands in the top bar of a page of the saga for its call: «Поговорить» on the site (V1.1). */
export const TopBarTalkProvider = TopBarTalkContext.Provider;

/** The place of that control in a top bar; empty where the edition has no call. */
export function TopBarTalk() {
  return <>{useContext(TopBarTalkContext)}</>;
}

/**
 * «Поговорить» as a line of a page offers it (specs/2026-11-10-ux3-first-screen.md FS3.1, FS3.3): the edition gives the hook
 * of the press of the button of its top bar; it answers null where that button is not there, and so does a build
 * without the voice.
 */
const TalkActionContext = createContext<() => (() => void) | null>(() => null);

export const TalkActionProvider = TalkActionContext.Provider;

export function useTalkAction() {
  const useEditionTalk = useContext(TalkActionContext);
  return useEditionTalk();
}
