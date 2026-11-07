// SPDX-License-Identifier: AGPL-3.0-only
// ForceAtlas2 in a module worker, as the app's map runs its layout off the main thread.
import Graph from "graphology";
import forceAtlas2 from "graphology-layout-forceatlas2";

self.onmessage = (event) => {
  const graph = Graph.from(event.data.graph);
  const started = performance.now();
  const settings = forceAtlas2.inferSettings(graph);
  forceAtlas2.assign(graph, { iterations: event.data.iterations, settings });
  self.postMessage({ graph: graph.export(), ms: performance.now() - started });
};
