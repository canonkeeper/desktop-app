import { useEffect } from 'react';
import type { AppRoute, Surface } from './app-route';

/** The name of the product, one word everywhere (specs/2026-11-10-ux1-shell.md SH5.1). */
export const productName = 'CanonKeeper';
/** The tab of a page without a section: the home page, the first load. */
export const productTitle = `${productName} — помощник для вашей саги`;

/**
 * The parts of the title of the tab (SH1.1): the mark of a ready answer or a ready saga, the object of the page (a chapter,
 * a drawing, a book), the section and the saga.
 */
export type TitlePart = 'mark' | 'object' | 'section' | 'saga';
export type TitleParts = Partial<Record<TitlePart, string>>;

/** `[● mark · ][object · ]section[ · saga] — CanonKeeper`; without a section — the name of the product. */
export function formatPageTitle({ mark, object, saga, section }: TitleParts) {
  const marked = (title: string) => mark ? `● ${mark} · ${title}` : title;
  if (!section) return marked(productTitle);
  return marked(`${[object, section, saga].filter(Boolean).join(' · ')} — ${productName}`);
}

/**
 * The section of every surface of the routes (SH1.2). The home page is the product itself: its tab is the name of the
 * product, as in `index.html`.
 */
export const sectionTitles: Readonly<Record<Surface, string | null>> = {
  account: 'Аккаунт',
  admin: 'Админка',
  auth: 'Вход',
  chat: 'Чат',
  compare: 'Какой текст приятнее читать',
  drawing: 'Рисунки',
  home: null,
  legal: 'Документы',
  // The page /link of the site (specs/2026-11-13-desk-link.md LK1.3).
  link: 'Связать приложение',
  manuscript: 'Книги',
  memory: 'Что помнит помощник',
  notes: 'Заметки',
  promo: 'Разрешения на продвижение',
  'promo-link': 'Разрешение на продвижение',
  saga: 'Карта',
  settings: 'Настройки',
  tariff: 'Тариф',
  unknown: 'Страница не найдена',
  writing: 'Письмо',
};

/** The pages of sign-in, each named by its own words: by the first word of the page in `/auth/<page>`. */
const authTitles: Readonly<Record<string, string>> = {
  register: 'Регистрация',
  forgot: 'Восстановление пароля',
  reset: 'Новый пароль',
  verify: 'Подтверждение почты',
  check: 'Подтвердите почту',
};

/** The section the address names: the shelf and the reader share a surface, the pages of sign-in share another. */
export function sectionTitle(route: Pick<AppRoute, 'path' | 'query' | 'surface'>) {
  if (route.surface === 'auth') return authTitles[route.path.split('/')[2]?.split('-')[0] ?? ''] ?? sectionTitles.auth;
  if (route.surface === 'manuscript' && route.query.get('mode') === 'read') return 'Чтение';
  return sectionTitles[route.surface];
}

interface Entry {
  part: TitlePart;
  value: string;
}

/** Who holds each part now: the latest holder wins, and the one before comes back when it leaves. */
const entries: Entry[] = [];

function partOf(part: TitlePart) {
  for (let index = entries.length - 1; index >= 0; index -= 1) {
    if (entries[index]!.part === part) return entries[index]!.value;
  }
  return undefined;
}

/** The only writer of `document.title` in the product. */
function writeTitle() {
  document.title = formatPageTitle({ mark: partOf('mark'), object: partOf('object'), saga: partOf('saga'), section: partOf('section') });
}

/** Holds one part of the title until the returned release is called. */
export function holdTitlePart(part: TitlePart, value: string) {
  const entry: Entry = { part, value };
  entries.push(entry);
  writeTitle();
  return () => {
    const index = entries.indexOf(entry);
    if (index < 0) return;
    entries.splice(index, 1);
    writeTitle();
  };
}

/**
 * A part of the title while the component stands (SH1.3): the section by the root, the saga by the shell that knows it,
 * the object by its page. A part the server has not named yet is not held.
 */
export function usePageTitlePart(part: Exclude<TitlePart, 'mark'>, value: string | null | undefined) {
  const text = value?.trim();
  useEffect(() => (text ? holdTitlePart(part, text) : undefined), [part, text]);
}

/**
 * «● Ответ готов», «● Сага готова» (R6.3, FS4): the mark stands before the title of whatever page is open until the author
 * comes back to the tab.
 */
export function markHiddenTab(message: string) {
  const release = holdTitlePart('mark', message);
  const restore = () => {
    if (document.visibilityState !== 'visible') return;
    release();
    document.removeEventListener('visibilitychange', restore);
  };
  document.addEventListener('visibilitychange', restore);
}
