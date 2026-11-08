import type { AppRoute, RouteNavigate } from '../../shared/navigation/app-route';
import { parseHidden } from './map-model';

export interface MapParams {
  node?: string;
  focus?: string;
  to?: string;
  hide?: string;
  /** The panel «Материалы автора» is open (R9.2). */
  materials?: string;
  /** The list «Убранное» is open (R11.3). */
  removed?: string;
}

/**
 * The state of the saga map lives in the URL (specs/2026-11-04-saga-map.md, R6.2): the open node, the node whose
 * surroundings are shown, the chapter key of the scale and the hidden categories.
 */
export function mapRoute(route: AppRoute, navigate: RouteNavigate, projectId: string) {
  const param = (name: keyof MapParams) => route.query.get(name) ?? undefined;
  const current: MapParams = { node: param('node'), focus: param('focus'), to: param('to'), hide: param('hide'), materials: param('materials'), removed: param('removed') };
  return {
    ...current,
    hidden: parseHidden(current.hide),
    /** Changes the map; replace keeps the moves of the scale out of the history. */
    update(patch: MapParams, options?: { replace?: boolean }) {
      navigate('/saga', { projectId, ...current, ...patch }, options);
    },
  };
}

export type MapRoute = ReturnType<typeof mapRoute>;
