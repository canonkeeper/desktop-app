import type { MouseEvent } from 'react';
import { parseChatSourceTarget } from './chat-source';

export type Surface = 'admin' | 'auth' | 'chat' | 'compare' | 'drawing' | 'gallery' | 'home' | 'illustrations' | 'legal' | 'manuscript' | 'memory' | 'notes' | 'promo' | 'promo-link' | 'saga' | 'unknown' | 'writing';

export interface AppRoute {
  invalidQuery?: boolean;
  path: string;
  surface: Surface;
  query: URLSearchParams;
  overlay?: string;
  sourceReturnHref?: string;
  /** The email typed on the sign-in screen, carried in history state to «Забыли пароль?». */
  authEmail?: string;
  /** The idea of a new drawing from the question of a chat turn, carried in history state, never in the address (IW7.3). */
  drawingIdea?: string;
  /** The question a new chat starts with, from «Спросить о нём» of the saga map, in history state (saga map R5.6). */
  chatQuestion?: string;
}

export type RouteNavigate = (
  path: string,
  query?: Record<string, string | undefined>,
  options?: { replace?: boolean; back?: boolean; sourceReturnHref?: string; authEmail?: string; drawingIdea?: string; chatQuestion?: string },
) => void;

const urlBase = 'http://canonkeeper.local';
const allowedValuesBySurface: Partial<Record<Surface, Readonly<Record<string, ReadonlySet<string>>>>> = {
  // The letter to a guest opens «Написать нам» on the home page (specs/2026-11-07-contacts.md F2.5).
  home: {
    contact: new Set(['write']),
  },
  // The admin panel (admin panel R6.1): the tab, the period of «Сводка» and the stage filter.
  admin: {
    tab: new Set(['writers', 'days', 'service', 'requests', 'feedback', 'limits', 'permissions']),
    days: new Set(['7', '14', '30']),
    stage: new Set(['unverified', 'no_documents', 'no_book', 'no_question', 'asking', 'quiet', 'blocked']),
  },
  notes: {
    source: new Set(['free', 'chat', 'chapter']),
  },
  // The lightbox of a drawing over the chat and its panel (illustrations R8.5).
  chat: {
    panel: new Set(['info', 'layers']),
  },
  memory: {
    kind: new Set(['plan', 'correction', 'style_rule', 'rejected']),
  },
  // «Рисование»: the voice presses «Нарисовать» of a new drawing; the tab open beside the drawing (specs/2026-11-05-drawing-one-screen.md D1.1, D2.5).
  drawing: {
    then: new Set(['draw']),
    tab: new Set(['check', 'brief', 'edits']),
  },
  // «Письмо»: the tab open beside the chapter, «Глава» on a narrow screen (specs/2026-11-07-writing-screen.md WS2.4, WS2.5).
  writing: {
    tab: new Set(['chapter', 'idea', 'check', 'versions', 'helper']),
    // What «Сверка» shows: the saga or a category of the proofreading (specs/2026-11-07-writing-proofread.md, PR4.2).
    show: new Set(['saga', 'typo', 'spelling', 'punctuation', 'grammar', 'names', 'phrase']),
  },
  // «Галерея» of the saga: its filters, view and lightbox (illustrations R8.8, R8.5, R11.9).
  gallery: {
    kind: new Set(['character', 'place', 'scene', 'item', 'sheet', 'other', 'upload']),
    refs: new Set(['1']),
    view: new Set(['chronicle', 'grid']),
    panel: new Set(['info', 'layers']),
  },
  manuscript: {
    bookMenu: new Set(['open']),
    chapters: new Set(['open']),
    focus: new Set(['1']),
    indexing: new Set(['active']),
    menu: new Set(['open']),
    mode: new Set(['read', 'draft']),
    view: new Set(['open']),
    // «Иллюстрированная книга» and the lightbox of a drawing in the reader (illustrations R8.9).
    illustrated: new Set(['1']),
    panel: new Set(['info', 'layers']),
  },
};
const allowedSearchKeysBySurface: Record<Surface, ReadonlySet<string>> = {
  // The open thread of «Обратная связь» (specs/2026-11-07-contacts.md F2.2).
  admin: new Set(['tab', 'days', 'stage', 'thread']),
  auth: new Set(['returnTo']),
  // «Написать нам» of the letter to a guest (specs/2026-11-07-contacts.md F2.5).
  home: new Set(['contact']),
  legal: new Set(),
  notes: new Set(['source', 'projectId', 'bookId', 'returnTo']),
  chat: new Set(['projectId', 'chatId', 'source', 'sourceMode', 'noteId', 'image', 'panel']),
  // The page of the feature «Иллюстрации саги» (illustrations R11.4).
  illustrations: new Set(['projectId']),
  // «Разрешения на продвижение» and the page of a letter link (specs/2026-10-30-promo-permissions.md):
  // the value of a link lives in the URL fragment only.
  promo: new Set(),
  'promo-link': new Set(),
  // The public page of a blind comparison: nothing but its slug in the address (specs/2026-11-05-reading-test.md).
  compare: new Set(),
  // The saga map: the open node, the surroundings shown, the chapter of the scale, the hidden categories (saga map R6.2).
  saga: new Set(['projectId', 'node', 'focus', 'to', 'hide', 'materials', 'removed']),
  gallery: new Set(['projectId', 'subject', 'chapter', 'kind', 'refs', 'view', 'versions', 'image', 'panel']),
  // The open drawing (or «new»), the picture on the scene, the drawing of version K, «Нарисовать» of the voice and the tab open (D1.1, D2.5, D4.9).
  drawing: new Set(['projectId', 'drawing', 'version', 'image', 'then', 'tab']),
  memory: new Set(['projectId', 'kind', 'q']),
  // The open chapter, the tab beside it and the version open in «Версии» (specs/2026-11-07-writing-screen.md WS2.4).
  writing: new Set(['projectId', 'draft', 'tab', 'version', 'show']),
  manuscript: new Set([
    'projectId',
    'bookId',
    'bookMenu',
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
    // The thread of «Связаться с нами» a letter links to (specs/2026-11-07-contacts.md F3.4).
    'contact',
  ]),
  unknown: new Set(),
};

export function parseRoute(url: string | URL): AppRoute {
  const parsed = typeof url === 'string' ? new URL(url, urlBase) : url;
  return createAppRoute(parsed.pathname, parsed.searchParams);
}

export function createAppRoute(path: string, search: URLSearchParams | Record<string, string | undefined> = {}) {
  const surface = surfaceFromPath(path);
  const { invalidQuery, query } = sanitizeSearch(surface, search);
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
  const sanitized = sanitizeSearch(surface, query ?? {}).query;
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

export function canonicalProductRoutes() {
  return [
    { label: 'Мои книги', path: '/manuscript/books' },
    { label: 'Чат', path: '/chat' },
    { label: 'Карта саги', path: '/saga' },
    { label: 'Письмо', path: '/writing' },
    { label: 'Рисование', path: '/drawing' },
    { label: 'Галерея', path: '/gallery' },
    { label: 'Заметки', path: '/notes' },
  ] as const;
}

export function isExpectedRevision(value: string) {
  return /^[1-9]\d*$/.test(value) && Number.isSafeInteger(Number(value));
}

function sanitizeSearch(surface: Surface, search: URLSearchParams | Record<string, string | undefined>) {
  const allowedKeys = allowedSearchKeysBySurface[surface];
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
    if (!allowedKeys.has(key) || rawValue === undefined) {
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

  if (surface === 'notes' && !query.has('projectId')) query.delete('bookId');

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
    if (target.origin !== urlBase || (surface !== 'chat' && surface !== 'manuscript' && surface !== 'notes')) return null;
    if (workspaceOnly && target.pathname !== '/chat' && target.pathname !== '/manuscript/books') return null;
    const sanitized = sanitizeSearch(surface, target.searchParams);
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
  if (path === '/gallery') return 'gallery';
  if (path === '/drawing') return 'drawing';
  if (path === '/illustrations') return 'illustrations';
  if (path === '/promo-permissions') return 'promo';
  if (path === '/promo/link') return 'promo-link';
  if (path === '/chat') {
    return 'chat';
  }
  const [surfaceKey] = path.split('/').filter(Boolean);
  if (surfaceKey === 'auth' || surfaceKey === 'manuscript' || surfaceKey === 'legal') {
    return surfaceKey;
  }
  return 'unknown';
}
