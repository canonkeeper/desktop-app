import { describe, expect, it } from 'vitest';
import { iterateLayout, layoutGraph, layoutPositions, placeLabels, seedLayout, type Positions } from './map-layout-core';

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

// specs/2026-11-11-ux8-map.md MP1.3: labels do not run over each other; as many show as fit, not a fixed eight.
describe('the labels of the saga map', () => {
  const label = (key: string, x: number, y: number, size: number, tier = 3) => ({ key, x, y, size, width: 80, height: 15, tier });

  it('keeps the label of the bigger of two nodes whose labels meet', () => {
    const shown = placeLabels([label('тарен', 100, 100, 6), label('северная пристань', 96, 106, 3), label('гильдия', 400, 300, 18)]);
    expect([...shown].sort()).toEqual(['гильдия', 'тарен']);
  });

  it('shows every label that fits, however many there are', () => {
    const grid = Array.from({ length: 40 }, (_, index) => label(`узел ${index}`, (index % 8) * 120, Math.floor(index / 8) * 40, 4));
    expect(placeLabels(grid).size).toBe(40);
  });

  it('does not lay a label over the circle of a node as big as its own, and the biggest names stay', () => {
    expect([...placeLabels([label('мара', 100, 100, 6), label('сава', 140, 121, 14)])]).toEqual(['сава']);
    expect([...placeLabels([label('мара', 100, 100, 14), label('сава', 140, 121, 6)])].sort()).toEqual(['мара', 'сава']);
  });

  it('gives the open or hovered node and then the hero their labels before a bigger node', () => {
    const shown = placeLabels([label('большой', 100, 100, 18), label('открытый', 104, 102, 3, 0), label('герой', 300, 100, 5, 1), label('сосед', 296, 104, 12)]);
    expect([...shown].sort()).toEqual(['герой', 'открытый']);
  });
});
