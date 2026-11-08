import Graph from 'graphology';
import forceAtlas2, { type ForceAtlas2Settings } from 'graphology-layout-forceatlas2';

/** A node to lay out: a pinned one stays where it is (the hero at the origin, R3.1). */
export interface LayoutNode {
  key: string;
  size: number;
  x: number;
  y: number;
  fixed: boolean;
}

export interface LayoutLink {
  source: string;
  target: string;
  weight: number;
}

export interface LayoutRequest {
  id: number;
  nodes: LayoutNode[];
  links: LayoutLink[];
  iterations: number;
}

export type Positions = Map<string, { x: number; y: number }>;

export interface LayoutResult {
  id: number;
  positions: Array<[string, number, number]>;
  done: boolean;
}

/** How many iterations the worker runs before it sends positions and looks for a newer request. */
export const layoutChunk = 20;

const goldenAngle = Math.PI * (3 - Math.sqrt(5));

/**
 * Places the nodes of a new view (R3.6): a node already laid out keeps its place, the pinned node stands at
 * the origin, a new node starts beside its strongest placed neighbor, the rest on a golden-angle spiral by
 * rank around the origin.
 */
export function seedLayout(nodes: ReadonlyArray<{ key: string; size: number }>, links: readonly LayoutLink[], previous: Positions, pinned: string | null): LayoutNode[] {
  const placed = new Map(previous);
  const strongest = new Map<string, Array<{ other: string; weight: number }>>();
  for (const link of links) {
    for (const [end, other] of [[link.source, link.target], [link.target, link.source]] as const) {
      strongest.set(end, [...(strongest.get(end) ?? []), { other, weight: link.weight }]);
    }
  }
  return nodes.map((node, rank) => {
    if (node.key === pinned) return { ...node, x: 0, y: 0, fixed: true };
    let at = placed.get(node.key);
    if (!at) {
      const neighbor = (strongest.get(node.key) ?? []).sort((left, right) => right.weight - left.weight).find((item) => placed.has(item.other));
      const angle = rank * goldenAngle;
      if (neighbor) {
        const base = placed.get(neighbor.other)!;
        at = { x: base.x + Math.cos(angle) * 4, y: base.y + Math.sin(angle) * 4 };
      } else {
        const radius = 12 * Math.sqrt(rank + 1);
        at = { x: Math.cos(angle) * radius, y: Math.sin(angle) * radius };
      }
      placed.set(node.key, at);
    }
    return { ...node, x: at.x, y: at.y, fixed: false };
  });
}

export function layoutSettings(order: number): ForceAtlas2Settings {
  return {
    barnesHutOptimize: order > 300,
    gravity: 1,
    scalingRatio: 12,
    strongGravityMode: true,
    slowDown: 1 + Math.log(Math.max(order, 1)),
    edgeWeightInfluence: 1,
  };
}

/** The graph ForceAtlas2 iterates: positions, sizes, pins and link weights. */
export function layoutGraph(request: Pick<LayoutRequest, 'nodes' | 'links'>) {
  const graph = new Graph({ type: 'undirected' });
  for (const node of request.nodes) graph.addNode(node.key, { x: node.x, y: node.y, size: node.size, fixed: node.fixed });
  for (const link of request.links) {
    if (graph.hasNode(link.source) && graph.hasNode(link.target) && !graph.hasEdge(link.source, link.target)) {
      graph.addEdge(link.source, link.target, { weight: link.weight });
    }
  }
  return graph;
}

/** Runs some iterations of ForceAtlas2 over the graph in place. */
export function iterateLayout(graph: Graph, iterations: number) {
  forceAtlas2.assign(graph, { iterations, settings: layoutSettings(graph.order), getEdgeWeight: 'weight' });
}

export function layoutPositions(graph: Graph): Array<[string, number, number]> {
  return graph.mapNodes((key, attributes) => [key, attributes.x as number, attributes.y as number]);
}
