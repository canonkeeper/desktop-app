import type {
  GetSagaContentsOptions,
  GetSagaMapNodeOptions,
  SearchSagaMapOptions,
  GetSagaMapOptions,
  ListSagaGlossaryOptions,
  ListSagaMapHiddenOptions,
  OperationId,
  OperationRequest,
  OperationResponse,
  Schema,
  SourceView,
} from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';

type ChapterRef = Schema<'SagaChapterRef'>;
type Mention = { name: string; aliases?: string[]; type?: string; note?: string; p: number };
/** Части карточки, которые читают карта саги, словарь и оглавление. */
type Card = {
  summary: string;
  characters: Mention[];
  entities: Mention[];
  changes: Array<{ subject: string; change: string; p: number }>;
  events?: Array<{ event: string; who: string[]; p: number; key?: boolean }>;
};
type Entry = { key: string; name: string; type: string; aliases: string[]; note?: string; chapters: number[] };
type Link = { chapter: number; subject: string; change: string; p: number; entries: Entry[] };

/** Карточки глав синтетической саги «Хроники Белого порта» по id главы. */
const cards: Record<string, Card> = {
  'chapter-01-01': {
    summary: 'Мара прибывает в Белый порт на рассвете и находит работу на северной пристани у смотрителя Савы. Сава учит её вести журнал и дарит старый кинжал.',
    characters: [{ name: 'Мара', aliases: ['Северянка'], note: 'молодая смотрительница пристани', p: 1 }, { name: 'Сава', note: 'старый смотритель', p: 3 }],
    entities: [
      { name: 'Белый порт', type: 'город', note: 'портовый город саги', p: 1 },
      { name: 'Северная пристань', type: 'место', note: 'пристань у складов', p: 2 },
      { name: 'Кинжал Мары', type: 'оружие', note: 'старый кинжал Савы', p: 6 },
    ],
    changes: [
      { subject: 'Мара', change: 'Устраивается работать на северной пристани.', p: 2 },
      { subject: 'Мара', change: 'Получает от Савы старый кинжал.', p: 6 },
    ],
    events: [
      { event: 'Мара сходит на берег Белого порта', who: ['Мара'], p: 1 },
      { event: 'Сава дарит Маре старый кинжал', who: ['Сава', 'Северянка'], p: 6, key: true },
    ],
  },
  'chapter-12': {
    summary: 'Ближе к полуночи над портом поднимается дым. Мара первой замечает пожар на северной пристани и поднимает тревогу; Сава спасает склад гильдии.',
    characters: [{ name: 'Мара', p: 2 }, { name: 'Сава', p: 4 }],
    entities: [
      { name: 'Белый порт', type: 'город', p: 1 },
      { name: 'Северная пристань', type: 'место', p: 2 },
      { name: 'Гильдия смотрителей', type: 'организация', note: 'хранители складов порта', p: 4 },
    ],
    changes: [
      { subject: 'Мара', change: 'Первой замечает пожар и поднимает тревогу.', p: 2 },
      { subject: 'Сава', change: 'Получает ожоги, спасая склад гильдии.', p: 4 },
    ],
    events: [
      { event: 'Мара замечает пожар на северной пристани', who: ['Мара'], p: 2, key: true },
      { event: 'Сава спасает склад гильдии', who: ['Сава'], p: 4 },
    ],
  },
  'chapter-16': {
    summary: 'Через неделю после пожара Мара возвращается к журналу Савы, вступает в Гильдию смотрителей и отдаёт кинжал в починку.',
    characters: [{ name: 'Мара', p: 1 }, { name: 'Сава', p: 2 }],
    entities: [{ name: 'Гильдия смотрителей', type: 'организация', p: 3 }, { name: 'Кинжал Мары', type: 'оружие', p: 5 }],
    changes: [
      { subject: 'Мара', change: 'Вступает в Гильдию смотрителей.', p: 3 },
      { subject: 'Кинжал Мары', change: 'Мара отдаёт кинжал в починку.', p: 5 },
    ],
  },
  'chapter-03-01': {
    summary: 'Северный суд разбирает дело о пожаре. Сава свидетельствует против гильдии, а Мара получает звание младшего смотрителя.',
    characters: [{ name: 'Мара', p: 2 }, { name: 'Сава', p: 1 }],
    entities: [{ name: 'Младший смотритель', type: 'титул', note: 'первое звание гильдии', p: 7 }, { name: 'Гильдия смотрителей', type: 'организация', p: 1 }],
    changes: [
      { subject: 'Мара', change: 'Получает звание младшего смотрителя.', p: 7 },
      { subject: 'Сава', change: 'Свидетельствует против гильдии.', p: 1 },
    ],
    events: [
      { event: 'Сава свидетельствует против гильдии', who: ['Сава'], p: 1 },
      { event: 'Мара получает звание младшего смотрителя', who: ['Мара'], p: 7 },
    ],
  },
};

/** Типы карточек по категориям карты саги, как в домене (specs/2026-11-04-saga-map.md, Data mapCategories). */
const mapCategories: Record<string, Schema<'SagaMapCategory'>> = Object.fromEntries(Object.entries({
  character: 'персонаж',
  place: 'место локация город страна королевство империя регион континент мир планета деревня село район квартал улица здание сооружение замок крепость храм башня этаж подземелье остров лес гора река море озеро пустыня',
  faction: 'фракция организация клан гильдия орден братство семья род народ группа отряд команда армия банда союз альянс',
  item: 'предмет артефакт оружие снаряжение броня доспех зелье амулет кольцо транспорт',
}).flatMap(([category, types]) => types.split(' ').map((type) => [type, category as Schema<'SagaMapCategory'>])));
const maxMapNodes = 600;
const weakLinkChapters = 3;
const weakLinksPerNode = 8;
const minSharedEvents = 2;
const mapFewCards = 3;
const maxLinkShared = 3;
const maxNodeBeside = 8;

const normalize = (text: string) => text.toLocaleLowerCase('ru').replaceAll('ё', 'е').trim();
const words = (text: string) => normalize(text).split(/[^a-zа-я0-9]+/u).filter(Boolean);
/** Грубая основа для мока: у слов длиннее трёх букв отбрасывается окончание. */
const stem = (word: string) => (word.length > 3 ? word.slice(0, -1) : word);
const names = (entry: Entry) => [entry.name, ...entry.aliases];
const mentions = (text: string, name: string) => {
  const textWords = words(text).map(stem);
  return words(name).every((word) => textWords.some((candidate) => candidate.startsWith(stem(word))));
};
const categoryOf = (type: string) => {
  const typeWords = normalize(type).split(/\s+/u).filter(Boolean);
  return mapCategories[typeWords.join(' ')] ?? mapCategories[typeWords.at(-1) ?? ''];
};
const countTo = (chapters: readonly number[], to: number) => chapters.filter((chapter) => chapter <= to).length;
type MapLink = { source: number; target: number; events: number[]; mentions: number[]; changes: number[] };
const honestAt = (link: MapLink, to: number) => countTo(link.events, to) >= minSharedEvents || countTo(link.changes, to) > 0;
const materialCategory: Partial<Record<Schema<'AuthorMaterial'>['kind'], Schema<'SagaMapCategory'>>> = { place: 'place', character: 'character', item: 'item', faction: 'faction' };

export function createSagaOperations(context: MockRepositoryContext, materialsOf: (projectId: string) => readonly Schema<'AuthorMaterial'>[] = () => []) {
  function version(operationId: OperationId, projectId: string, sourceView: SourceView | undefined) {
    context.applyOperation(operationId);
    if (sourceView !== 'draft' && sourceView !== 'published') {
      throw new MockApiError(400, 'Source view must be draft or published.', operationId, 'validation_failed');
    }
    const project = context.state.projects.find((item) => item.id === projectId);
    if (!project) throw new MockApiError(404, 'Project not found.', operationId, 'not_found');
    if (sourceView === 'draft' && !['editor', 'admin', 'owner'].includes(context.actorRole(projectId))) {
      throw new MockApiError(403, 'Draft needs editor access.', operationId, 'forbidden');
    }
    const indexed = new Set(context.state.indexedChapterIds[sourceView]);
    const carded = new Set(context.state.cardedChapterIds[sourceView]);
    const books = context.state.books.filter((book) => book.projectId === projectId).sort((left, right) => left.order - right.order);
    const chapters = books.flatMap((book) => context.state.chapters
      .filter((chapter) => chapter.bookId === book.id && indexed.has(chapter.id))
      .sort((left, right) => left.order - right.order)
      .map((chapter): ChapterRef => ({
        key: `b${book.order}c${chapter.order}`, chapterId: chapter.id, bookId: book.id, bookOrder: book.order, bookTitle: book.title, title: chapter.title,
      })));
    const chapterCards = chapters.map((chapter) => (carded.has(chapter.chapterId) ? cards[chapter.chapterId] : undefined));
    const entries: Entry[] = [];
    const byName = new Map<string, Entry>();
    chapterCards.forEach((card, index) => {
      if (!card) return;
      for (const [mention, kind] of [...card.characters.map((item) => [item, 'персонаж'] as const), ...card.entities.map((item) => [item, item.type ?? ''] as const)]) {
        const key = normalize(mention.name);
        let entry = byName.get(key) ?? (mention.aliases ?? []).map((alias) => byName.get(normalize(alias))).find(Boolean);
        if (!entry) {
          entry = { key, name: mention.name, type: kind, aliases: [], chapters: [] };
          entries.push(entry);
        }
        for (const name of [mention.name, ...(mention.aliases ?? [])]) {
          if (!byName.has(normalize(name))) byName.set(normalize(name), entry);
          if (normalize(name) !== entry.key && !entry.aliases.includes(name)) entry.aliases.push(name);
        }
        if (mention.note && !entry.note) entry.note = mention.note;
        if (entry.chapters.at(-1) !== index) entry.chapters.push(index);
      }
    });
    const links: Link[] = chapterCards.flatMap((card, chapter) => (card?.changes ?? []).map((change) => ({
      chapter, ...change,
      entries: entries.filter((entry) => names(entry).some((name) => mentions(`${change.subject} ${change.change}`, name))),
    })));
    const coverage = { chapters: chapters.length, withCards: chapterCards.filter(Boolean).length };
    const bookList = books
      .map((book) => ({ book, refs: chapters.filter((chapter) => chapter.bookId === book.id) }))
      .filter(({ refs }) => refs.length > 0)
      .map(({ book, refs }) => ({ id: book.id, order: book.order, title: book.title, chapterCount: refs.length, keys: refs.map((ref) => ref.key) }));
    const chapterIndex = (operation: OperationId, key: string | undefined) => {
      if (key === undefined) return undefined;
      const index = chapters.findIndex((chapter) => chapter.key === key);
      if (index < 0) throw new MockApiError(400, 'Unknown chapter key.', operation, 'validation_failed');
      return index;
    };
    const lookup = (key: string) => byName.get(normalize(key));
    const change = (link: Link): Schema<'SagaChange'> => ({ chapter: chapters[link.chapter]!, subject: link.subject, change: link.change, paragraph: link.p });
    const bookRange = (operation: OperationId, bookId: string | undefined) => {
      const book = bookId === undefined ? bookList[0] : bookList.find((item) => item.id === bookId);
      if (bookId !== undefined && !book) throw new MockApiError(400, 'Unknown book.', operation, 'validation_failed');
      return book;
    };
    return { chapters, chapterCards, entries, links, coverage, bookList, chapterIndex, lookup, change, bookRange };
  }

  /** Граф саги по правилам домена: узлы четырёх категорий, связи событий и соседства (UC1). */
  function graphOf(saga: ReturnType<typeof version>) {
    const min = saga.coverage.withCards >= 20 ? 2 : 1;
    const nodes = saga.entries
      .filter((entry) => categoryOf(entry.type) && entry.chapters.length >= min)
      .sort((left, right) => right.chapters.length - left.chapters.length || (left.name < right.name ? -1 : left.name > right.name ? 1 : 0));
    const total = nodes.length;
    nodes.splice(maxMapNodes);
    const nodeOf = new Map(nodes.map((entry, index) => [entry, index]));
    const pairs = new Map<string, MapLink>();
    const pair = (source: number, target: number) => {
      const key = `${source}:${target}`;
      const found = pairs.get(key) ?? { source, target, events: [], mentions: [], changes: [] };
      pairs.set(key, found);
      return found;
    };
    saga.chapterCards.forEach((card, chapter) => {
      if (!card) return;
      const together = new Set<string>();
      for (const event of card.events ?? []) {
        const who = [...new Set(event.who.flatMap((name) => {
          const node = nodeOf.get(saga.lookup(name)!);
          return node === undefined ? [] : [node];
        }))].sort((left, right) => left - right);
        who.forEach((source, i) => who.slice(i + 1).forEach((target) => {
          together.add(`${source}:${target}`);
          pair(source, target).events.push(chapter);
        }));
      }
      const named = nodes.flatMap((entry, index) => (entry.chapters.includes(chapter) ? [index] : []));
      named.forEach((source, i) => named.slice(i + 1).forEach((target) => {
        if (!together.has(`${source}:${target}`)) pair(source, target).mentions.push(chapter);
      }));
    });
    const kept = new Set<MapLink>();
    const weak = new Map<number, MapLink[]>();
    for (const link of pairs.values()) {
      if (link.events.length) kept.add(link);
      else if (link.mentions.length >= weakLinkChapters) {
        for (const end of [link.source, link.target]) weak.set(end, [...(weak.get(end) ?? []), link]);
      }
    }
    for (const [node, links] of weak) {
      const other = (link: MapLink) => link.source + link.target - node;
      links.sort((left, right) => right.mentions.length - left.mentions.length || other(left) - other(right))
        .slice(0, weakLinksPerNode).forEach((link) => kept.add(link));
    }
    // A change naming two is a reason by itself (R11.1).
    for (const link of saga.links) {
      const subject = saga.lookup(link.subject);
      for (const entry of link.entries) {
        const [a, b] = [nodeOf.get(entry), subject && nodeOf.get(subject)];
        if (a === undefined || b === undefined || a === b) continue;
        const found = pair(Math.min(a, b), Math.max(a, b));
        if (!found.changes.includes(link.chapter)) found.changes.push(link.chapter);
        kept.add(found);
      }
    }
    // A node of one chapter stays off the map without a reason (R11.4); its links stay for the cards.
    const everyLink = [...kept].sort((left, right) => left.source - right.source || left.target - right.target);
    const reasoned = new Set(everyLink.filter((link) => honestAt(link, Infinity)).flatMap((link) => [link.source, link.target]));
    const stays = nodes.map((entry, index) => saga.coverage.withCards < mapFewCards || entry.chapters.length > 1 || reasoned.has(index));
    const moved = new Map<number, number>();
    const onMap = nodes.filter((_, index) => {
      if (stays[index]) moved.set(index, moved.size);
      return stays[index];
    });
    const links = everyLink.flatMap((link) => (moved.has(link.source) && moved.has(link.target) ? [{ ...link, source: moved.get(link.source)!, target: moved.get(link.target)! }] : []));
    const hero = onMap.findIndex((entry) => entry.type === 'персонаж');
    const neighborsOf = (entry: Entry) => everyLink.flatMap((link) => {
      const index = nodes.indexOf(entry);
      if (link.source !== index && link.target !== index) return [];
      return [{ link, other: nodes[link.source + link.target - index]! }];
    });
    return { nodes: onMap, links, total, hero, neighborsOf };
  }

  /** Что автор убрал с карты (R11.3): решения применяются при чтении, сами строки не меняются. */
  const hiddenByProject = new Map<string, Schema<'SagaMapHiddenItem'>[]>();
  let nextHiddenId = 1;
  // A new scenario of the mock starts with a clean saga, so with nothing off its map.
  let hiddenOwner = context.state;
  const hiddenOf = (projectId: string) => {
    if (hiddenOwner !== context.state) {
      hiddenOwner = context.state;
      hiddenByProject.clear();
    }
    return hiddenByProject.get(projectId) ?? [];
  };
  const hidesNode = (projectId: string, key: string) => hiddenOf(projectId).some((item) => item.otherKey === null && item.nodeKey === key);
  const hidesLink = (projectId: string, a: string, b: string) => hiddenOf(projectId).some((item) => (item.nodeKey === a && item.otherKey === b) || (item.nodeKey === b && item.otherKey === a));

  async function listSagaMapHidden(projectId: string, options: ListSagaMapHiddenOptions): Promise<OperationResponse<'listSagaMapHidden', '200'>> {
    version('listSagaMapHidden', projectId, options.sourceView);
    return { items: hiddenOf(projectId) };
  }

  async function hideSagaMapItem(projectId: string, sourceView: SourceView, body: OperationRequest<'hideSagaMapItem'>): Promise<OperationResponse<'hideSagaMapItem', '201'>> {
    const saga = version('hideSagaMapItem', projectId, sourceView);
    if (!['editor', 'admin', 'owner'].includes(context.actorRole(projectId))) throw new MockApiError(403, 'Editor access required.', 'hideSagaMapItem', 'forbidden');
    const find = (key: string) => {
      const entry = saga.lookup(key);
      const own = materialsOf(projectId).find((record) => normalize(record.name) === normalize(key));
      if (!entry && !own) throw new MockApiError(404, 'Entry not found.', 'hideSagaMapItem', 'not_found');
      return entry ? { key: entry.key, name: entry.name } : { key: normalize(own!.name), name: own!.name };
    };
    const node = find(body.nodeKey);
    const other = body.otherKey ? find(body.otherKey) : null;
    const kept = hiddenOf(projectId).find((item) => (other ? hidesLink(projectId, node.key, other.key) && [item.nodeKey, item.otherKey].includes(other.key) && [item.nodeKey, item.otherKey].includes(node.key) : item.otherKey === null && item.nodeKey === node.key));
    if (kept) return kept;
    const item: Schema<'SagaMapHiddenItem'> = {
      id: `00000000-0000-4000-b000-${String(nextHiddenId++).padStart(12, '0')}`,
      nodeKey: node.key, nodeName: node.name, otherKey: other?.key ?? null, otherName: other?.name ?? null, createdAt: new Date().toISOString(),
    };
    hiddenByProject.set(projectId, [item, ...hiddenOf(projectId)]);
    return item;
  }

  async function restoreSagaMapItem(projectId: string, hiddenId: string) {
    context.applyOperation('restoreSagaMapItem');
    if (!['editor', 'admin', 'owner'].includes(context.actorRole(projectId))) throw new MockApiError(403, 'Editor access required.', 'restoreSagaMapItem', 'forbidden');
    if (!hiddenOf(projectId).some((item) => item.id === hiddenId)) throw new MockApiError(404, 'Decision not found.', 'restoreSagaMapItem', 'not_found');
    hiddenByProject.set(projectId, hiddenOf(projectId).filter((item) => item.id !== hiddenId));
  }

  async function listSagaGlossary(projectId: string, options: ListSagaGlossaryOptions): Promise<OperationResponse<'listSagaGlossary', '200'>> {
    const saga = version('listSagaGlossary', projectId, options.sourceView);
    const query = normalize(options.q ?? '');
    const types = new Map<string, number>();
    for (const entry of saga.entries) types.set(entry.type, (types.get(entry.type) ?? 0) + 1);
    const found = saga.entries
      .filter((entry) => (!options.type || entry.type === options.type)
        && (!query || normalize([entry.name, ...entry.aliases, entry.note ?? ''].join(' | ')).includes(query)))
      .sort((left, right) => right.chapters.length - left.chapters.length || left.name.localeCompare(right.name, 'ru'));
    const offset = options.offset ?? 0;
    return {
      coverage: saga.coverage,
      types: [...types].map(([type, count]) => ({ type, count })).sort((left, right) => right.count - left.count || left.type.localeCompare(right.type, 'ru')),
      total: found.length,
      offset,
      entries: found.slice(offset, offset + (options.limit ?? 100)).map((entry) => ({
        key: entry.key, name: entry.name, type: entry.type, aliases: entry.aliases, ...(entry.note ? { note: entry.note } : {}),
        chapterCount: entry.chapters.length, first: saga.chapters[entry.chapters[0]!]!,
      })),
    };
  }

  async function getSagaMap(projectId: string, options: GetSagaMapOptions): Promise<OperationResponse<'getSagaMap', '200'>> {
    const saga = version('getSagaMap', projectId, options.sourceView);
    const graph = graphOf(saga);
    const records = materialsOf(projectId);
    // specs/2026-11-04-saga-map.md R9.5: what the book does not know is a node without chapters, linked to what it names.
    const added = records.filter((record) => !saga.lookup(record.name) && materialCategory[record.kind]);
    const bookNodes = graph.nodes.map((entry) => ({
      key: entry.key, name: entry.name, category: categoryOf(entry.type)!, type: entry.type, aliases: entry.aliases, chapters: entry.chapters, material: false,
    }));
    const allNodes = bookNodes.concat(added.map((record) => ({
      key: normalize(record.name), name: record.name, category: materialCategory[record.kind]!, type: 'из ваших материалов · ещё не в книге', aliases: [], chapters: [], material: true,
    })));
    const allLinks: Array<Schema<'SagaMapLink'>> = [
      ...graph.links.filter((link) => honestAt(link, Infinity)),
      ...added.flatMap((record, index) => graph.nodes
        .map((entry, node) => ({ entry, node }))
        .filter(({ entry }) => names(entry).some((name) => mentions(record.description, name)))
        .map(({ node }) => ({ source: node, target: graph.nodes.length + index, events: [], mentions: [], material: true }))),
    ];
    // R11.3: what the author took off the map leaves the answer, the rows stay.
    const moved = new Map<number, number>();
    allNodes.forEach((node, index) => {
      if (!hidesNode(projectId, node.key)) moved.set(index, moved.size);
    });
    const hero = graph.hero >= 0 && !hidesNode(projectId, graph.nodes[graph.hero]!.key) ? graph.nodes[graph.hero]!.key : null;
    return {
      revision: `mock-${saga.chapters.length}-${saga.coverage.withCards}-${records.length}-${records.reduce((sum, record) => sum + record.revision, 0)}-${hiddenOf(projectId).map((item) => item.id).join(',')}`,
      coverage: saga.coverage,
      chapters: saga.chapters,
      hero,
      nodesTotal: graph.total - (allNodes.length - moved.size),
      nodes: allNodes.filter((_, index) => moved.has(index)).map(({ material, ...node }) => (material ? { ...node, material } : node)),
      links: allLinks.flatMap((link) => {
        if (!moved.has(link.source) || !moved.has(link.target) || hidesLink(projectId, allNodes[link.source]!.key, allNodes[link.target]!.key)) return [];
        return [{ ...link, source: moved.get(link.source)!, target: moved.get(link.target)! }];
      }),
    };
  }

  async function searchSagaMap(projectId: string, options: SearchSagaMapOptions): Promise<OperationResponse<'searchSagaMap', '200'>> {
    const saga = version('searchSagaMap', projectId, options.sourceView);
    const query = normalize(options.q ?? '');
    if (!query && options.q?.trim()) return { entries: [] };
    const records = materialsOf(projectId);
    const recordText = (record: Schema<'AuthorMaterial'>) => normalize([record.name, record.description, ...record.properties.map((property) => property.value)].join(' | '));
    const book = saga.entries
      .filter((entry) => normalize([entry.name, ...entry.aliases, entry.note ?? ''].join(' | ')).includes(query)
        || records.some((record) => saga.lookup(record.name) === entry && recordText(record).includes(query)))
      .sort((left, right) => right.chapters.length - left.chapters.length || left.name.localeCompare(right.name, 'ru'))
      .map((entry) => ({ key: entry.key, name: entry.name, type: entry.type }));
    const own = records
      .filter((record) => !saga.lookup(record.name) && recordText(record).includes(query))
      .map((record) => ({ key: normalize(record.name), name: record.name, type: 'из ваших материалов · ещё не в книге', material: true }));
    return { entries: [...book, ...own].slice(0, options.limit ?? 8) };
  }

  async function getSagaMapNode(projectId: string, options: GetSagaMapNodeOptions): Promise<OperationResponse<'getSagaMapNode', '200'>> {
    const saga = version('getSagaMapNode', projectId, options.sourceView);
    const entry = saga.lookup(options.key);
    const records = materialsOf(projectId);
    const materialView = (record: Schema<'AuthorMaterial'>) => ({
      id: record.id, name: record.name, kind: record.kind, description: record.description, properties: record.properties,
      section: record.section, sourceName: record.sourceName, sourceKind: record.sourceKind, revision: record.revision,
    });
    if (!entry) {
      const own = records.find((record) => normalize(record.name) === normalize(options.key));
      if (!own) throw new MockApiError(404, 'Entry not found.', 'getSagaMapNode', 'not_found');
      const graph = graphOf(saga);
      return {
        key: normalize(own.name), name: own.name, type: 'из ваших материалов · ещё не в книге', category: materialCategory[own.kind] ?? null, aliases: [], note: null, first: null,
        mentions: 0, events: [], eventsTotal: 0, changes: [], changesTotal: 0,
        links: graph.nodes.filter((other) => names(other).some((name) => mentions(own.description, name))).map((other) => ({
          key: other.key, name: other.name, category: categoryOf(other.type)!, together: 0, nearby: 0, changes: [], fromMaterials: true,
        })),
        material: materialView(own), namedIn: [],
        ...(hiddenOf(projectId).some((item) => item.otherKey === null && item.nodeKey === normalize(own.name))
          ? { hiddenId: hiddenOf(projectId).find((item) => item.otherKey === null && item.nodeKey === normalize(own.name))!.id } : {}),
      };
    }
    const to = saga.chapterIndex('getSagaMapNode', options.to) ?? saga.chapters.length - 1;
    const firstChapter = entry.chapters[0];
    const firstMention = firstChapter === undefined ? undefined
      : [...(saga.chapterCards[firstChapter]?.characters ?? []), ...(saga.chapterCards[firstChapter]?.entities ?? [])]
        .find((item) => names(entry).some((name) => normalize(name) === normalize(item.name)));
    const events = saga.chapterCards.flatMap((card, chapter) => (chapter > to ? [] : (card?.events ?? [])
      .filter((event) => event.who.some((name) => saga.lookup(name) === entry))
      .map((event) => ({ chapter: saga.chapters[chapter]!, event: event.event, paragraph: event.p, key: event.key ?? false }))));
    const changes = saga.links.filter((link) => link.chapter <= to && link.entries.includes(entry));
    const graph = graphOf(saga);
    const reasons = graph.neighborsOf(entry)
      .filter(({ other }) => !hidesNode(projectId, other.key) && !hidesLink(projectId, entry.key, other.key))
      .map(({ link, other }) => ({ link, other, together: countTo(link.events, to), nearby: countTo(link.mentions, to) }))
      .sort((left, right) => right.together - left.together || right.nearby - left.nearby);
    const honest = reasons.filter(({ link }) => honestAt(link, to));
    const beside = reasons.filter(({ link, together, nearby }) => !honestAt(link, to) && together + nearby > 0);
    const hiddenItem = hiddenOf(projectId).find((item) => item.otherKey === null && item.nodeKey === entry.key);
    return {
      key: entry.key, name: entry.name, type: entry.type, category: categoryOf(entry.type) ?? null, aliases: entry.aliases, note: entry.note ?? null,
      first: firstChapter !== undefined && firstChapter <= to ? { chapter: saga.chapters[firstChapter]!, ...(firstMention ? { paragraph: firstMention.p } : {}) } : null,
      mentions: countTo(entry.chapters, to),
      events: events.slice(-40),
      eventsTotal: events.length,
      changes: changes.slice(-30).map(saga.change),
      changesTotal: changes.length,
      links: honest.slice(0, 10).map(({ other, together, nearby }) => ({
        key: other.key, name: other.name, category: categoryOf(other.type)!, together, nearby,
        changes: saga.links.filter((link) => link.chapter <= to && link.entries.includes(entry) && link.entries.includes(other)).slice(-5).map(saga.change),
        shared: saga.chapterCards.flatMap((card, chapter) => (chapter > to ? [] : (card?.events ?? [])
          .filter((event) => event.who.some((name) => saga.lookup(name) === entry) && event.who.some((name) => saga.lookup(name) === other))
          .map((event) => ({ chapter: saga.chapters[chapter]!, event: event.event, paragraph: event.p, key: event.key ?? false })))).slice(-maxLinkShared),
      })),
      beside: beside.slice(0, maxNodeBeside).map(({ other }) => ({ key: other.key, name: other.name })),
      ...(hiddenItem ? { hiddenId: hiddenItem.id } : {}),
      material: (() => {
        const own = records.find((record) => saga.lookup(record.name) === entry);
        return own ? materialView(own) : null;
      })(),
      namedIn: records.filter((record) => saga.lookup(record.name) !== entry && names(entry).some((name) => mentions(record.description, name))).map((record) => ({ id: record.id, name: record.name, kind: record.kind })),
    };
  }

  async function getSagaContents(projectId: string, options: GetSagaContentsOptions): Promise<OperationResponse<'getSagaContents', '200'>> {
    const saga = version('getSagaContents', projectId, options.sourceView);
    const query = words(options.q ?? '');
    const item = (index: number) => {
      const card = saga.chapterCards[index];
      return { chapter: saga.chapters[index]!, ...(card ? { summary: card.summary } : {}) };
    };
    if (query.length > 0) {
      return {
        coverage: saga.coverage, books: saga.bookList,
        chapters: saga.chapters.map((_, index) => item(index)).filter((chapter) => {
          const text = words(`${chapter.chapter.title} ${chapter.summary ?? ''}`);
          return query.every((word) => text.some((candidate) => candidate.startsWith(word)));
        }),
      };
    }
    const book = saga.bookRange('getSagaContents', options.bookId);
    if (!book) return { coverage: saga.coverage, books: saga.bookList, chapters: [] };
    return {
      coverage: saga.coverage, books: saga.bookList, bookId: book.id,
      chapters: saga.chapters.flatMap((chapter, index) => (chapter.bookId === book.id ? [item(index)] : [])),
    };
  }

  return { listSagaGlossary, getSagaMap, getSagaMapNode, searchSagaMap, getSagaContents, listSagaMapHidden, hideSagaMapItem, restoreSagaMapItem };
}
