import { iterateLayout, layoutChunk, layoutGraph, layoutPositions, type LayoutRequest, type LayoutResult } from './map-layout-core';

/**
 * The layout of the saga map off the page (specs/2026-11-04-saga-map.md, R3.6): ForceAtlas2 in chunks, the
 * positions after each chunk; a newer request drops the one running. A module of the build, not a `blob:`
 * worker, which the CSP of production refuses.
 */
const scope = self as unknown as { onmessage: ((event: MessageEvent<LayoutRequest>) => void) | null; postMessage: (result: LayoutResult) => void };
let current = 0;

scope.onmessage = ({ data: request }) => {
  current = request.id;
  const graph = layoutGraph(request);
  let done = 0;
  const step = () => {
    if (current !== request.id) return;
    const iterations = Math.min(layoutChunk, request.iterations - done);
    iterateLayout(graph, iterations);
    done += iterations;
    scope.postMessage({ id: request.id, positions: layoutPositions(graph), done: done >= request.iterations });
    if (done < request.iterations) setTimeout(step, 0);
  };
  step();
};
