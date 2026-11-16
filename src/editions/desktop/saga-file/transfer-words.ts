// SPDX-License-Identifier: AGPL-3.0-only
import type { Schema } from '../../../shared/api';
import { formatFileSize } from '../../../ui/copy';

/** The codes a transfer that failed names (`SiteSagaTransfer.code`, specs/2026-11-16-desk-site-saga.md TR2.2). */
export type TransferCode = NonNullable<Schema<'SiteSagaTransfer'>['code']>;

const time = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });

/** «Хроники Белого порта.cksaga» as the site names the file, «Хроники Белого порта» as the author knows the saga. */
function sagaOf(fileName: string | null) {
  const name = fileName?.replace(/\.cksaga$/iu, '').trim();
  return name ? `«${name}»` : null;
}

/** What goes on while the saga comes, without the count: what a screen reader hears once. */
function downloadingSaga(fileName: string | null) {
  const saga = sagaOf(fileName);
  return saga ? `Скачиваю сагу ${saga}` : 'Скачиваю сагу';
}

/**
 * The words of the transfer of a saga from the site, word for word by board 44 (rows 44·77–44·80; Data of
 * specs/2026-11-16-desk-boards13.md); the lines the board does not name — the decision log of specs/2026-11-16-desk-site-saga.md.
 */
export const transferWords = {
  /** The main button of the card of the first screen (44·77). */
  card: 'Перенести с сайта…',
  /** The button of «Данные» and the item of the menu of sagas (44·78). */
  entry: 'Перенести сагу с сайта…',
  needLink: 'Чтобы перенести сагу с сайта, свяжите приложение с аккаунтом CanonKeeper',
  /** «Перенести с сайта…» while a transfer downloads or opens: the server would start no second one. */
  goingReason: 'Сага уже переносится',
  waitingTitle: 'Перенесите сагу на сайте',
  waitingText: (email: string | null) => (email
    ? `В браузере открылась страница CanonKeeper. Войдите в аккаунт ${email}, выберите сагу и нажмите «Перенести в приложение».`
    : 'В браузере открылась страница CanonKeeper. Войдите в аккаунт, с которым связано приложение, выберите сагу и нажмите «Перенести в приложение».'),
  until: (expiresAt: string) => `Ссылка действует до ${time.format(new Date(expiresAt))}`,
  openAgain: 'Открыть страницу снова',
  cancel: 'Отменить',
  close: 'Закрыть',
  preparing: 'Сайт готовит сагу…',
  preparingLong: 'До нескольких минут',
  downloadingSaga,
  downloading: (fileName: string | null, bytes: number) => `${downloadingSaga(fileName)} · ${formatFileSize(bytes)}`,
  opening: (fileName: string | null) => {
    const saga = sagaOf(fileName);
    return saga ? `Открываю сагу ${saga}` : 'Открываю сагу';
  },
  stop: 'Остановить',
  openingReason: 'Сага уже открывается',
  /** A start the backend did not take for a reason the page has no words of its own for. */
  startFailed: 'Не удалось начать перенос саги',
  stopFailed: 'Не удалось остановить перенос — попробуйте ещё раз',
  /** The general line of a failure this computer had (`unexpected`). */
  failed: 'Не удалось перенести сагу',
  updating: 'Ставлю обновление — перенесите сагу после перезапуска',
} as const;

/** What the line of a failed transfer says, and whether it offers «Написать нам». */
export interface TransferRefusal {
  message: string;
  write?: true;
}

const startAgain = 'начните перенос снова';

/**
 * A failed transfer by its code (TR6.2; the four lines of 44·80 and the rest by the decision log of the spec): one line of the
 * one area of the toasts. `canceled` says nothing: the author stopped it himself. «Сайт занят» names no minutes — the backend
 * does not say when the site is free — and no other saga: the site is busy also when the saves of the hour ran out (the
 * decision log).
 */
export const transferRefusals: Readonly<Record<TransferCode, TransferRefusal | null>> = {
  expired: { message: `Ссылка на сайте устарела — ${startAgain}` },
  canceled: null,
  transfer_invalid: { message: `Сайт не принял перенос — ${startAgain}` },
  gone: { message: `Этой саги на сайте уже нет — ${startAgain}` },
  busy: { message: 'Сайт занят и не успел собрать сагу — начните перенос позже' },
  unreachable: { message: `Нет связи с сайтом — проверьте интернет и ${startAgain}` },
  not_linked: { message: 'Приложение отвязано от аккаунта — перенос остановлен' },
  download_cut: { message: `Сага скачалась не до конца — ${startAgain}` },
  too_large: { message: 'Сага больше 1 ГБ — такую сагу CanonKeeper не открывает' },
  no_space: { message: `На диске не хватает места для саги. Освободите место и ${startAgain}` },
  session_ended: { message: `Окно приложения перезапускалось — ${startAgain}` },
  updating: { message: 'Ставлю обновление — перенесите сагу после перезапуска' },
  unexpected: { message: transferWords.failed, write: true },
  'saga_archive.not_archive': { message: `Сайт прислал не файл саги — ${startAgain}` },
  'saga_archive.damaged': { message: `Сага пришла повреждённой — ${startAgain}` },
  'saga_archive.newer_version': { message: 'Сага сохранена более новой версией CanonKeeper. Обновите приложение и начните перенос снова' },
  'saga_archive.too_large': { message: 'Сага больше 1 ГБ — такую сагу CanonKeeper не открывает' },
  'saga_archive.no_space': { message: `На диске не хватает места для саги. Освободите место и ${startAgain}` },
  'saga_archive.invalid': { message: 'В саге с сайта ошибка, и CanonKeeper не может её открыть', write: true },
};
