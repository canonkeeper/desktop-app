import type { LayoutRequest, LayoutResult } from './map-layout-core';

export type { LayoutLink, LayoutNode, Positions } from './map-layout-core';

/** The layout worker of the map: one request at a time, positions as they come. */
export function createMapLayout(onPositions: (result: LayoutResult) => void) {
  const worker = new Worker(new URL('./map-layout.worker.ts', import.meta.url), { type: 'module' });
  let id = 0;
  worker.onmessage = (event: MessageEvent<LayoutResult>) => {
    if (event.data.id === id) onPositions(event.data);
  };
  return {
    run(request: Omit<LayoutRequest, 'id'>) {
      id += 1;
      worker.postMessage({ ...request, id });
    },
    dispose() {
      worker.terminate();
    },
  };
}
