// SPDX-License-Identifier: AGPL-3.0-only
import { charactersRange } from '../account-reading/characters';
import type { AccountInfo, DesktopAccount } from './account-api';

/**
 * The words of the link with an account and of the part «Аккаунт» — Data of specs/2026-11-15-desk-account-link-web.md, the
 * build without the workshops (boards 44·4–44·8, 44·12, 44·16): one name an action — «Связать с аккаунтом» the first time,
 * «Связать снова» a repeat and a way back, «Обновить сведения» a new read of the account.
 */
export const accountWords = {
  intro: 'С аккаунтом CanonKeeper помощник отвечает на вопросы по книгам и читает их — по вашему тарифу. Настраивать нечего. Книги лежат на этом компьютере',
  link: 'Связать с аккаунтом',
  relink: 'Связать снова',
  server: 'Что проходит через сервер',
  // specs/2026-11-19-desk-trust.md TW2.5, TW1.1: what goes through the server, and with whom the helper works.
  serverLine: 'Через сервер CanonKeeper к помощнику проходят: разговор чата с каждым вопросом — ваши вопросы, ответы помощника и места книг, которые он прочитал, а когда он ищет по всей саге — главы целиком, — и приложенные главы; при чтении книг — их текст; ваши материалы, когда вы их загружаете. Книги, главы и чаты лежат на этом компьютере',
  provider: 'Помощник работает у OpenAI (ChatGPT) или другого крупного облачного поставщика. Обучение на ваших текстах выключено',
  unsupported: 'Эту сборку CanonKeeper нельзя связать с аккаунтом',
  startFailed: 'Связь не началась. Попробуйте ещё раз',
  loadFailed: 'Не открылись сведения о связи с аккаунтом',
  // A refusal of «Отменить» of the waiting and of the choice of the helper from the line of the end (but no connection).
  actionFailed: 'Не получилось — попробуйте ещё раз',
  // The window of the waiting (board 44·5).
  waitingTitle: 'Подтвердите связь в браузере: там открылась страница CanonKeeper',
  address: 'Адрес страницы связи',
  copy: 'Скопировать адрес',
  copied: 'Адрес скопирован',
  notCopied: 'Адрес не скопировался — выделите его и скопируйте сами',
  openAgain: 'Открыть ещё раз',
  cancel: 'Отменить',
  close: 'Закрыть окно связи',
  noBrowser: 'Браузер не открылся? Скопируйте адрес и откройте его сами',
  waiting: 'Ждёт подтверждения в браузере',
  openWindow: 'Открыть окно связи',
  // Linked (board 44·7).
  refresh: 'Обновить сведения',
  tariffs: 'Тарифы',
  apps: 'Приложения на компьютере',
  more: 'Действия связи с аккаунтом',
  unlinkItem: 'Отвязать приложение…',
  offlineEmpty: 'Приложение связано. Сведения об аккаунте появятся, когда будет связь с сервером',
  broken: 'Связь с аккаунтом прервана. Так бывает после смены пароля, «Выйти везде» или отвязки на сайте',
  storeSystem: 'Связь с аккаунтом лежит в хранилище паролей системы',
  storeFile: 'Связь с аккаунтом лежит в файле в папке данных без шифрования: в системе нет хранилища паролей',
  refreshFailed: 'Сведения не обновились. Попробуйте ещё раз',
  // Unlinking (board 44·8).
  unlinkTitle: 'Отвязать приложение от аккаунта?',
  unlinkLine: 'Помощник CanonKeeper перестанет отвечать и читать книги, пока вы не свяжете приложение снова. Книги и всё, что помощник уже узнал о них, останутся на компьютере',
  unlinkAction: 'Отвязать',
  unlinked: 'Приложение отвязано от аккаунта',
  unlinkedHere: 'Приложение отвязано на этом компьютере. Сайт об этом не узнал: в «Приложениях на компьютере» оно может остаться — отвяжите его там',
  unlinkFailed: 'Приложение не отвязалось. Попробуйте ещё раз',
  // The erasure (board 44·12).
  erase: 'Приложение будет отвязано от аккаунта CanonKeeper. Если сервер не ответит, отвяжите его в «Приложениях на компьютере» на сайте',
  // The outcome of a link (board 44·5).
  chooseHelper: 'Выбрать помощника CanonKeeper',
} as const;

/**
 * «Осталось 9 минут», «Осталась 1 минута», in the last minute «Осталось меньше минуты» (AW1.1): by the term of the server, the
 * whole minutes left before it — a link just started says 9 of its 10.
 */
export function remainingWords(expiresAt: string, now: number) {
  const minutes = Math.floor((Date.parse(expiresAt) - now - 1) / 60_000);
  if (minutes < 1) return 'Осталось меньше минуты';
  const last = minutes % 10;
  const teen = minutes % 100 >= 11 && minutes % 100 <= 14;
  if (last === 1 && !teen) return `Осталась ${minutes} минута`;
  if (last >= 2 && last <= 4 && !teen) return `Осталось ${minutes} минуты`;
  return `Осталось ${minutes} минут`;
}

/** «Ждёт подтверждения в браузере · Осталось 9 минут» in the place of a button «Связать…» (AW1.2). */
export function waitingLine(expiresAt: string, now: number) {
  return `${accountWords.waiting} · ${remainingWords(expiresAt, now)}`;
}

/** «Сайт увидит имя этого компьютера «…», систему и версию приложения» (AW1.1). */
export function computerLine(computer: string) {
  return `Сайт увидит имя этого компьютера «${computer}», систему и версию приложения`;
}

type Failure = NonNullable<DesktopAccount['failure']>;

/**
 * Why a link did not go through (AW1.3, AW2.4): the term, the site, or the store of the pass — named as the server says where
 * the shell keeps it.
 */
export function failureLine(failure: Failure, passStore: DesktopAccount['passStore']) {
  switch (failure) {
    case 'expired':
      return 'Связать не получилось: время на подтверждение вышло';
    case 'exchange':
      return 'Связать не получилось: сайт не подтвердил связь';
    case 'keyring':
      return passStore === 'file'
        ? 'Связь не сохранилась: не получилось записать её в папку данных'
        : 'Связь не сохранилась: хранилище паролей системы не ответило';
  }
}

/** The address with its mask does not break after «…»: a word joiner holds it to the rest (board 44·23). */
function unbroken(email: string) {
  return email.replaceAll('…', '…\u2060');
}

/**
 * The outcome of a link made (AW1.3, AW3.2): the address of the account with its mask and who answers now, by the source the
 * server chose. «Свой ИИ» answers — «Отвечает ваш помощник»; no helper chosen (the site told none) — only the link.
 */
export function linkedLine(email: string | null, source: 'none' | 'own' | 'account') {
  const linked = email ? `Приложение связано с аккаунтом ${unbroken(email)}` : 'Приложение связано с аккаунтом';
  if (source === 'account') return `${linked}. Отвечает помощник CanonKeeper`;
  if (source === 'own') return `${linked}. Отвечает ваш помощник`;
  return linked;
}

const dayFormatter = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long' });
const timeFormatter = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit' });
// The end of a tier is a day of the Moscow calendar, as the site writes it (src/features/tiers/tiers-copy.ts).
const moscowDayFormatter = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', timeZone: 'Europe/Moscow' });

function sameDay(first: Date, second: Date) {
  return first.getFullYear() === second.getFullYear() && first.getMonth() === second.getMonth() && first.getDate() === second.getDate();
}

/** «Сведения от 14:05», not of today — «Сведения от 13 ноября, 14:05» (AW2.2). */
export function freshLine(at: string, now: number) {
  const told = new Date(at);
  const time = timeFormatter.format(told);
  return sameDay(told, new Date(now)) ? `Сведения от ${time}` : `Сведения от ${dayFormatter.format(told)}, ${time}`;
}

/** «Нет связи с сервером CanonKeeper. Сведения от 14:05» (AW2.3). */
export function offlineLine(at: string, now: number) {
  return `Нет связи с сервером CanonKeeper. ${freshLine(at, now)}`;
}

/**
 * «Тариф «Писатель» до 14 декабря» / «Тариф «Бесплатно»»: «до …» only with the end the server names, a day of the Moscow
 * calendar whatever the zone of the computer (AW2.2).
 */
export function tierLine(tier: AccountInfo['tier']) {
  if (!tier.until) return `Тариф «${tier.title}»`;
  return `Тариф «${tier.title}» до ${moscowDayFormatter.format(new Date(tier.until))}`;
}

/** «Вопросы сегодня: 12 из 30» (AW2.2). */
export function questionsLine(questions: NonNullable<AccountInfo['questions']>) {
  return `Вопросы сегодня: ${questions.used} из ${questions.limit}`;
}

/**
 * «Чтение книг за 30 дней: использовано 3,2 из 40 млн знаков» — the counter of the tier is «использовано» (AW2.2), its number by
 * the one function of the characters of the tier (specs/2026-11-15-desk-account-words-web.md AW5.6).
 */
export function readingLine(reading: AccountInfo['reading']) {
  return `Чтение книг за 30 дней: использовано ${charactersRange(reading.used, reading.limit)} знаков`;
}

/** «Анна · a…@example.com» (AW2.2). */
export function headLine(account: AccountInfo) {
  return `${account.name} · ${unbroken(account.email)}`;
}

/** «Этот компьютер на сайте: «…»» (AW2.2). */
export function computerOnSite(computer: string) {
  return `Этот компьютер на сайте: «${computer}»`;
}
