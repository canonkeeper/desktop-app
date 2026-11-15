// SPDX-License-Identifier: AGPL-3.0-only
import type { Schema } from '../../../shared/api';
import { useAiSettings } from '../settings/helper/helper-api';
import { useDesktopAccount, type DesktopAccount } from './account-api';
import { useAccountLink, type AccountLinkClient } from './AccountLink';

/** What the way out of «помощник не подключён» does: the window of the choice, a new link, the window of the waiting. */
export type HelperReasonAction = 'connect' | 'relink' | 'linkWindow';

/**
 * Why the helper is not connected, in the words of the line of the reading and of the field of the chat, the name of the line
 * by those words, and its way out.
 */
export interface HelperReason {
  title: string;
  label: string;
  action: HelperReasonAction;
}

const notYet = 'Помощник ещё не подключён';

/** «Помощник ещё не подключён. Читать книги и искать по ним можно уже сейчас» — the words before the account (WN3.2). */
export const notConnected = 'Помощник ещё не подключён. Читать книги и искать по ним можно уже сейчас';

/**
 * The one reason «помощник не подключён» of the app (specs/2026-11-15-desk-account-link-web.md AW3.4) — the line of the
 * reading and the field of the chat say it by this function — by the source of the settings and the state of the link, the
 * answers of getAiSettings and getAccount: the helper of the account and a broken link — «связь прервана» with «Связать снова»;
 * a link that waits (the account chosen, or none yet) — the waiting with «Открыть окно связи»; the helper of the account not
 * linked, or none chosen — «Помощник ещё не подключён…» with «Подключить помощника»; «Свой ИИ» — the words it had.
 */
export function helperReasonOf(source: Schema<'AiSettings'>['source'], state: DesktopAccount['state']): HelperReason {
  if (source === 'own') return { title: notConnected, label: notYet, action: 'connect' };
  if (state === 'waiting') return { title: 'Помощник ещё не подключён: связь ждёт подтверждения в браузере', label: notYet, action: 'linkWindow' };
  if (source === 'account' && state === 'broken') {
    return { title: 'Помощник не подключён: связь с аккаунтом прервана', label: 'Помощник не подключён', action: 'relink' };
  }
  return { title: notConnected, label: notYet, action: 'connect' };
}

/**
 * Whether the answers of getAiSettings and getAccount say the helper is not connected (decision of the coordinator 15.11): none
 * chosen, or the helper of the account without its link held (specs/2026-11-13-desk-account-ai.md AI1.2). Whether «Свой ИИ»
 * answers — its models and its key — is the server's rule alone (`aisettings.Settings.Connected`): the page does not repeat it,
 * the status says it by `waiting: helper`, a refused question by `helper.not_connected`.
 */
export function notConnectedOf(source: Schema<'AiSettings'>['source'], state: DesktopAccount['state']) {
  return source === 'none' || (source === 'account' && state !== 'linked');
}

/**
 * The reason while the helper is not connected by those answers, whatever the status of the saga says — a read saga says
 * nothing of a waiting (decision of the coordinator 15.11): «Спросить» of the chat and the line of the reading say it before a
 * question. Null while connected, until both answers are read — nothing flickers meanwhile — and outside the link of the edition.
 */
export function useNotConnectedReason(enabled = true): HelperReason | null {
  const link = useAccountLink();
  const on = enabled && link !== null;
  // Without the link the queries never run: their client is not asked.
  const api = link?.api as AccountLinkClient;
  const settings = useAiSettings(api, on).data;
  const account = useDesktopAccount(api, on).data;
  if (!on || !settings || !account || !notConnectedOf(settings.settings.source, account.state)) return null;
  return helperReasonOf(settings.settings.source, account.state);
}

/**
 * The reason of the app while the reading waits for the helper (`waiting`): null until both answers are read — the open line
 * stands meanwhile — and outside the link of the edition.
 */
export function useHelperReason(waiting: boolean): HelperReason | null {
  const link = useAccountLink();
  const enabled = waiting && link !== null;
  // Without the link the queries never run: their client is not asked.
  const api = link?.api as AccountLinkClient;
  const settings = useAiSettings(api, enabled).data;
  const account = useDesktopAccount(api, enabled).data;
  if (!enabled || !settings || !account) return null;
  return helperReasonOf(settings.settings.source, account.state);
}

/**
 * The reason once the answers of getAiSettings and getAccount asked again after `since` came (or failed): the server refused a
 * message because the helper is not connected, and the link may have broken on the site at that very moment
 * (specs/2026-11-15-desk-account-words-web.md AW4.2). Null until both came, and outside the link of the edition.
 */
export function useHelperReasonSince(since: number): HelperReason | null {
  const link = useAccountLink();
  const enabled = link !== null;
  const api = link?.api as AccountLinkClient;
  const settings = useAiSettings(api, enabled);
  const account = useDesktopAccount(api, enabled);
  const came = (query: { dataUpdatedAt: number; errorUpdatedAt: number }) => Math.max(query.dataUpdatedAt, query.errorUpdatedAt) >= since;
  if (!enabled || !settings.data || !account.data || !came(settings) || !came(account)) return null;
  return helperReasonOf(settings.data.settings.source, account.data.state);
}

/** The words of the reason at «Спросить» of the chat — the field of the slot of the chat of the app. */
export function useHelperAskReason(waiting: boolean) {
  return useHelperReason(waiting)?.title ?? null;
}
