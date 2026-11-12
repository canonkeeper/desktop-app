import type { AppRoute, RouteNavigate } from '../../shared/navigation/app-route';
import { parseHidden } from './map-model';

export interface MapParams {
  node?: string;
  focus?: string;
  to?: string;
  hide?: string;
  /** The list «Убранное с карты» is open (R11.3). */
  removed?: string;
  /** `new`: only the records of the author the book does not know, the rest pale (specs/2026-11-11-ux8-map.md MP3.5). */
  only?: string;
}

/** The address «Показать новые на карте» of «Что помнит помощник» opens (MP3.5). */
export const onlyNewOnMap = { only: 'new' } as const;

/**
 * The state of the saga map lives in the URL (specs/2026-11-04-saga-map.md, R6.2): the open node, the node whose
 * surroundings are shown, the chapter key of the scale, the hidden categories and «только новое».
 */
export function mapRoute(route: AppRoute, navigate: RouteNavigate, projectId: string) {
  const param = (name: keyof MapParams) => route.query.get(name) ?? undefined;
  const current: MapParams = { node: param('node'), focus: param('focus'), to: param('to'), hide: param('hide'), removed: param('removed'), only: param('only') };
  return {
    ...current,
    hidden: parseHidden(current.hide),
    onlyNew: current.only === onlyNewOnMap.only,
    /** Changes the map; replace keeps the moves of the scale out of the history. */
    update(patch: MapParams, options?: { replace?: boolean }) {
      navigate('/saga', { projectId, ...current, ...patch }, options);
    },
  };
}

export type MapRoute = ReturnType<typeof mapRoute>;
