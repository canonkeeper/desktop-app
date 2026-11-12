import type { AppRoute, Surface } from '../../shared/navigation/app-route';
import { sectionEntry, type SagaSection } from '../saga-sections';

/**
 * Which surface stands in the one shell of a signed-in author (specs/2026-11-10-ux2-shell-core.md SC1.1): the shell, or
 * a page of its own — sign-in and its link pages, the guest home, the admin panel, the page of a blind comparison.
 */
export const shellOfSurface: Readonly<Record<Surface, boolean>> = {
  account: true,
  admin: false,
  auth: false,
  chat: true,
  compare: false,
  drawing: true,
  home: false,
  legal: true,
  manuscript: true,
  memory: true,
  notes: true,
  promo: true,
  'promo-link': false,
  saga: true,
  tariff: true,
  unknown: true,
  writing: true,
};

/** The section of the menu each surface is: the reader is «Книги» (S2.2), a page without a saga has none. */
const sectionOfSurface: Readonly<Partial<Record<Surface, SagaSection>>> = {
  chat: 'chat',
  drawing: 'drawing',
  manuscript: 'manuscript',
  memory: 'memory',
  notes: 'notes',
  saga: 'saga',
  writing: 'writing',
};

export function activeSection(route: Pick<AppRoute, 'surface'>): SagaSection | null {
  return sectionOfSurface[route.surface] ?? null;
}

/** The reader of a book: the shelf of the same surface opened at a chapter, a source or the source of a chat. */
export function isReaderRoute(route: Pick<AppRoute, 'query' | 'surface'>) {
  return route.surface === 'manuscript'
    && (route.query.get('mode') === 'read' || route.query.has('source') || route.query.has('chatId'));
}

/**
 * The sections stand in a narrow column at any width where the room goes to the work (SC1.2, D2.3): an open chapter of
 * «Письмо», an open drawing, the reader. The rule is the address, not a word of the page.
 */
export function narrowColumn(route: Pick<AppRoute, 'query' | 'surface'>) {
  if (route.surface === 'writing') return Boolean(route.query.get('draft'));
  if (route.surface === 'drawing') {
    const open = route.query.get('drawing');
    return Boolean(open && open !== 'new');
  }
  return isReaderRoute(route);
}

/**
 * The two widths of the content of the shell (specs/2026-11-10-ux2-patterns-states.md PS1.2, П-3): «чтение и формы» — up to
 * 720 px in the middle; «списки и рабочие места» — the whole width. The width is the shell's, by the surface; a page keeps
 * no `max-width` of its own.
 */
export type ContentWidth = 'reading' | 'wide';

const readingSurfaces: ReadonlySet<Surface> = new Set<Surface>(['account', 'legal', 'promo', 'tariff', 'unknown']);

export function contentWidth(route: Pick<AppRoute, 'surface'>): ContentWidth {
  return readingSurfaces.has(route.surface) ? 'reading' : 'wide';
}

/** The surfaces whose saga comes from the address; the rest name the last saga of the author (SC1.3). */
export function sagaInAddress(route: Pick<AppRoute, 'surface'>) {
  return route.surface in sectionOfSurface;
}

/**
 * The word of a page: the section by the word of its menu item, a page without one by its own name (SC1.4). The tab and the
 * name of the content say it; the top bar does not (specs/2026-11-11-ux2-polish.md PL1.1).
 */
export function sectionWord(route: Pick<AppRoute, 'surface'>, fallback: string | null) {
  const section = activeSection(route);
  return section ? sectionEntry(section).label : fallback;
}
