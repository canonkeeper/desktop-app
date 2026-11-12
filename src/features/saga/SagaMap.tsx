import { Waypoints } from 'lucide-react';
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import type { AuthorMaterialsApiClient } from '../../entities/author-materials/api';
import { useIndexingStatusQuery, type ManuscriptApiClient } from '../../entities/manuscript/api';
import { chaptersOf, mapReadingLine } from '../../entities/manuscript/preparation-copy';
import { useSagaMapHiddenQuery, useSagaMapQuery, type SagaApiClient, type SagaMap as SagaMapData, type SourceView } from '../../entities/saga/api';
import { navigateFrom, type AppRoute, type RouteNavigate } from '../../shared/navigation/app-route';
import { Button } from '../../ui/controls/Button';
import { EmptyState } from '../../ui/feedback/EmptyState';
import { LongWork, longWorkLine } from '../../ui/feedback/LongWork';
import { SkeletonArea } from '../../ui/feedback/Skeleton';
import { useHostToasts } from '../../ui/feedback/toast-host';
import { Drawer } from '../../ui/overlays/Drawer';
import { Sheet } from '../../ui/overlays/Sheet';
import { WorkplaceHeading } from '../../ui/shell/PageHead';
import { useNarrowScreen } from '../../ui/use-narrow-screen';
import { HiddenPanel } from './HiddenPanel';
import { MapCanvas } from './MapCanvas';
import { MapLegend, useMapLegend } from './MapLegend';
import { formatHidden, mapCategories, parseHidden, projectMap, resolveNodeKey, type MapFilter } from './map-model';
import { mapRoute } from './map-route';
import { viewKey, type MapView } from './map-view';
import { MapCount, MapViewControl } from './MapOverlays';
import { MapToolbar } from './MapToolbar';
import { NodeCard } from './NodeCard';
import { useSagaSlot, type SagaDrawings, type SagaDrawingsOptions } from './saga-slot';
import { LoadError } from './SagaParts';
import { TimeScale } from './TimeScale';
import { useMapDecisions } from './use-map-decisions';
import styles from './SagaMap.module.css';

export interface SagaMapProps {
  api: SagaApiClient & AuthorMaterialsApiClient & Pick<ManuscriptApiClient, 'getIndexingStatus'>;
  /** An editor of the saga: may ask the chat and draw (illustrations R3.1). */
  canEdit: boolean;
  navigate: RouteNavigate;
  projectId: string;
  route: AppRoute;
  sourceView: SourceView;
}

/** How long the scale rests before the URL takes its chapter (R4.1). */
const scaleCommitDelay = 400;

/**
 * «Карта саги» (specs/2026-11-04-saga-map.md): the graph of the saga the server built from the chapter cards, the
 * search, the categories, the scale «к главе N» and the card of a node.
 */
const noDrawings: SagaDrawings = { likeness: () => null, overlays: null };

/** The map without the drawings of the site. */
function useNoDrawings(_options: SagaDrawingsOptions) {
  return noDrawings;
}

export function SagaMap(props: SagaMapProps) {
  const { api, canEdit, navigate, projectId, sourceView } = props;
  const query = useSagaMapQuery(api, projectId, sourceView);
  // How far the helper has read, by the counts and the time of the shelf (specs/2026-11-10-ux3-saga-state.md SS3.2).
  const unread = query.data?.coverage.withCards === 0;
  const preparation = useIndexingStatusQuery(api, projectId, { enabled: unread, follow: true }).data?.preparation;
  const useDrawings = useSagaSlot()?.drawings?.useDrawings ?? useNoDrawings;
  const drawings = useDrawings({ canEdit, navigate, projectId, sourceView });
  // The bar of the map with the heading first in it (specs/2026-11-11-ux2-polish.md PL2.2): the heading stands from the first
  // moment and stays the same element — the focus after a change of the page stays on it (PL2.4); the search and the kinds
  // come into the same row when the map is there, and the page does not jump (PS3.1).
  const [bar, setBar] = useState<HTMLDivElement | null>(null);
  const head = <div className={styles.bar} ref={setBar}><WorkplaceHeading surface="saga" /></div>;
  if (!query.data) {
    return (
      <div className={styles.map}>
        {head}
        {query.isError ? <div className={styles.empty}><LoadError error={query.error} onRetry={() => void query.refetch()} title="Не открылась карта саги" /></div>
          : <SkeletonArea className={styles.loadingMap} shape="cards" />}
      </div>
    );
  }
  const map = query.data;
  if (map.coverage.withCards === 0) {
    return (
      <div className={styles.map}>
        {head}
        {/* The words of the reading are ux3-saga-state's (SS3.2); the block is the one empty state (PS2.1). */}
        <EmptyState
          action={<Button onClick={() => navigate('/manuscript/books', { projectId })} variant="primary">К книгам</Button>}
          className={styles.empty}
          title="Карта появится, когда помощник прочитает книгу"
          why={preparation ? mapReadingLine(preparation) : 'Помощник ещё не прочитал ни одной главы.'}
        />
      </div>
    );
  }
  if (map.building && map.nodes.length === 0) {
    return <div className={styles.map}>{head}<EmptyState className={styles.empty} title="Карта собирается — страница обновится сама" /></div>;
  }
  if (map.nodes.length === 0) {
    return (
      <div className={styles.map}>
        {head}
        <EmptyState className={styles.empty} icon={<Waypoints size={28} />} title="На карте пока никого нет"
          why="Героев и мест, которые встречаются хотя бы в двух главах, помощник не нашёл." />
      </div>
    );
  }
  return (
    <div className={styles.map}>
      {head}
      <MapScreen {...props} bar={bar} drawings={drawings} map={map} />
      {drawings.overlays}
    </div>
  );
}

function MapScreen({ api, bar, canEdit, drawings, map, navigate, projectId, route, sourceView }: SagaMapProps & { bar: HTMLElement | null; drawings: SagaDrawings; map: SagaMapData }) {
  const view = mapRoute(route, navigate, projectId);
  const decisions = useMapDecisions(api, projectId, sourceView);
  const removed = useSagaMapHiddenQuery(api, projectId, sourceView);
  const [cardNotices, setCardNotices] = useState<HTMLDivElement | null>(null);
  const narrow = useNarrowScreen(1100);
  const phone = useNarrowScreen();
  const step = phone ? 30 : 60;
  const [extra, setExtra] = useState(0);
  const [noWebGL, setNoWebGL] = useState(false);
  const unavailable = useCallback(() => setNoWebGL(true), []);
  const camera = useRef<MapView | null>(null);
  const legend = useMapLegend();
  // A name «Найти похожее» puts into the search of the map (MP3.4).
  const [seed, setSeed] = useState<{ id: number; text: string } | null>(null);
  // The node the keyboard stands on in the list beside the canvas: the canvas shows its label (MP1.3).
  const [pointed, setPointed] = useState<string | null>(null);
  // The chat opened from the map leads back to it, «← К карте: Мара» (specs/2026-11-11-ux9-shelf-chat.md SC3.5, П-13).
  const ask = (question: string, name?: string) => navigate('/chat', { projectId }, { chatQuestion: question, from: { section: 'saga', ...(name ? { name } : {}) } });
  const last = map.chapters.length - 1;
  const committedIndex = view.to ? map.chapters.findIndex((chapter) => chapter.key === view.to) : -1;
  const committed = committedIndex >= 0 ? committedIndex : last;
  const [moving, setMoving] = useState<{ base: number; value: number } | null>(null);
  const to = moving && moving.base === committed ? moving.value : committed;
  const limit = step + extra;
  const hide = view.hide;
  const selected = resolveNodeKey(map, view.node);
  // The hidden categories are read from hide, the string the URL keeps, so the projection keeps between renders.
  const graph = useMemo(
    () => projectMap(map, { to, hidden: parseHidden(hide), limit, focus: view.focus ?? null, selected, onlyNew: view.onlyNew }),
    [map, to, hide, limit, view.focus, selected, view.onlyNew],
  );
  const update = view.update;
  useEffect(() => {
    if (!moving || moving.base !== committed || moving.value === committed) return undefined;
    const timer = window.setTimeout(() => {
      update({ to: moving.value >= last ? undefined : map.chapters[moving.value]!.key }, { replace: true });
    }, scaleCommitDelay);
    return () => window.clearTimeout(timer);
  }, [committed, last, map.chapters, moving, update]);

  const select = (key: string | null) => update({ node: key ?? undefined });
  const focus = (key: string) => update({ focus: key, node: key });
  const toggle = (category: MapFilter) => {
    const next = new Set(view.hidden);
    if (next.has(category)) next.delete(category);
    else next.add(category);
    update({ hide: formatHidden(next) }, { replace: true });
  };
  const focusName = view.focus ? map.nodes.find((node) => node.key === view.focus)?.name : undefined;
  const selectedName = selected ? map.nodes.find((node) => node.key === selected)?.name ?? selected : undefined;
  const allHidden = mapCategoriesHidden(view.hidden);
  // A card in a sheet covers the notices above the page: «Отменить» of a decision made there stands in the sheet (R11.3).
  useHostToasts(selected && narrow ? cardNotices : null);
  const card = selected ? (
    <NodeCard api={api} canEdit={canEdit} decisions={decisions} drawings={drawings} key={selected} map={map} nodeKey={selected}
      onAsk={(question) => ask(question, selectedName)}
      onClose={() => select(null)}
      onFindLike={(name) => {
        setSeed((current) => ({ id: (current?.id ?? 0) + 1, text: name }));
        select(null);
      }}
      onFocus={focus}
      onSelect={select}
      projectId={projectId} sourceView={sourceView} target={{ navigate: navigateFrom(navigate, { section: 'saga', ...(selectedName ? { name: selectedName } : {}) }), projectId }}
      to={to} toKey={committed >= last ? undefined : map.chapters[committed]!.key} />
  ) : null;

  // The card and what was removed live in the address: their own «Назад» closes them (PA4.2).
  const side = (label: string, onDismiss: () => void, body: ReactNode) => (phone
    ? <Sheet canDismiss className={styles.drawerCard} history={false} label={label} onDismiss={onDismiss}><div className={styles.drawerScroll}>{body}</div></Sheet>
    : <Drawer canDismiss className={styles.drawerCard} history={false} label={label} onDismiss={onDismiss} side="right" size="reading"><div className={styles.drawerScroll}>{body}</div></Drawer>);

  const shown = Math.min(graph.total, graph.nodes.length);
  // The map is still read or rebuilt: one line above the graph, not over it, and one thing at a time (MP2.4, К-04).
  const read = map.coverage.withCards < map.coverage.chapters ? `Помощник прочитал ${chaptersOf(map.coverage.withCards, map.coverage.chapters)}` : null;
  const state = map.building
    ? <LongWork className={styles.mapStateWork} detail={longWorkLine({ term: read ?? undefined, leave: 'новое появится на карте само' })} label="Карта обновляется" title="Карта обновляется" />
    : read ? <p className={styles.mapState}>{read} — карта дополняется сама</p> : null;

  // The bar and its heading are SagaMap's: the heading stays the same element from the loading on (the focus SH2 stays on
  // it), the search and the kinds stand beside it in the same row (PL2.2).
  return (
    <>
      {bar ? createPortal(
        <MapToolbar api={api} canEdit={canEdit} counts={graph.counts} hidden={view.hidden} map={map} navigate={navigate} onAsk={ask} onLegend={legend.show}
          onOpenRemoved={() => update({ removed: 'open' })} onSelect={select} onToggle={toggle} phone={phone} projectId={projectId}
          removed={removed.data?.items.length ?? 0} seed={seed} sourceView={sourceView} />, bar) : null}
      {legend.shown ? <MapLegend onHide={legend.hide} /> : null}
      {state}
      <div className={styles.stage}>
        {/* The keys «+», «−», «0» move the view while the focus is on the map (MP1.1). */}
        <section aria-keyshortcuts="+ - 0" aria-label="Карта саги" className={styles.canvasArea}
          onKeyDown={(event) => {
            const move = viewKey(event.nativeEvent);
            if (!move || !camera.current) return;
            event.preventDefault();
            camera.current[move]();
          }} tabIndex={0}>
          {noWebGL ? (
            <div className={styles.fallback}>
              <div className={styles.fallbackWords} role="note">
                <p><strong>Этот браузер не умеет рисовать карту. Имена, поиск и карточки работают — вот кто в саге:</strong></p>
                <p>Чтобы карта рисовалась, включите ускорение графики: Chrome и Edge — Настройки → Система → «Использовать аппаратное ускорение», затем перезапустите браузер; Firefox — Настройки → Основные → Производительность.</p>
              </div>
              <NodeList graph={graph} onSelect={select} />
            </div>
          ) : <>
            <MapCanvas graph={graph} hoverable={!phone} onFocus={focus} onSelect={select} onUnavailable={unavailable} pointed={pointed} selected={selected} view={camera} />
            <div className={styles.srOnly}><NodeList graph={graph} onPoint={setPointed} onSelect={select} /></div>
            <MapViewControl phone={phone} view={camera} />
          </>}
          {graph.nodes.length === 0 ? (
            <p className={styles.blank}>{allHidden ? 'Включите хотя бы один тип вверху.' : 'До этой главы на карте никого нет.'}</p>
          ) : null}
          {focusName || view.onlyNew ? (
            <div className={styles.overlayTop}>
              {focusName ? (
                <div className={styles.focusBar}>
                  <span>Окружение: <strong>{focusName}</strong></span>
                  <Button onClick={() => update({ focus: undefined })} size="sm" variant="ghost">Вся карта</Button>
                </div>
              ) : null}
              {view.onlyNew ? (
                // «Показать новые на карте» of «Что помнит помощник»: the records the book does not know, the rest pale (MP3.5).
                <div className={styles.focusBar}>
                  <span>{graph.counts.material ? `Ваши записи, которых нет в книге: ${graph.counts.material}`
                    : map.building ? 'Карта обновляется — новые записи появятся через минуту-две' : 'На карте нет ваших записей, которых нет в книге'}</span>
                  <Button onClick={() => update({ only: undefined })} size="sm" variant="ghost">Вся карта</Button>
                </div>
              ) : null}
            </div>
          ) : null}
          {!focusName && graph.total > 0 ? (
            <MapCount onAll={() => setExtra(graph.total)} onMore={() => setExtra((value) => value + step)} shown={shown} step={step} total={graph.total} />
          ) : null}
        </section>
        {card && !narrow ? <aside aria-label={`Карточка: ${selectedName}`} className={styles.card}>{card}</aside> : null}
      </div>
      <TimeScale chapters={map.chapters} onMove={(index) => setMoving({ base: committed, value: Math.max(0, Math.min(last, index)) })} value={to} />
      {card && narrow ? side(`Карточка: ${selectedName}`, () => select(null), <><div className={styles.cardNotices} ref={setCardNotices} />{card}</>) : null}
      {view.removed ? side('Убранное с карты', () => update({ removed: undefined }), (
        <HiddenPanel api={api} canEdit={canEdit} decisions={decisions} onOpen={(key) => update({ node: key, removed: undefined })} projectId={projectId} sourceView={sourceView} />
      )) : null}
    </>
  );
}

/** The nodes on the map as buttons: for a screen reader beside the canvas, and instead of it without WebGL (R3.8). */
function NodeList({ graph, onPoint, onSelect }: { graph: ReturnType<typeof projectMap>; onPoint?: (key: string | null) => void; onSelect: (key: string) => void }) {
  return (
    <ul aria-label="Узлы на карте" className={styles.nodeList}>
      {graph.nodes.map((node) => (
        <li key={node.key}>
          <Button onBlur={() => onPoint?.(null)} onClick={() => onSelect(node.key)} onFocus={() => onPoint?.(node.key)} size="sm" variant="outline">
            <span aria-hidden="true" className={styles.dot} data-category={node.category} />{node.name}
          </Button>
        </li>
      ))}
    </ul>
  );
}

/** Every category of the map is off: nothing to show whatever the chapter. */
function mapCategoriesHidden(hidden: ReadonlySet<MapFilter>) {
  return mapCategories.every(({ category }) => hidden.has(category));
}
