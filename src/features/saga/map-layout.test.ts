import { describe, expect, it } from 'vitest';
import { iterateLayout, layoutGraph, layoutPositions, seedLayout, type Positions } from './map-layout-core';

const nodes = ['мирон', 'молли', 'ринна', 'карагора'].map((key) => ({ key, size: 5 }));
const links = [{ source: 'мирон', target: 'молли', weight: 4 }, { source: 'молли', target: 'ринна', weight: 1 }];

describe('the layout of the saga map (specs/2026-11-04-saga-map.md R3.6)', () => {
  it('pins the hero at the origin, keeps laid out nodes and starts a new one beside its neighbor', () => {
    const previous: Positions = new Map([['молли', { x: 30, y: -10 }]]);
    const seeded = seedLayout(nodes, links, previous, 'мирон');
    const at = Object.fromEntries(seeded.map((node) => [node.key, node]));
    expect(at['мирон']).toMatchObject({ x: 0, y: 0, fixed: true });
    expect(at['молли']).toMatchObject({ x: 30, y: -10, fixed: false });
    expect(Math.hypot(at['ринна']!.x - 30, at['ринна']!.y + 10)).toBeCloseTo(4);
    expect(Math.hypot(at['карагора']!.x, at['карагора']!.y)).toBeGreaterThan(10);
  });

  it('moves the others and leaves the hero at the center', () => {
    const graph = layoutGraph({ nodes: seedLayout(nodes, links, new Map(), 'мирон'), links });
    const before = new Map(layoutPositions(graph).map(([key, x, y]) => [key, { x, y }]));
    iterateLayout(graph, 40);
    const after = new Map(layoutPositions(graph).map(([key, x, y]) => [key, { x, y }]));
    expect(after.get('мирон')).toEqual({ x: 0, y: 0 });
    expect(after.get('молли')).not.toEqual(before.get('молли'));
    for (const { x, y } of after.values()) expect(Number.isFinite(x) && Number.isFinite(y)).toBe(true);
  });
});
