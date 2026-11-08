import { describe, expect, it } from 'vitest';
import type { SagaMap, SagaMapCategory } from '../../entities/saga/api';
import { countTo, formatHidden, linkAt, parseHidden, projectMap, resolveNodeKey, searchNodes, type MapFilter, type MapView } from './map-model';

const chapter = (index: number) => ({
  key: String(index + 1), chapterId: `c${index}`, bookId: 'b1', bookOrder: 1, bookTitle: 'Том 1', title: `Глава ${index + 1}`, number: index + 1,
});

function node(key: string, category: SagaMapCategory, chapters: number[], aliases: string[] = []) {
  return { key, name: key[0]!.toLocaleUpperCase('ru') + key.slice(1), category, type: category === 'character' ? 'персонаж' : 'город', aliases, chapters };
}

/** Мирон in every chapter; Молли with him in events of chapters 3 and 7; Ринна beside him in 1, 2, 4; a city from chapter 6. */
const map: SagaMap = {
  revision: 'r',
  coverage: { chapters: 10, withCards: 10 },
  chapters: Array.from({ length: 10 }, (_, index) => chapter(index)),
  hero: 'мирон',
  nodesTotal: 4,
  nodes: [
    node('мирон', 'character', [0, 1, 2, 3, 4, 5, 6, 7, 8, 9], ['Ворон']),
    node('молли', 'character', [3, 7, 8], ['Молли Хоунс', 'Серый кардинал']),
    node('ринна', 'character', [1, 2, 4]),
    node('карагора', 'place', [6, 7, 8]),
  ],
  links: [
    { source: 0, target: 1, events: [3, 7], mentions: [8] },
    { source: 0, target: 2, events: [], mentions: [1, 2, 4] },
    { source: 0, target: 3, events: [], mentions: [6, 7, 8] },
  ],
};

const view = (patch: Partial<MapView> = {}): MapView => ({ to: 9, hidden: new Set(), limit: 60, focus: null, selected: null, ...patch });
const keys = (graph: ReturnType<typeof projectMap>) => graph.nodes.map((item) => item.key);

describe('the saga map projected on a chapter (specs/2026-11-04-saga-map.md UC3, UC4)', () => {
  it('counts chapters up to a chapter', () => {
    expect(countTo([1, 3, 7, 11], 7)).toBe(3);
    expect(countTo([1, 3], 0)).toBe(0);
    expect(countTo([], 5)).toBe(0);
  });

  it('shows only the nodes that have appeared by the chapter, sized by their chapters up to it', () => {
    const early = projectMap(map, view({ to: 4 }));
    expect(keys(early)).toEqual(['мирон', 'молли', 'ринна']);
    expect(early.nodes.map((item) => item.count)).toEqual([5, 1, 3]);
    expect(early.total).toBe(3);
    expect(early.counts).toEqual({ character: 3, place: 0, faction: 0, item: 0, material: 0 });
    expect(early.nodes[0]!.hero).toBe(true);
  });

  // R11.1: no link without a reason.
  it('draws a link only with a reason: two shared events or a change naming both', () => {
    expect(linkAt(map.links[0]!, 3)).toBeNull();
    expect(linkAt(map.links[0]!, 7)).toEqual({ together: 2, nearby: 0, weak: false });
    expect(linkAt(map.links[1]!, 9)).toBeNull();
    expect(linkAt({ ...map.links[1]!, changes: [4] }, 3)).toBeNull();
    expect(linkAt({ ...map.links[1]!, changes: [4] }, 4)).toEqual({ together: 0, nearby: 3, weak: true });
    expect(projectMap(map, view({ to: 4 })).links).toEqual([]);
    expect(projectMap(map, view()).links.map((link) => [link.source, link.target, link.weak])).toEqual([['мирон', 'молли', false]]);
  });

  it('keeps the most mentioned up to the limit, with the hero and the open node', () => {
    expect(keys(projectMap(map, view({ limit: 2 })))).toEqual(['мирон', 'молли']);
    expect(keys(projectMap(map, view({ limit: 1, selected: 'ринна' })))).toEqual(['мирон', 'ринна']);
    const hidden = projectMap(map, view({ limit: 1, hidden: new Set(['character']) }));
    expect(keys(hidden)).toEqual(['карагора']);
    expect(hidden.total).toBe(1);
    // A hidden node the author opens is shown; one that has not appeared by the chapter is not.
    expect(keys(projectMap(map, view({ hidden: new Set(['place']), selected: 'карагора' })))).toContain('карагора');
    expect(keys(projectMap(map, view({ to: 2, selected: 'карагора' })))).not.toContain('карагора');
  });

  it('shows the surroundings of a node: it and its neighbors by the chapter', () => {
    expect(keys(projectMap(map, view({ focus: 'молли' })))).toEqual(['мирон', 'молли']);
    expect(keys(projectMap(map, view({ focus: 'ринна', to: 2 })))).toEqual(['ринна']);
    const city = projectMap(map, view({ focus: 'карагора', hidden: new Set(['character']) }));
    expect(keys(city)).toEqual(['карагора']);
  });

  it('finds nodes by any name, ignoring case and «ё», names starting with the query first', () => {
    expect(searchNodes(map, 'кардинал').map((item) => item.key)).toEqual(['молли']);
    expect(searchNodes(map, 'ВОР').map((item) => item.key)).toEqual(['мирон']);
    expect(searchNodes(map, 'р').map((item) => item.key)).toEqual(['ринна', 'мирон', 'молли', 'карагора']);
    expect(searchNodes(map, '  ')).toEqual([]);
  });

  it('takes a node by its key or by a name the voice agent says (R6.4)', () => {
    expect(resolveNodeKey(map, 'молли')).toBe('молли');
    expect(resolveNodeKey(map, 'Молли Хоунс')).toBe('молли');
    expect(resolveNodeKey(map, 'ворон')).toBe('мирон');
    expect(resolveNodeKey(map, 'Прохор')).toBe('Прохор');
    expect(resolveNodeKey(map, undefined)).toBeNull();
  });

  it('keeps the hidden categories in the URL', () => {
    expect([...parseHidden('item,place,nonsense')]).toEqual(['item', 'place']);
    expect(formatHidden(new Set(['item', 'place']))).toBe('place,item');
    expect(formatHidden(new Set())).toBeUndefined();
  });
});

// specs/2026-11-04-saga-map.md R9.5: a record of the author stands at any chapter, outside the limit, and has its own filter.
describe('the materials of the author on the map', () => {
  const withMaterial = (): SagaMap => {
    const base = map;
    return {
      ...base,
      nodes: [...base.nodes, { key: 'миг назад', name: 'Миг назад', category: 'place', type: 'из ваших материалов · ещё не в книге', aliases: [], chapters: [], material: true }],
      links: [...base.links, { source: 0, target: base.nodes.length, events: [], mentions: [], material: true }],
    };
  };
  const view: MapView = { to: 0, hidden: new Set<MapFilter>(), limit: 1, focus: null, selected: null };

  it('shows a record at any chapter and outside the limit, with a thin link', () => {
    const graph = projectMap(withMaterial(), view);
    const found = graph.nodes.find((item) => item.key === 'миг назад');
    expect(found).toMatchObject({ material: true, count: 0, category: 'place' });
    expect(graph.counts.material).toBe(1);
    expect(graph.links.find((link) => link.target === 'миг назад')).toMatchObject({ weak: true, together: 0 });
  });

  it('hides the records by their filter or by their category, and keeps the filter in the URL', () => {
    const drawn = withMaterial();
    expect(projectMap(drawn, { ...view, hidden: new Set<MapFilter>(['material']) }).nodes.some((item) => item.material)).toBe(false);
    expect(projectMap(drawn, { ...view, hidden: new Set<MapFilter>(['place']) }).nodes.some((item) => item.material)).toBe(false);
    expect(parseHidden('place,material,nothing')).toEqual(new Set(['place', 'material']));
    expect(formatHidden(new Set<MapFilter>(['material', 'item']))).toBe('item,material');
  });

  it('opens the card of a record as the open node', () => {
    const graph = projectMap(withMaterial(), { ...view, hidden: new Set<MapFilter>(['material']), selected: 'миг назад' });
    expect(graph.nodes.some((item) => item.key === 'миг назад')).toBe(true);
  });
});
