import { useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import type { AuthorMaterialsApiClient } from '../../entities/author-materials/api';
import { useSagaMapHiddenQuery, useSagaMapQuery, type SagaApiClient, type SagaMap as SagaMapData, type SourceView } from '../../entities/saga/api';
import type { AppRoute, RouteNavigate } from '../../shared/navigation/app-route';
import { Button } from '../../ui/controls/Button';
import { pluralize } from '../../ui/copy';
import { Alert } from '../../ui/feedback/Alert';
import { EmptyState } from '../../ui/feedback/EmptyState';
import { NoticeSlot } from '../../ui/feedback/notice-slot';
import { ToastRegion } from '../../ui/feedback/Toast';
import { Drawer } from '../../ui/overlays/Drawer';
import { Sheet } from '../../ui/overlays/Sheet';
import { useNarrowScreen } from '../../ui/use-narrow-screen';
import { HiddenPanel } from './HiddenPanel';
import { MapCanvas } from './MapCanvas';
import { formatHidden, mapCategories, parseHidden, projectMap, resolveNodeKey, type MapFilter } from './map-model';
import { mapRoute } from './map-route';
import { MaterialsPanel } from './materials/MaterialsPanel';
import { MapToolbar } from './MapToolbar';
import { NodeCard } from './NodeCard';
import { useSagaSlot, type SagaDrawings, type SagaDrawingsOptions } from './saga-slot';
import { LoadError, LoadingRows } from './SagaParts';
import { TimeScale } from './TimeScale';
import { useMapDecisions } from './use-map-decisions';
import styles from './SagaMap.module.css';

export interface SagaMapProps {
  api: SagaApiClient & AuthorMaterialsApiClient;
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
  const useDrawings = useSagaSlot()?.drawings?.useDrawings ?? useNoDrawings;
  const drawings = useDrawings({ canEdit, navigate, projectId, sourceView });
  if (!query.data) {
    return (
      <div className={styles.map}>
        {query.isError ? <div className={styles.empty}><LoadError error={query.error} onRetry={() => void query.refetch()} what="карту" /></div>
          : <LoadingRows label="Загрузка карты" />}
      </div>
    );
  }
  const map = query.data;
  if (map.coverage.withCards === 0) {
    return (
      <EmptyState
        action={<Button onClick={() => navigate('/manuscript/books', { projectId })} variant="primary">К книгам</Button>}
        className={styles.empty}
        description="Она строится из карточек глав. Карточки появятся, когда книга будет подготовлена к чату — это видно на полке."
        title="Карта появится, когда книга будет подготовлена"
      />
    );
  }
  if (map.building && map.nodes.length === 0) {
    return <EmptyState className={styles.empty} description="Она собирается из карточек глав. Страница обновится сама, ждать на ней не обязательно." title="Карта готовится" />;
  }
  if (map.nodes.length === 0) {
    return <EmptyState className={styles.empty} description="В карточках глав нет героев, мест, фракций и предметов, которые встречаются хотя бы в двух главах." title="На карте пока никого нет" />;
  }
  return <>
    <MapScreen {...props} drawings={drawings} map={map} />
    {drawings.overlays}
  </>;
}

function MapScreen({ api, canEdit, drawings, map, navigate, projectId, route, sourceView }: SagaMapProps & { drawings: SagaDrawings; map: SagaMapData }) {
  const view = mapRoute(route, navigate, projectId);
  const decisions = useMapDecisions(api, projectId, sourceView);
  const removed = useSagaMapHiddenQuery(api, projectId, sourceView);
  // A card in a sheet covers the notices above the page: «Отменить» of a decision made there stands in the sheet (R11.3).
  const pageNotices = useContext(NoticeSlot);
  const [cardNotices, setCardNotices] = useState<HTMLDivElement | null>(null);
  const narrow = useNarrowScreen(1100);
  const phone = useNarrowScreen();
  const step = phone ? 30 : 60;
  const [extra, setExtra] = useState(0);
  const [noWebGL, setNoWebGL] = useState(false);
  const unavailable = useCallback(() => setNoWebGL(true), []);
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
    () => projectMap(map, { to, hidden: parseHidden(hide), limit, focus: view.focus ?? null, selected }),
    [map, to, hide, limit, view.focus, selected],
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
  const card = selected ? (
    <NodeCard api={api} canEdit={canEdit} decisions={decisions} drawings={drawings} key={selected} map={map} nodeKey={selected}
      onAsk={(name) => navigate('/chat', { projectId }, { chatQuestion: `Расскажи о «${name}»: кто или что это, ключевые события и связи по книге.` })}
      onClose={() => select(null)}
      onFocus={focus}
      onSelect={select}
      projectId={projectId} sourceView={sourceView} target={{ navigate, projectId }}
      to={to} toKey={committed >= last ? undefined : map.chapters[committed]!.key} />
  ) : null;

  const panel = <MaterialsPanel api={api} canEdit={canEdit} onOpen={(name) => update({ node: name, materials: undefined })} projectId={projectId} />;
  const side = (label: string, onDismiss: () => void, body: ReactNode) => (phone
    ? <Sheet canDismiss className={styles.drawerCard} label={label} onDismiss={onDismiss}><div className={styles.drawerScroll}>{body}</div></Sheet>
    : <Drawer canDismiss className={styles.drawerCard} label={label} onDismiss={onDismiss} side="right" size="reading"><div className={styles.drawerScroll}>{body}</div></Drawer>);

  return (
    <div className={styles.map}>
      <h1 className={styles.srOnly}>Карта саги</h1>
      <MapToolbar api={api} counts={graph.counts} hidden={view.hidden} map={map} onOpenMaterials={() => update({ materials: 'open' })} onOpenMemory={() => navigate('/memory', { projectId })}
        onOpenRemoved={() => update({ removed: 'open' })} onSelect={select} onToggle={toggle} projectId={projectId} removed={removed.data?.items.length ?? 0} sourceView={sourceView} />
      <div className={styles.stage}>
        <section aria-label="Карта саги" className={styles.canvasArea}>
          {noWebGL ? (
            <div className={styles.fallback}>
              <Alert tone="info">Браузер не показывает карту: включите аппаратное ускорение. Пока вот кто на ней:</Alert>
              <NodeList graph={graph} onSelect={select} />
            </div>
          ) : <>
            <MapCanvas graph={graph} hoverable={!phone} onFocus={focus} onSelect={select} onUnavailable={unavailable} selected={selected} />
            <div className={styles.srOnly}><NodeList graph={graph} onSelect={select} /></div>
          </>}
          {graph.nodes.length === 0 ? (
            <p className={styles.blank}>{allHidden ? 'Включите хотя бы один тип вверху.' : 'До этой главы на карте никого нет.'}</p>
          ) : null}
          <div className={styles.overlayTop}>
            {map.building ? <Alert className={styles.coverage} tone="info">Карта обновляется: новое появится само.</Alert> : null}
            {focusName ? (
              <div className={styles.focusBar}>
                <span>Окружение: <strong>{focusName}</strong></span>
                <Button onClick={() => update({ focus: undefined })} size="sm" variant="ghost">Вся карта</Button>
              </div>
            ) : null}
            {map.coverage.withCards < map.coverage.chapters ? (
              <Alert className={styles.coverage} tone="info">
                Карточки построены для {map.coverage.withCards} из {map.coverage.chapters} {pluralize(map.coverage.chapters, 'главы', 'глав', 'глав')} — карта пока неполна.
              </Alert>
            ) : null}
          </div>
          {!focusName && graph.total > 0 ? (
            <div className={styles.overlayBottom}>
              <span>На карте {Math.min(graph.total, graph.nodes.length)} из {graph.total}</span>
              {graph.total > limit ? <Button onClick={() => setExtra((value) => value + step)} size="sm" variant="ghost">Показать больше</Button> : null}
            </div>
          ) : null}
        </section>
        {card && !narrow ? <aside aria-label={`Карточка: ${selectedName}`} className={styles.card}>{card}</aside> : null}
      </div>
      <TimeScale chapters={map.chapters} onMove={(index) => setMoving({ base: committed, value: Math.max(0, Math.min(last, index)) })} value={to} />
      {card && narrow ? side(`Карточка: ${selectedName}`, () => select(null), <><div className={styles.cardNotices} ref={setCardNotices} />{card}</>) : null}
      {view.materials ? side('Материалы автора', () => update({ materials: undefined }), panel) : null}
      {view.removed ? side('Убранное', () => update({ removed: undefined }), (
        <HiddenPanel api={api} canEdit={canEdit} decisions={decisions} onOpen={(key) => update({ node: key, removed: undefined })} projectId={projectId} sourceView={sourceView} />
      )) : null}
      <NoticeSlot.Provider value={card && narrow ? cardNotices : pageNotices}>
        <ToastRegion onDismiss={decisions.toasts.dismiss} queue={decisions.toasts.queue} />
      </NoticeSlot.Provider>
    </div>
  );
}

/** The nodes on the map as buttons: for a screen reader beside the canvas, and instead of it without WebGL (R3.8). */
function NodeList({ graph, onSelect }: { graph: ReturnType<typeof projectMap>; onSelect: (key: string) => void }) {
  return (
    <ul aria-label="Узлы на карте" className={styles.nodeList}>
      {graph.nodes.map((node) => (
        <li key={node.key}>
          <Button onClick={() => onSelect(node.key)} size="sm" variant="outline">
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
