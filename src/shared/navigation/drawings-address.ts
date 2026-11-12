/**
 * The list of «Рисунки» in its address (specs/2026-11-10-ux6-drawings-section.md DS1.5): what narrows it and how it is
 * shown. The section lives at `/drawing`; the address of an open drawing (`drawing`) is the workplace, `image=K` opens the
 * drawing of version K, or — in «Мои картинки» and «Показать все варианты» — the picture K itself.
 */
export const drawingStates = ['waiting', 'failed'] as const;
export type DrawingState = typeof drawingStates[number];

/** «Списком», «Мои картинки», «Показать все варианты»; without it — the tiles (or the view the browser remembers). */
export const drawingViews = ['list', 'mine', 'variants'] as const;
export type DrawingView = typeof drawingViews[number];

/** The parameters of the list, in the order the address writes them. */
export const drawingsListKeys = ['q', 'state', 'subject', 'chapter', 'look', 'sort', 'view'] as const;
export type DrawingsListKey = typeof drawingsListKeys[number];

/**
 * An address of the former «Галерея» in the words of «Рисунки» (DS2.2): the hero is `subject`, the chapter `chapter`, «только
 * образцы» `look`, «Таблицей» (the gallery's own view) `view=list`, «Сеткой» the tiles, «По ходу саги» `sort=chapters`,
 * «мои картинки» the view «Мои картинки», «Все версии» the view «Показать все варианты». The picture open there stays open:
 * a drawing of the workshop opens at its workplace, the author's own picture in «Мои картинки».
 */
export function galleryAddress(search: URLSearchParams): Record<string, string | undefined> {
  const value = (key: string) => search.get(key) || undefined;
  const mine = value('kind') === 'upload';
  const variants = !mine && value('versions') === 'all';
  const view = value('view');
  const pictures = mine || variants;
  return {
    projectId: value('projectId'),
    subject: mine ? undefined : value('subject'),
    chapter: mine ? undefined : value('chapter'),
    look: !mine && value('refs') === '1' ? '1' : undefined,
    sort: !mine && view === 'chronicle' ? 'chapters' : undefined,
    view: mine ? 'mine' : variants ? 'variants' : view === 'grid' || view === 'chronicle' ? undefined : 'list',
    image: value('image'),
    panel: pictures ? value('panel') : undefined,
  };
}
