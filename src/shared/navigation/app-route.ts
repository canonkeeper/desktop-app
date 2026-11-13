import type { MouseEvent } from 'react';
import { parseChatSourceTarget } from './chat-source';
import { drawingStates, drawingsListKeys, drawingViews } from './drawings-address';

/** Every surface of the routes: the name of the tab is read from one table by it (specs/2026-11-10-ux1-shell.md SH1.2). */
export const surfaces = [
  'account', 'admin', 'auth', 'chat', 'compare', 'drawing', 'home', 'legal', 'link', 'manuscript', 'memory',
  'notes', 'promo', 'promo-link', 'saga', 'settings', 'tariff', 'unknown', 'writing',
] as const;
export type Surface = typeof surfaces[number];

export interface AppRoute {
  invalidQuery?: boolean;
  path: string;
  surface: Surface;
  query: URLSearchParams;
  overlay?: string;
  sourceReturnHref?: string;
  /** The email typed on the sign-in screen, carried in history state to «Забыли пароль?». */
  authEmail?: string;
  /**
   * A new letter of verification the author asked for on the page of an old link, carried in history state to sign-in
   * and «Откройте почту», never in the address: a reload sends no second letter (specs/2026-11-10-ux3-auth.md AU2.3).
   */
  sendLetter?: boolean;
  /** The idea of a new drawing from the question of a chat turn, carried in history state, never in the address (IW7.3). */
  drawingIdea?: string;
  /** The question a new chat starts with, from «Спросить о нём» of the saga map, in history state (saga map R5.6). */
  chatQuestion?: string;
  /** Where the author came to «Тариф» from by the link of a refusal, in history state: the page offers the way back. */
  returnPlace?: ReturnPlace;
  /** The section the author came from by a link, in history state: the shell offers «← К …» (PS4.1). */
  from?: BackFrom;
  /**
   * The list of «Рисунки» a drawing was opened from, its query in history state (specs/2026-11-10-ux6-drawings-section.md
   * DS1.9): the workplace steps to the neighbours of that list and goes back to it with the same selection.
   */
  drawingList?: string;
}

/**
 * Where the author came from by a link of another section (specs/2026-11-10-ux2-patterns-states.md PS4.1, PS4.2): the
 * section and, when there is one, the name of its object — the beginning of the question of a chat, a hero of the map, the
 * title of a chapter in work, the name of a drawing, the title of a chapter read (`manuscript`, the reader:
 * specs/2026-11-10-ux7-reader.md RD3.2), or a list of a section — the books, the chapters in work, the drawings — and
 * «Что помнит помощник» (specs/2026-11-11-ux9-account.md AC2.6). It lives in the state of the history entry, never in the
 * address: a link opened anew has no way back, and a step of the menu of the sections starts without one (PS4.3).
 */
export const backSections = ['chat', 'saga', 'writing', 'drawing', 'notes', 'manuscript', 'books', 'writings', 'drawings', 'memory'] as const;
export interface BackFrom {
  section: typeof backSections[number];
  name?: string;
}

export function isBackFrom(value: unknown): value is BackFrom {
  if (!value || typeof value !== 'object') return false;
  const { name, section } = value as { name?: unknown; section?: unknown };
  return backSections.some((known) => known === section) && (name === undefined || typeof name === 'string');
}

/**
 * The section the author is in, as a way back to it (specs/2026-11-11-ux9-account.md AC2.6): «Тариф» and «Аккаунт», opened
 * from the menu of the account, offer «← К чату», «← К книгам», «← К рисунку»… — an open object of a section, or its list;
 * none from a page of no section.
 */
export function backFromRoute(route: Pick<AppRoute, 'surface' | 'query'>): BackFrom | undefined {
  switch (route.surface) {
    case 'chat':
    case 'saga':
    case 'notes':
    case 'memory':
      return { section: route.surface };
    case 'manuscript':
      return { section: route.query.has('chapterId') ? 'manuscript' : 'books' };
    case 'writing':
      return { section: route.query.has('draft') ? 'writing' : 'writings' };
    case 'drawing':
      return { section: route.query.has('drawing') ? 'drawing' : 'drawings' };
    default:
      return undefined;
  }
}

/** The same navigation, which marks each step as a step from `from`: the page that opens offers «← К …» back. */
export function navigateFrom(navigate: RouteNavigate, from: BackFrom): RouteNavigate {
  return (path, query, options) => navigate(path, query, options?.back ? options : { ...options, from });
}

/**
 * A place whose refusal leads to «Тариф» (specs/2026-11-10-ux1-drawing.md DX2.8, specs/2026-11-10-ux2-tariff-account.md
 * TA3.6, specs/2026-11-10-ux2-patterns-refusals.md PR2.4): the workplace of a drawing, the list of «Рисунки», the
 * question of a chat, a chapter of «Письмо», the shelf, the map of the saga, a new saga. The page names it
 * in its link back; the way back itself is a step of the browser's history, with what the author typed kept by the place.
 */
export const returnPlaces = ['drawing', 'drawings', 'question', 'chapter', 'shelf', 'map', 'new-saga'] as const;
export type ReturnPlace = typeof returnPlaces[number];

export function isReturnPlace(value: unknown): value is ReturnPlace {
  return returnPlaces.some((place) => place === value);
}

export type RouteNavigate = (
  path: string,
  query?: Record<string, string | undefined>,
  options?: {
    replace?: boolean; back?: boolean; sourceReturnHref?: string; authEmail?: string; sendLetter?: boolean; drawingIdea?: string;
    chatQuestion?: string; returnPlace?: ReturnPlace;
    /** The section this step comes from (PS4.2); `null` — a step of the menu, which starts without a way back (PS4.3). */
    from?: BackFrom | null;
    /** The query of the list of «Рисунки» an opened drawing belongs to (DS1.9); a step that replaces a drawing keeps it. */
    drawingList?: string;
  },
) => void;

const urlBase = 'http://canonkeeper.local';
const allowedValuesBySurface: Partial<Record<Surface, Readonly<Record<string, ReadonlySet<string>>>>> = {
  // The letter to a guest opens «Написать нам» on the home page (specs/2026-11-07-contacts.md F2.5).
  home: {
    contact: new Set(['write']),
  },
  // The admin panel (specs/2026-11-08-admin-redesign.md AR1.1, AR3.3): the section, the period of «Сервис» and the
  // filters of «Писатели».
  admin: {
    tab: new Set(['today', 'writers', 'feedback', 'tiers', 'service', 'promo', 'settings']),
    days: new Set(['7', '14', '30']),
    stage: new Set(['unverified', 'no_documents', 'no_book', 'no_question', 'asking', 'quiet', 'blocked']),
    tier: new Set(['free', 'artist', 'writer', 'pro']),
    flag: new Set(['blocked', 'own']),
    sort: new Set(['new', 'seen']),
  },
  // A document opened from the registration form offers the way back to it (specs/2026-11-11-ux9-account.md AC3.5).
  legal: {
    from: new Set(['register']),
  },
  // «Откуда» and «Все саги» of «Заметки» (specs/2026-11-11-ux7-notes.md NT3.1, NT3.2).
  notes: {
    source: new Set(['free', 'chat', 'chapter']),
    sagas: new Set(['all']),
  },
  // The lightbox of a drawing over the chat and its panel (illustrations R8.5); `new=1` — the author asked for a new chat,
  // while /chat without a chat opens the latest one (specs/2026-11-11-ux4-after-answer.md AA2.1).
  chat: {
    panel: new Set(['info', 'layers']),
    new: new Set(['1']),
  },
  // «Что помнит помощник»: the open part and the kind of the records (specs/2026-11-10-ux2-memory-page.md MP1.1).
  memory: {
    part: new Set(['memory', 'materials']),
    kind: new Set(['plan', 'correction', 'style_rule', 'rejected']),
  },
  // «Рисунки»: the voice presses «Нарисовать» of a new drawing; the tab open beside the drawing (specs/2026-11-05-drawing-one-screen.md
  // D1.1, D2.5); what narrows the list and how it is shown, the panel of a picture open in «Мои картинки» or «Показать все
  // варианты» (specs/2026-11-10-ux6-drawings-section.md DS1.5, DS2.1).
  drawing: {
    then: new Set(['draw']),
    tab: new Set(['check', 'brief', 'edits']),
    state: new Set(drawingStates),
    look: new Set(['1']),
    sort: new Set(['chapters']),
    view: new Set(drawingViews),
    panel: new Set(['info', 'layers']),
  },
  // «Письмо»: the tab open beside the chapter, «Глава» on a narrow screen (specs/2026-11-07-writing-screen.md WS2.4, WS2.5).
  writing: {
    tab: new Set(['chapter', 'idea', 'check', 'versions', 'helper']),
    // A new chapter, not written down until the author writes something (specs/2026-11-11-ux9-writing-a.md WA1.2).
    new: new Set(['idea', 'text']),
    // What «Сверка» shows: the saga or a category of the proofreading (specs/2026-11-07-writing-proofread.md, PR4.2).
    show: new Set(['saga', 'typo', 'spelling', 'punctuation', 'grammar', 'names', 'phrase']),
  },
  manuscript: {
    chapters: new Set(['open']),
    focus: new Set(['1']),
    indexing: new Set(['active']),
    menu: new Set(['open']),
    mode: new Set(['read', 'draft']),
    view: new Set(['open']),
    // «Иллюстрированная книга» and the lightbox of a drawing in the reader (illustrations R8.9).
    illustrated: new Set(['1']),
    panel: new Set(['info', 'layers']),
    // The open tab of «Содержание» of the reader (specs/2026-11-10-ux7-reader.md RD1.7).
    contents: new Set(['chapters', 'notes', 'drawings', 'found']),
  },
};
const allowedSearchKeysBySurface: Record<Surface, ReadonlySet<string>> = {
  // The open thread of «Обратная связь» (specs/2026-11-07-contacts.md F2.2).
  admin: new Set(['tab', 'days', 'stage', 'tier', 'flag', 'sort', 'thread']),
  auth: new Set(['returnTo']),
  // «Написать нам» of the letter to a guest (specs/2026-11-07-contacts.md F2.5).
  home: new Set(['contact']),
  legal: new Set(['from']),
  // The filters, the open note and the place the notes were opened from (specs/2026-11-11-ux7-notes.md NT2.1, NT3.2);
  // `projectId` of an older link names the saga they are opened in (notes-route.ts).
  notes: new Set(['source', 'projectId', 'bookId', 'sagas', 'note', 'returnTo']),
  chat: new Set(['projectId', 'chatId', 'new', 'source', 'sourceMode', 'noteId', 'image', 'panel']),
  // «Разрешения на продвижение» and the page of a letter link (specs/2026-10-30-promo-permissions.md):
  // the value of a link lives in the URL fragment only.
  promo: new Set(),
  'promo-link': new Set(),
  // The public page of a blind comparison: nothing but its slug in the address (specs/2026-11-05-reading-test.md).
  compare: new Set(),
  // «Тариф», open without signing in, and «Аккаунт» (specs/2026-11-10-ux2-tariff-account.md TA2.1, TA4.1); `for` — the code
  // of the refusal the author came with: its card stands out (specs/2026-11-11-ux9-account.md AC2.1).
  tariff: new Set(['for']),
  account: new Set(),
  // The page /link of the site, opened by the app on the computer of the author (specs/2026-11-13-desk-link.md LK1.1): its
  // challenge, its state, the port of its return and its system.
  link: new Set(['challenge', 'state', 'port', 'platform']),
  // «Настройки» of the desktop app: the open part (specs/2026-11-13-desk-settings.md ST1.2).
  settings: new Set(['part']),
  // The saga map: the open node, the surroundings shown, the chapter of the scale, the hidden categories (saga map R6.2) and
  // «только новое» (specs/2026-11-11-ux8-map.md MP3.5).
  saga: new Set(['projectId', 'node', 'focus', 'to', 'hide', 'removed', 'only']),
  // The open drawing (or «new»), the picture on the scene, the drawing of version K, «Нарисовать» of the voice and the tab open
  // (D1.1, D2.5, D4.9); the selection of the list and the panel of an open picture (DS1.5, DS2.1).
  drawing: new Set(['projectId', 'drawing', 'version', 'image', 'then', 'tab', ...drawingsListKeys, 'panel']),
  memory: new Set(['projectId', 'part', 'kind', 'q']),
  // The open chapter, the tab beside it and the version open in «Версии» (specs/2026-11-07-writing-screen.md WS2.4); a new
  // chapter by its entry and the place after a chapter of the book (specs/2026-11-11-ux9-writing-a.md WA1.2).
  writing: new Set(['projectId', 'draft', 'tab', 'version', 'show', 'new', 'book', 'after']),
  manuscript: new Set([
    'projectId',
    'bookId',
    'menu',
    'indexing',
    'mode',
    'chapterId',
    'paragraphId',
    'expectedRevision',
    'chapters',
    'focus',
    'view',
    'illustrated',
    'image',
    'panel',
    // «Содержание» and «Найти» of the reader (specs/2026-11-10-ux7-reader.md RD1.7, RD2.1).
    'contents',
    // The note open beside the text (specs/2026-11-11-ux7-notes.md NT2.2, NT2.3).
    'note',
    'find',
    // The thread of «Связаться с нами» a letter links to (specs/2026-11-07-contacts.md F3.4).
    'contact',
  ]),
  unknown: new Set(),
};
/**
 * The keys one address of a surface takes besides the keys of its surface: the draft of the editor of the desktop app,
 * /manuscript/edit (specs/2026-11-13-desk-editor.md ED1.1). The site has no such page, so its addresses of «Книги» keep none.
 */
const allowedSearchKeysByPath: ReadonlyMap<string, ReadonlySet<string>> = new Map([
  ['/manuscript/edit', new Set(['draftId'])],
]);

export function parseRoute(url: string | URL): AppRoute {
  const parsed = typeof url === 'string' ? new URL(url, urlBase) : url;
  return createAppRoute(parsed.pathname, parsed.searchParams);
}

export function createAppRoute(path: string, search: URLSearchParams | Record<string, string | undefined> = {}) {
  const surface = surfaceFromPath(path);
  const { invalidQuery, query } = sanitizeSearch(surface, search, path);
  const overlay = query.get('menu') || undefined;

  return {
    invalidQuery,
    path,
    surface,
    query,
    overlay,
  } satisfies AppRoute;
}

export function toHref(path: string, query?: Record<string, string | undefined>) {
  const next = new URL(path, urlBase);
  const surface = surfaceFromPath(next.pathname);
  if (surface === 'manuscript' && query?.expectedRevision !== undefined && !isExpectedRevision(query.expectedRevision)) {
    throw new Error('Invalid expected source revision');
  }
  const sanitized = sanitizeSearch(surface, query ?? {}, next.pathname).query;
  for (const [key, value] of sanitized.entries()) {
    next.searchParams.set(key, value);
  }
  return `${next.pathname}${next.search}`;
}

/**
 * A real link to an app route opens in place; a modified click (a new tab or window) stays with
 * the browser. The next page starts from its top, as after a full load.
 */
export function followRoute(navigate: RouteNavigate, path: string) {
  return (event: MouseEvent<HTMLAnchorElement>) => {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return;
    event.preventDefault();
    navigate(path);
    window.document.documentElement.scrollTop = 0;
  };
}

export function isExpectedRevision(value: string) {
  return /^[1-9]\d*$/.test(value) && Number.isSafeInteger(Number(value));
}

function sanitizeSearch(surface: Surface, search: URLSearchParams | Record<string, string | undefined>, path: string) {
  const allowedKeys = allowedSearchKeysBySurface[surface];
  const pathKeys = allowedSearchKeysByPath.get(path);
  const entries = search instanceof URLSearchParams ? search.entries() : Object.entries(search);
  const query = new URLSearchParams();
  let invalidQuery = false;

  for (const [key, rawValue] of entries) {
    // The role picks the text version now; open tabs and bookmarks still carry the old key.
    if (key === 'sourceView') continue;
    if (surface === 'chat' && key === 'bookId') continue;
    if (surface === 'chat' && (key === 'source' || key === 'sourceMode')) {
      if (rawValue !== undefined && String(rawValue).length <= 300) query.append(key, String(rawValue));
      continue;
    }
    if ((!allowedKeys.has(key) && !pathKeys?.has(key)) || rawValue === undefined) {
      if (surface === 'chat') invalidQuery = true;
      continue;
    }
    const value = String(rawValue);
    if (key === 'expectedRevision' && !isExpectedRevision(value)) {
      invalidQuery = true;
      continue;
    }
    const maxLength = (surface === 'auth' || surface === 'notes') && key === 'returnTo' ? 512 : 200;
    if (value.length === 0 || value.length > maxLength) {
      if (surface === 'chat') invalidQuery = true;
      continue;
    }
    const allowedValues = allowedValuesBySurface[surface]?.[key];
    if (allowedValues && !allowedValues.has(value)) {
      if (surface === 'chat') invalidQuery = true;
      continue;
    }
    if ((surface === 'auth' || surface === 'notes') && key === 'returnTo') {
      const returnTo = sanitizeReturnTo(value, surface === 'notes');
      if (returnTo) query.set(key, returnTo);
    } else {
      query.set(key, value);
    }
  }

  // A book is a book of the open saga: «Все саги» has none (NT3.2).
  if (surface === 'notes' && query.get('sagas') === 'all') query.delete('bookId');

  if (surface === 'chat' && !parseChatSourceTarget(query)) {
    query.delete('source');
    query.delete('sourceMode');
  }

  return { invalidQuery, query };
}

function sanitizeReturnTo(value: string, workspaceOnly = false) {
  try {
    const target = new URL(value, urlBase);
    const surface = surfaceFromPath(target.pathname);
    // «Тариф» takes a guest with a key back after signing in (specs/2026-11-10-ux2-tariff-account.md TA3.4); it has no query.
    // The page /link takes the author back after signing in or the registration (specs/2026-11-13-desk-link.md LK1.2).
    const outsideWorkspace = !workspaceOnly && (surface === 'tariff' || surface === 'link');
    if (target.origin !== urlBase || (surface !== 'chat' && surface !== 'manuscript' && surface !== 'notes' && !outsideWorkspace)) return null;
    if (workspaceOnly && target.pathname !== '/chat' && target.pathname !== '/manuscript/books') return null;
    const sanitized = sanitizeSearch(surface, target.searchParams, target.pathname);
    if ((surface === 'manuscript' || workspaceOnly) && sanitized.invalidQuery) return null;
    if (workspaceOnly) {
      const contextKeys = surface === 'chat' ? ['projectId', 'chatId'] : ['projectId', 'bookId', 'chapterId', 'mode'];
      for (const key of [...sanitized.query.keys()]) if (!contextKeys.includes(key)) sanitized.query.delete(key);
    }
    const query = sanitized.query.toString();
    return `${target.pathname}${query ? `?${query}` : ''}`;
  } catch {
    return null;
  }
}

/** The writer of the admin writer card (/admin/writers/:userId), or null on another path. */
export function adminWriterIdFromPath(path: string) {
  const match = /^\/admin\/writers\/([^/]+)$/.exec(path);
  if (!match) return null;
  try {
    return decodeURIComponent(match[1]!);
  } catch {
    return null;
  }
}

/** The test of the page of a blind comparison (/compare/:slug), or null on another path. */
export function compareSlugFromPath(path: string) {
  const match = /^\/compare\/([^/]+)\/?$/.exec(path);
  if (!match) return null;
  try {
    return decodeURIComponent(match[1]!);
  } catch {
    return null;
  }
}

function surfaceFromPath(path: string): Surface {
  if (path === '/') return 'home';
  if (compareSlugFromPath(path) !== null) return 'compare';
  if (path === '/admin' || adminWriterIdFromPath(path) !== null) return 'admin';
  if (path === '/notes') return 'notes';
  if (path === '/saga') return 'saga';
  if (path === '/memory') return 'memory';
  if (path === '/writing') return 'writing';
  if (path === '/drawing') return 'drawing';
  if (path === '/promo-permissions') return 'promo';
  if (path === '/promo/link') return 'promo-link';
  if (path === '/tariff') return 'tariff';
  if (path === '/account') return 'account';
  if (path === '/link') return 'link';
  if (path === '/settings') return 'settings';
  if (path === '/chat') {
    return 'chat';
  }
  const [surfaceKey] = path.split('/').filter(Boolean);
  if (surfaceKey === 'auth' || surfaceKey === 'manuscript' || surfaceKey === 'legal') {
    return surfaceKey;
  }
  return 'unknown';
}
