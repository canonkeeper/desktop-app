import { createContext, useContext, type ReactNode } from 'react';

const TopBarStripContext = createContext<ReactNode>(null);

/** The app says what stands under the top bar of every page: the strip of a call (specs/2026-11-03-voice-assistant.md V6.5). */
export const TopBarStripProvider = TopBarStripContext.Provider;

/**
 * Its place, right after a top bar in the flow of the page: the page below takes the rest of the window, so the strip
 * covers nothing, and a page with a shared top bar cannot lose «Закончить» of an open microphone.
 */
export function TopBarStrip() {
  return <>{useContext(TopBarStripContext)}</>;
}

const TopBarTalkContext = createContext<ReactNode>(null);

/** The edition says what stands in the top bar of a page of the saga for its call: «Поговорить» on the site (V1.1). */
export const TopBarTalkProvider = TopBarTalkContext.Provider;

/** The place of that control in a top bar; empty where the edition has no call. */
export function TopBarTalk() {
  return <>{useContext(TopBarTalkContext)}</>;
}
