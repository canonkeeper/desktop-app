import type { SagaChapterRef, SagaMap, SagaMapCategory, SagaMapNode } from '../../entities/saga/api';

/**
 * The saga map in the browser (specs/2026-11-04-saga-map.md, UC3, UC4): the graph the server built,
 * projected on the chapters up to N. Nothing here asks the server: weights up to N are counted from the
 * chapters every node and link carries.
 */

export const mapCategories: ReadonlyArray<{ category: SagaMapCategory; label: string }> = [
  { category: 'character', label: 'Персонажи' },
  { category: 'place', label: 'Места' },
  { category: 'faction', label: 'Фракции' },
  { category: 'item', label: 'Предметы' },
];

/** What a map hides: a category, or the records of the author the book does not know (R9.5). */
export type MapFilter = SagaMapCategory | 'material';

/** How many shared events make a link, as on the server (R11.1). */
export const minSharedEvents = 2;
/** How many neighbors the surroundings of a node show (R3.5). */
export const focusNeighbors = 40;

/** Counts the chapter indexes of an ascending list up to a chapter index (inclusive). */
export function countTo(chapters: readonly number[], to: number) {
  let low = 0;
  let high = chapters.length;
  while (low < high) {
    const middle = (low + high) >> 1;
    if (chapters[middle]! <= to) low = middle + 1;
    else high = middle;
  }
  return low;
}

export interface MapView {
  /** The last chapter index the map shows. */
  to: number;
  hidden: ReadonlySet<MapFilter>;
  /** How many of the most mentioned nodes the map shows. */
  limit: number;
  /** The node whose surroundings the map shows, or null for the whole map. */
  focus: string | null;
  /** The node whose card is open: always on the map once it has appeared. */
  selected: string | null;
}

export interface ViewNode {
  key: string;
  name: string;
  category: SagaMapCategory;
  /** Chapters up to N that mention it. */
  count: number;
  hero: boolean;
  /** A record of the author the book does not know: a dashed outline, no chapters (R9.5). */
  material: boolean;
}

export interface ViewLink {
  key: string;
  source: string;
  target: string;
  /** Shared events up to N. */
  together: number;
  /** Chapters beside each other without a shared event up to N. */
  nearby: number;
  weak: boolean;
}

export interface ViewGraph {
  nodes: ViewNode[];
  links: ViewLink[];
  /** Nodes up to N of the shown categories: «60 из 412». */
  total: number;
  /** Nodes up to N of each category, for the filters. */
  counts: Record<MapFilter, number>;
}

/**
 * The strength of a link up to N, or null when it is no link yet (R11.1: two shared events or a change naming both). One shared
 * event or chapters beside each other are no link: the card names them apart. A link by a change alone is drawn weak.
 */
export function linkAt(link: SagaMap['links'][number], to: number) {
  if (link.material) return { together: 0, nearby: 0, weak: true };
  const together = countTo(link.events, to);
  const nearby = countTo(link.mentions, to);
  if (together < minSharedEvents && countTo(link.changes ?? [], to) === 0) return null;
  return { together, nearby, weak: together < minSharedEvents };
}

/** The neighbors of a node up to N, strongest first: shared events, then chapters beside each other. */
export function neighborsAt(map: SagaMap, node: number, to: number) {
  const neighbors: Array<{ node: number; together: number; nearby: number }> = [];
  for (const link of map.links) {
    if (link.source !== node && link.target !== node) continue;
    const strength = linkAt(link, to);
    if (strength) neighbors.push({ node: link.source + link.target - node, together: strength.together, nearby: strength.nearby });
  }
  return neighbors.sort((left, right) => right.together - left.together || right.nearby - left.nearby || left.node - right.node);
}

/**
 * Projects the map on the chapters up to N (UC3 «граф на главу N»): the nodes that have appeared by then, of
 * the shown categories, the most mentioned first up to the limit, with the hero and the open node; or the
 * surroundings of a node — it and its strongest neighbors. Links join shown nodes once they are links by N.
 */
export function projectMap(map: SagaMap, view: MapView): ViewGraph {
  const counts = Object.fromEntries([...mapCategories.map(({ category }) => [category, 0]), ['material', 0]]) as Record<MapFilter, number>;
  // A record of the author is no event of a chapter: it stands at any chapter.
  const present = map.nodes.map((node) => (node.material ? 0 : countTo(node.chapters, view.to)));
  const appeared = (index: number) => present[index]! > 0 || Boolean(map.nodes[index]!.material);
  const hiddenNode = (node: SagaMapNode) => view.hidden.has(node.category) || (Boolean(node.material) && view.hidden.has('material'));
  const shown = new Set<number>();
  let total = 0;
  map.nodes.forEach((node, index) => {
    if (!appeared(index)) return;
    counts[node.material ? 'material' : node.category] += 1;
    if (!hiddenNode(node)) total += 1;
  });
  const indexOf = new Map(map.nodes.map((node, index) => [node.key, index]));
  const focus = view.focus === null ? undefined : indexOf.get(view.focus);
  if (focus !== undefined && appeared(focus)) {
    shown.add(focus);
    for (const neighbor of neighborsAt(map, focus, view.to)) {
      if (shown.size > focusNeighbors) break;
      if (!hiddenNode(map.nodes[neighbor.node]!)) shown.add(neighbor.node);
    }
  } else {
    const ranked = map.nodes.map((_, index) => index)
      .filter((index) => present[index]! > 0 && !hiddenNode(map.nodes[index]!))
      .sort((left, right) => present[right]! - present[left]! || left - right);
    for (const index of ranked.slice(0, view.limit)) shown.add(index);
    const hero = map.hero === null ? undefined : indexOf.get(map.hero);
    if (hero !== undefined && present[hero]! > 0 && !hiddenNode(map.nodes[hero]!)) shown.add(hero);
    map.nodes.forEach((node, index) => {
      if (node.material && !hiddenNode(node)) shown.add(index);
    });
  }
  const selected = view.selected === null ? undefined : indexOf.get(view.selected);
  if (selected !== undefined && appeared(selected)) shown.add(selected);

  const nodes = [...shown].sort((left, right) => left - right).map((index): ViewNode => {
    const node = map.nodes[index]!;
    return { key: node.key, name: node.name, category: node.category, count: present[index]!, hero: node.key === map.hero, material: Boolean(node.material) };
  });
  const links: ViewLink[] = [];
  for (const link of map.links) {
    if (!shown.has(link.source) || !shown.has(link.target)) continue;
    const strength = linkAt(link, view.to);
    if (!strength) continue;
    const source = map.nodes[link.source]!.key;
    const target = map.nodes[link.target]!.key;
    links.push({ key: `${source}\u0000${target}`, source, target, ...strength });
  }
  return { nodes, links, total, counts };
}

const normalized = (text: string) => text.toLocaleLowerCase('ru').replaceAll('ё', 'е').trim();

/**
 * The nodes whose name or another name contains the query, ignoring case and «ё» (R3.3): names starting with
 * it first, then the most mentioned.
 */
export function searchNodes(map: SagaMap, query: string, limit = 8): SagaMapNode[] {
  const wanted = normalized(query);
  if (!wanted) return [];
  const found: Array<{ node: SagaMapNode; starts: boolean }> = [];
  for (const node of map.nodes) {
    const names = [node.name, ...node.aliases].map(normalized);
    if (!names.some((name) => name.includes(wanted))) continue;
    found.push({ node, starts: names.some((name) => name.startsWith(wanted)) });
  }
  return found
    .sort((left, right) => Number(right.starts) - Number(left.starts) || right.node.chapters.length - left.node.chapters.length)
    .slice(0, limit)
    .map((item) => item.node);
}

/**
 * The key of the node a URL names: a key, or a name or another name as the voice agent says it (R6.4); a name that
 * is no node stays as it is — the card asks the glossary of the saga for it.
 */
export function resolveNodeKey(map: SagaMap, value: string | undefined) {
  if (value === undefined) return null;
  if (map.nodes.some((node) => node.key === value)) return value;
  const wanted = normalized(value);
  return map.nodes.find((node) => [node.key, node.name, ...node.aliases].some((name) => normalized(name) === wanted))?.key ?? value;
}

/** «гл. 312 · Том 7»: the chapter the scale stands at. */
export function chapterLabel(chapter: SagaChapterRef) {
  return `гл. ${chapter.number ?? chapter.key} · Том ${chapter.bookOrder}`;
}

/** The hidden categories of the URL: «place,item»; unknown words are left out. */
export function parseHidden(value: string | undefined): Set<MapFilter> {
  const known = new Set<string>([...mapCategories.map(({ category }) => category), 'material']);
  return new Set((value ?? '').split(',').filter((word): word is MapFilter => known.has(word)));
}

export function formatHidden(hidden: ReadonlySet<MapFilter>) {
  const words = [...mapCategories.map(({ category }): MapFilter => category), 'material' as const].filter((category) => hidden.has(category));
  return words.length ? words.join(',') : undefined;
}
