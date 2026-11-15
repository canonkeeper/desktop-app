// SPDX-License-Identifier: AGPL-3.0-only
import type { QueryClient } from '@tanstack/react-query';
import { createElement, type ReactNode } from 'react';
import type { AccountQuota } from '../../entities/account-quota/api';
import { refusalWords } from '../../entities/account-quota/refusal-words';
import type { ChatJob } from '../../entities/chat/api';
import type { ChatSlot } from '../../features/chat/chat-slot';
import type { ChatFailure, ChatFailureWords } from '../../features/chat/research-chat-presentation';
import { ApiStatusError } from '../../shared/api';
import type { RouteNavigate } from '../../shared/navigation/app-route';
import { formatCount } from '../../ui/copy';
import { accountQueryKey, afterLinkChange } from './account/account-api';
import { useHelperAskReason } from './account/helper-reason';
import { ConnectOwnButton, HelperRefusalWay, HelperRefusalWords, OpenUpdatesButton, RelinkButton, TariffsLink } from './chat-ways';

/**
 * The way out of a refusal or a failed turn of the app (specs/2026-11-15-desk-account-words-web.md AW4.2, AW4.3): the question
 * again into the field and «Новый чат» are the core's; a new link, the part «Обновления» and the page of the tiers are the
 * edition's.
 */
export type AppWay = 'ask_again' | 'new_chat' | 'relink' | 'updates' | 'tariffs';

export interface AppFailureWords {
  title: string;
  way: AppWay | null;
}

/**
 * The words of a failed turn of the app by the code of its Job (Data «срыв хода»): the refusals of the site of the account its
 * last request got (specs/2026-11-13-desk-account-ai.md AI2.3, AI2.5), a question a restart of the app interrupted
 * (specs/2026-11-15-desk-interrupted-turn.md IT1.4, board 44·45), and two codes of the site whose words of the core do not fit
 * the app — the advice of the shared limit of the service lived in the line of the count of the core, and an interrupted answer
 * spoke of an update of the service. A code of the site the app does not name keeps the core's.
 */
export const turnWords: Readonly<Record<string, AppFailureWords>> = {
  'model.daily_fence': { title: 'Сегодня помощник из приложения больше не отвечает: превышен суточный предел его работы. Завтра он снова ответит', way: null },
  'model.account_unlinked': { title: 'Связь с аккаунтом прервана', way: 'relink' },
  'model.permit_ended': { title: 'Вопрос ждал слишком долго, и сервер закрыл его', way: 'ask_again' },
  'model.site_unavailable': { title: 'Помощник на сервере сейчас не отвечает. Попробуйте позже', way: 'ask_again' },
  'model.app_unfit': { title: 'Эта версия CanonKeeper устарела для сервера', way: 'updates' },
  'model.thread_too_large': { title: 'Этот разговор стал слишком длинным для помощника CanonKeeper', way: 'new_chat' },
  'model.site_unreachable': { title: 'Нет связи с сервером CanonKeeper', way: 'ask_again' },
  'model.session_ended': { title: 'Ответ прервался: CanonKeeper был перезапущен', way: 'ask_again' },
  'model.usage_limit_exceeded': { title: 'Сервис перегружен. Повторите через несколько минут', way: 'ask_again' },
  'model.turn_interrupted': { title: 'Ответ прервался. Спросите ещё раз', way: 'ask_again' },
  // The gateway did not send a request of the turn: its record did not lie in the journal of what went out
  // (specs/2026-11-14-desk-journal.md JR1.6, Data «срыв вопроса»).
  'journal.unavailable': { title: 'Запрос помощнику не отправлен: не удалось записать его в журнал отправок. Попробуйте ещё раз', way: 'ask_again' },
};

/**
 * «Пока вопрос ждал, место заняли другие ваши вопросы…» (Data «срыв хода», `quota.active_turns`): the question waited while its
 * permit went by unused, and the next one met «разом» — with the limit of the server when it is known, else without a number.
 */
function activeTurnsTitle(limit: number | undefined) {
  const took = 'Пока вопрос ждал, место заняли другие ваши вопросы';
  if (!limit || limit < 1) return `${took}. Спросите снова, когда один из ответов будет готов`;
  return `${took}: разом помощник готовит не больше ${formatCount(limit, 'ответа', 'ответов', 'ответов')}. Спросите снова, когда ${limit === 1 ? 'он' : 'один из них'} будет готов`;
}

/**
 * The words of a failed turn of the app (Data «срыв хода»); a code of the tier — the question waited while its permit went by
 * unused and the next one was refused — «Пока вопрос ждал, …»: the questions of the day in the words of the tier with
 * «Тарифы», «разом» with «Повторить вопрос»; null — the core's.
 */
export function turnWordsOf(code: string, quota: AccountQuota | undefined): AppFailureWords | null {
  if (code === 'quota.active_turns') return { title: activeTurnsTitle(quota?.activeTurns.limit), way: 'ask_again' };
  if (code.startsWith('quota.')) {
    const words = refusalWords(code, quota);
    return words ? { title: `Пока вопрос ждал, ${words.ended.charAt(0).toLowerCase()}${words.ended.slice(1)}`, way: 'tariffs' } : null;
  }
  return turnWords[code] ?? null;
}

/** The line of the count of a failed question (Data «счёт сорванного вопроса»): «да» and «решается» say so, the rest nothing. */
export const countWords = {
  yes: 'Этот вопрос не засчитан',
  pending: 'Засчитан ли этот вопрос, решит сервер в течение нескольких минут',
} as const;

/**
 * The count of a failed question only by the sign of the server (AW4.4; specs/2026-11-13-desk-account-ai.md AI2.9): '' — no
 * line; the page counts and keeps nothing of it.
 */
export function countLine(returned: ChatJob['questionReturned']) {
  return returned === 'yes' ? countWords.yes : returned === 'pending' ? countWords.pending : '';
}

interface RefusedWords {
  text: string;
  way: AppWay | null;
}

/** The name of the block of a refusal at the start for the reader of the screen: it has no visible title (board 44·45). */
const refusedLabels = { start: 'Вопрос не отправлен', steer: 'Уточнение не отправлено' } as const;

/**
 * A question or a correction the counter of the helper of the account refused at its start (AW4.2; Data «старт», «уточнение»):
 * the sentence of the return to the count only when the server says the unit may come back; null — no words of the app (the
 * refusals of the tier stand above the field, the helper not connected is the one reason of the edition).
 */
export function refusedWordsOf(code: string | undefined, operation: 'start' | 'steer', mayReturn: boolean): RefusedWords | null {
  const question = operation === 'start';
  const what = question ? 'вопрос не отправлен' : 'уточнение не отправлено';
  switch (code) {
    case 'ai.account_unreachable': {
      const kept = question ? 'вопрос не отправлен и остался в поле' : 'уточнение не отправлено и осталось в поле';
      const back = question
        ? 'Если он успел засчитаться, сервер сам вернёт его в остаток в течение пяти минут'
        : 'Если оно успело засчитаться, сервер сам вернёт его в остаток в течение пяти минут';
      return { text: `Нет связи с сервером CanonKeeper — ${kept}${mayReturn ? `. ${back}` : ''}`, way: null };
    }
    case 'ai.daily_fence':
      return {
        text: `Сегодня помощник из приложения больше не отвечает: превышен суточный предел его работы. ${question ? 'Вопрос остался' : 'Уточнение осталось'} в поле — задайте его после 00:00 по Москве`,
        way: null,
      };
    case 'ai.app_unfit':
      return { text: `Эта версия CanonKeeper устарела для сервера — ${what}`, way: 'updates' };
    case 'app.request_too_large':
      return { text: `Этот разговор стал слишком длинным для помощника CanonKeeper — ${what}`, way: 'new_chat' };
    default:
      return null;
  }
}

/** «Ответы на ваши вопросы готовятся…» — «разом» while no answer of this app is prepared (Data). */
export const elsewhereWords = 'Ответы на ваши вопросы готовятся на сайте или в другом приложении этого аккаунта';

/** Who holds the places of «разом» (specs/2026-11-15-desk-turns-here.md, Data «слова»), in the order the block says them. */
export const placesWords = {
  otherSaga: 'Ответ на ваш вопрос готовится в другой саге.',
  otherSagas: 'Ответы на ваши вопросы готовятся в других сагах.',
  held: 'Сервер ещё считает недавний вопрос идущим — его место освободится само за несколько минут.',
  elsewhere: elsewhereWords,
} as const;

/**
 * What the block of «разом» says of who holds the places (TH3.2–TH3.4): by the numbers of the server when the quota names them —
 * the answers of this app beyond the chats of this saga the block named are in other sagas, a place the server still counts
 * frees itself, the rest are the site and the other apps —; without them, the rule of the page as before: where they are
 * prepared, while the block named no chat and the window awaits no turn.
 */
export function placesSaying(quota: AccountQuota | undefined, seen: { named: number; watched: number }): readonly string[] {
  const places = quota?.activeTurns.places;
  if (!places) return seen.named === 0 && seen.watched === 0 ? [elsewhereWords] : [];
  const lines: string[] = [];
  const others = places.answering - seen.named;
  if (others > 0) lines.push(others === 1 ? placesWords.otherSaga : placesWords.otherSagas);
  if (places.held > 0) lines.push(placesWords.held);
  if (places.elsewhere > 0) lines.push(placesWords.elsewhere);
  return lines;
}

/** The button or the link of a way of the edition; the question again and «Новый чат» are the core's. */
function wayOf(way: AppWay | null, navigate: RouteNavigate): Pick<ChatFailure, 'action' | 'way'> {
  switch (way) {
    case 'ask_again':
    case 'new_chat':
      return { action: way };
    case 'relink':
      return { way: createElement(RelinkButton) };
    case 'updates':
      return { way: createElement(OpenUpdatesButton, { navigate }) };
    case 'tariffs':
      return { way: createElement(TariffsLink) };
    default:
      return {};
  }
}

/**
 * A question or a correction the server refused because the helper is not connected (409 `helper.not_connected`; AW4.2): the
 * link may have broken on the site at that very moment. What a link undone changes is read again — the link, the settings of
 * the helper, the status of the saga —, and the block says the one reason of the edition over those answers with its way out;
 * «Спросить» and the line of the reading read the same answers.
 */
function helperRefusal(operation: 'start' | 'steer', navigate: RouteNavigate, client: QueryClient): ChatFailure {
  const since = Date.now();
  void client.invalidateQueries({ queryKey: accountQueryKey });
  afterLinkChange(client);
  return {
    label: refusedLabels[operation], text: '',
    reason: createElement(HelperRefusalWords, { operation, since }),
    way: createElement(HelperRefusalWay, { navigate, since }),
  };
}

/** The words and the ways out of the chat of the app over the core's (AW4.1–AW4.5), its navigation and its queries given once. */
function failuresOf(navigate: RouteNavigate, client: QueryClient): ChatFailureWords {
  const tariffWays: ReactNode = [createElement(TariffsLink, { key: 'tariffs' }), createElement(ConnectOwnButton, { key: 'own', navigate })];
  return {
    refused: (error, operation) => {
      const problem = error instanceof ApiStatusError ? error.problem : null;
      if (problem?.code === 'helper.not_connected') return helperRefusal(operation, navigate, client);
      const words = refusedWordsOf(problem?.code, operation, problem?.mayReturn === true);
      return words ? { label: refusedLabels[operation], text: words.text, ...wayOf(words.way, navigate) } : null;
    },
    turn: (job, core, quota) => {
      const words = turnWordsOf(job.error?.code ?? '', quota);
      const failure: ChatFailure = words ? { title: words.title, text: '', ...wayOf(words.way, navigate) } : { ...core };
      return { ...failure, count: countLine(job.questionReturned) };
    },
    tariffWays,
    elsewhere: placesSaying,
  };
}

/**
 * The slot of the chat of the app: the words and the ways out of the refusals and the failed turns of its helper
 * (specs/2026-11-15-desk-account-words-web.md AW4; specs/2026-11-15-desk-interrupted-turn.md IT1.4) and the reason «помощник не
 * подключён» of the edition at «Спросить» (specs/2026-11-15-desk-account-link-web.md AW3.4) — the closed blocks of the site are
 * not the app's.
 */
export function desktopChatSlot(navigate: RouteNavigate, client: QueryClient): ChatSlot {
  return { failures: failuresOf(navigate, client), useHelperReason: useHelperAskReason };
}
