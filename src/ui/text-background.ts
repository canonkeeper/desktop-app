import { createContext, useContext } from 'react';

/** «Фон текста» of a sheet of text: light, sepia or dark (specs/2026-11-12-ux11-dark-core.md TH1.3). */
export type TextBackground = 'light' | 'sepia' | 'dark';

/**
 * The background of the sheet of text the reader and «Письмо» paint. The windows of the sheet — «Содержание», «Вид
 * текста», the menu of a selection — stand outside it (a portal) and follow it by this, not the theme of the site (TH1.4);
 * the other windows and the lines of a result follow the site.
 */
export const TextBackgroundContext = createContext<TextBackground | null>(null);

/** The mark that paints a window of the sheet of text in its background; nothing outside a sheet. */
export function useTextBackgroundMark(): { 'data-reading-theme'?: TextBackground } {
  const background = useContext(TextBackgroundContext);
  return background ? { 'data-reading-theme': background } : {};
}
