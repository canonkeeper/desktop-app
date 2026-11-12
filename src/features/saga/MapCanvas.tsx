import Graph from 'graphology';
import { useEffect, useRef, useState, type MutableRefObject } from 'react';
import type Sigma from 'sigma';
import type { SagaMapCategory } from '../../entities/saga/api';
import { useSiteThemeStore } from '../../shared/theme/site-theme';
import { createMapLayout, type LayoutLink, type Positions } from './map-layout';
import { ordinaryLabel, placeLabels, seedLayout, type LabelCandidate } from './map-layout-core';
import type { ViewGraph } from './map-model';
import { cameraView, type MapView } from './map-view';
import styles from './SagaMap.module.css';

export interface MapCanvasProps {
  graph: ViewGraph;
  selected: string | null;
  /** A pointer that hovers: a phone has none, a tap opens the card (R3.9). */
  hoverable: boolean;
  onSelect: (key: string | null) => void;
  onFocus: (key: string) => void;
  /** The browser cannot draw WebGL (R3.8). */
  onUnavailable: () => void;
  /** «+», «−», «Показать всё» of the page move the camera of the canvas (specs/2026-11-11-ux8-map.md MP1.1). */
  view?: MutableRefObject<MapView | null>;
  /** The node the keyboard stands on in the list beside the canvas: lit with its label, as under the pointer (MP1.3). */
  pointed?: string | null;
}

interface Palette {
  categories: Record<SagaMapCategory, string>;
  link: string;
  weak: string;
  dim: string;
  pane: string;
  label: string;
  /** The plate under the name of a hovered node, and its shadow. */
  plate: string;
  shadow: string;
  font: string;
}

function readPalette(element: HTMLElement): Palette {
  const style = getComputedStyle(element);
  // The tokens are always there (src/styles/tokens.css): no light value of their own here (specs/2026-11-12-ux11-dark-sections.md DS3.2).
  const token = (name: string) => style.getPropertyValue(name).trim();
  return {
    categories: {
      character: token('--ck-map-character'),
      place: token('--ck-map-place'),
      faction: token('--ck-map-faction'),
      item: token('--ck-map-item'),
    },
    link: token('--ck-map-link'),
    weak: token('--ck-map-link-weak'),
    dim: token('--ck-map-dim'),
    pane: token('--ck-reader-pane'),
    label: token('--ck-foreground'),
    plate: token('--ck-card'),
    shadow: token('--ck-map-plate-shadow'),
    font: token('--ck-font-sans') || 'sans-serif',
  };
}

/** What sigma's drawing functions are given of a node and of the settings. */
interface HoverData { x: number; y: number; size: number; label?: string | null }
interface HoverSettings { labelSize: number; labelFont: string; labelWeight: string }

/**
 * The name of a hovered node on its plate, as sigma draws it (drawDiscNodeHover), but in the colours of the theme: sigma's
 * own plate is always white, and a light name of the dark theme would stand on it unread (specs/2026-11-12-ux11-dark-core.md
 * TH3.2).
 */
function drawHover(context: CanvasRenderingContext2D, data: HoverData, settings: HoverSettings, palette: Palette) {
  const size = settings.labelSize;
  context.font = `${settings.labelWeight} ${size}px ${settings.labelFont}`;
  context.fillStyle = palette.plate;
  context.shadowOffsetX = 0;
  context.shadowOffsetY = 0;
  context.shadowBlur = 8;
  context.shadowColor = palette.shadow;
  const padding = 2;
  if (typeof data.label === 'string') {
    const boxWidth = Math.round(context.measureText(data.label).width + 5);
    const boxHeight = Math.round(size + 2 * padding);
    const radius = Math.max(data.size, size / 2) + padding;
    const angle = Math.asin(boxHeight / 2 / radius);
    const xDelta = Math.sqrt(Math.abs(radius ** 2 - (boxHeight / 2) ** 2));
    context.beginPath();
    context.moveTo(data.x + xDelta, data.y + boxHeight / 2);
    context.lineTo(data.x + radius + boxWidth, data.y + boxHeight / 2);
    context.lineTo(data.x + radius + boxWidth, data.y - boxHeight / 2);
    context.lineTo(data.x + xDelta, data.y - boxHeight / 2);
    context.arc(data.x, data.y, radius, angle, -angle);
    context.closePath();
    context.fill();
  } else {
    context.beginPath();
    context.arc(data.x, data.y, data.size + padding, 0, Math.PI * 2);
    context.closePath();
    context.fill();
  }
  context.shadowBlur = 0;
  if (!data.label) return;
  context.fillStyle = palette.label;
  context.fillText(data.label, data.x + data.size + 3, data.y + size / 3);
}

/** A record of the author has no chapters to count: a small node, but one to see and to hit. */
const materialNodeSize = 7;

/** A label of the canvas as sigma would draw it, before the page chooses the ones that fit (MP1.3). */
interface DrawnLabel {
  key: string;
  label: string;
  x: number;
  y: number;
  size: number;
}

/** How long a move of the camera by a button takes. */
const cameraMove = { duration: 250 };

/**
 * The canvas of the saga map (specs/2026-11-04-saga-map.md, UC3): `sigma` draws the view with WebGL, the layout
 * worker moves the nodes; the hovered or open node lights up with its neighbors (R3.5). The graph is changed in
 * place, so laid out nodes keep their places when the chapter, the filters or the limit change (R4.3).
 */
export function MapCanvas({ graph, hoverable, onFocus, onSelect, onUnavailable, pointed = null, selected, view }: MapCanvasProps) {
  const container = useRef<HTMLDivElement>(null);
  // The canvas reads the colours of the theme of the site and draws again when it changes (TH3.2).
  const theme = useSiteThemeStore((state) => state.theme);
  const state = useRef({
    graph: new Graph({ type: 'undirected' }),
    renderer: null as Sigma | null,
    layout: null as ReturnType<typeof createMapLayout> | null,
    palette: null as Palette | null,
    positions: new Map() as Positions,
    hovered: null as string | null,
    selected,
    hoverable,
    hero: null as string | null,
    labels: [] as DrawnLabel[],
    widths: new Map<string, number>(),
    laidOut: false,
    callbacks: { onFocus, onSelect },
  });
  state.current.selected = selected;
  state.current.hoverable = hoverable;
  state.current.callbacks = { onFocus, onSelect };

  const [ready, setReady] = useState(false);
  useEffect(() => {
    const element = container.current;
    if (!element) return undefined;
    const current = state.current;
    let cancelled = false;
    let observer: ResizeObserver | null = null;
    // sigma loads with the canvas: it needs WebGL as soon as it is imported (R3.8).
    import('sigma').then(({ default: SigmaRenderer }) => {
      if (cancelled) return;
      const palette = readPalette(element);
      current.palette = palette;
      // The colours of now: the theme of the site may change while the map is open.
      const colors = () => current.palette ?? palette;
      const font = `500 13px ${palette.font}`;
      // The labels that fit, the open or hovered node first, then the hero and the neighbors of the open one, then the
      // bigger nodes (MP1.3); the open and the hovered node have theirs in the hover layer of sigma.
      const drawLabels = (context: CanvasRenderingContext2D, colors: Palette) => {
        const center = (current.hoverable ? current.hovered : null) ?? current.selected;
        const centered = center && current.graph.hasNode(center) ? center : null;
        context.font = font;
        const candidates: LabelCandidate[] = current.labels.map((label) => {
          let width = current.widths.get(label.label);
          if (width === undefined) {
            width = context.measureText(label.label).width;
            current.widths.set(label.label, width);
          }
          const tier = label.key === current.selected || label.key === current.hovered ? 0
            : label.key === current.hero ? 1
              : centered && current.graph.hasNode(label.key) && current.graph.areNeighbors(label.key, centered) ? 2 : ordinaryLabel;
          return { key: label.key, x: label.x, y: label.y, size: label.size, width, height: 15, tier };
        });
        const shown = placeLabels(candidates);
        context.fillStyle = colors.label;
        for (const label of current.labels) {
          if (!shown.has(label.key) || label.key === current.selected || label.key === current.hovered) continue;
          context.fillText(label.label, label.x + label.size + 3, label.y + 13 / 3);
        }
      };
      const renderer = new SigmaRenderer(current.graph, element, {
        allowInvalidContainer: true,
        defaultEdgeColor: palette.link,
        labelColor: { color: palette.label },
        labelFont: palette.font,
        labelRenderedSizeThreshold: 0,
        labelSize: 13,
        labelWeight: '500',
        defaultDrawNodeHover: (context, data, settings) => drawHover(context, data, settings, colors()),
        // Every label comes here first; the ones that fit are drawn after the frame (MP1.3).
        defaultDrawNodeLabel: (_context, data) => {
          if (data.label) current.labels.push({ key: (data as { key?: string }).key ?? '', label: data.label, x: data.x, y: data.y, size: data.size });
        },
        stagePadding: 40,
        zIndex: true,
        minCameraRatio: 0.08,
        maxCameraRatio: 4,
        nodeReducer: (node, data) => {
          const center = (current.hoverable ? current.hovered : null) ?? current.selected;
          const attributes = { ...data };
          if (node === current.selected || node === current.hovered) {
            attributes.highlighted = true;
            attributes.forceLabel = true;
            attributes.zIndex = 2;
          } else if (data.dim || (center && current.graph.hasNode(center) && !current.graph.areNeighbors(node, center))) {
            attributes.color = colors().dim;
            attributes.label = null;
            attributes.forceLabel = false;
            attributes.zIndex = 0;
          } else if (center) {
            attributes.forceLabel = true;
            attributes.zIndex = 1;
          }
          return attributes;
        },
        edgeReducer: (edge, data) => {
          const center = (current.hoverable ? current.hovered : null) ?? current.selected;
          const [source, target] = current.graph.extremities(edge);
          if (!center && (current.graph.getNodeAttribute(source, 'dim') || current.graph.getNodeAttribute(target, 'dim'))) return { ...data, color: colors().dim, zIndex: 0 };
          if (!center || !current.graph.hasNode(center)) return data;
          if (!current.graph.hasExtremity(edge, center)) return { ...data, color: colors().dim, zIndex: 0 };
          const color = colors().categories[current.graph.getNodeAttribute(center, 'category') as SagaMapCategory];
          return { ...data, color, zIndex: 1, size: Math.max(data.size, 1.5) };
        },
      });
      current.renderer = renderer;
      // A record of the author the book does not know has a dashed outline (R9.5): WebGL draws no dashes, a canvas above it does.
      renderer.createCanvasContext('materials', { style: { pointerEvents: 'none' } });
      renderer.resize(true);
      const outlineContext = renderer.getCanvases().materials!.getContext('2d');
      const labelContext = renderer.getCanvases().labels!.getContext('2d');
      renderer.on('beforeRender', () => {
        current.labels = [];
      });
      renderer.on('afterRender', () => {
        if (labelContext) drawLabels(labelContext, colors());
        if (!outlineContext) return;
        const { width, height } = renderer.getDimensions();
        outlineContext.clearRect(0, 0, width, height);
        outlineContext.setLineDash([4, 3]);
        outlineContext.lineWidth = 2;
        current.graph.forEachNode((key, attributes) => {
          const shape = attributes.material ? renderer.getNodeDisplayData(key) : undefined;
          if (!shape || shape.hidden) return;
          const center = renderer.framedGraphToViewport(shape);
          outlineContext.strokeStyle = shape.color === colors().dim ? colors().dim : colors().categories[attributes.category as SagaMapCategory];
          outlineContext.beginPath();
          outlineContext.arc(center.x, center.y, renderer.scaleSize(shape.size), 0, 2 * Math.PI);
          outlineContext.stroke();
        });
      });
      renderer.on('clickNode', ({ node }) => current.callbacks.onSelect(node));
      renderer.on('clickStage', () => current.callbacks.onSelect(null));
      renderer.on('doubleClickNode', (event) => {
        event.preventSigmaDefault();
        current.callbacks.onFocus(event.node);
      });
      renderer.on('enterNode', ({ node }) => {
        if (!current.hoverable) return;
        current.hovered = node;
        element.style.cursor = 'pointer';
        renderer.refresh({ skipIndexation: true });
      });
      renderer.on('leaveNode', () => {
        current.hovered = null;
        element.style.cursor = '';
        renderer.refresh({ skipIndexation: true });
      });
      current.layout = createMapLayout((result) => {
        let extent = 1;
        for (const [key, x, y] of result.positions) {
          current.positions.set(key, { x, y });
          if (current.graph.hasNode(key)) current.graph.mergeNodeAttributes(key, { x, y });
          extent = Math.max(extent, Math.abs(x), Math.abs(y));
        }
        // A box symmetric around the origin keeps the pinned hero in the middle of the screen (R3.1).
        renderer.setCustomBBox({ x: [-extent, extent], y: [-extent, extent] });
        renderer.refresh();
      });
      // A box that changes its size clears the canvas (a notice above the page comes and goes): sigma redraws only for the window.
      observer = typeof ResizeObserver === 'function' ? new ResizeObserver(() => renderer.scheduleRefresh()) : null;
      observer?.observe(element);
      if (view) {
        view.current = cameraView(renderer.getCamera(), cameraMove);
      }
      setReady(true);
    }).catch(() => {
      if (!cancelled) onUnavailable();
    });
    return () => {
      cancelled = true;
      if (view) view.current = null;
      observer?.disconnect();
      current.layout?.dispose();
      current.layout = null;
      current.renderer?.kill();
      current.renderer = null;
    };
  }, [onUnavailable, view]);

  useEffect(() => {
    const current = state.current;
    const target = current.graph;
    if (!current.palette || !container.current) return;
    // Read again on every change of the graph and of the theme: the nodes and the links take the colours of now.
    const palette = readPalette(container.current);
    current.palette = palette;
    current.renderer?.setSetting('labelColor', { color: palette.label });
    current.renderer?.setSetting('defaultEdgeColor', palette.link);
    const wanted = new Set(graph.nodes.map((node) => node.key));
    let changed = false;
    target.forEachNode((key) => {
      if (!wanted.has(key)) {
        target.dropNode(key);
        changed = true;
      }
    });
    const maxCount = Math.max(1, ...graph.nodes.map((node) => node.count));
    const maxTogether = Math.max(1, ...graph.links.map((link) => link.together));
    const links: LayoutLink[] = graph.links.map((link) => ({
      source: link.source, target: link.target, weight: link.weak ? 0.2 * Math.max(1, link.nearby) : link.together,
    }));
    const hero = graph.nodes.find((node) => node.hero)?.key ?? null;
    const sized = graph.nodes.map((node) => ({ key: node.key, size: node.material ? materialNodeSize : 3 + 15 * Math.sqrt(node.count / maxCount) }));
    const seeded = new Map(seedLayout(sized, links, current.positions, hero).map((node) => [node.key, node]));
    for (const node of graph.nodes) {
      const seed = seeded.get(node.key)!;
      const attributes = {
        label: node.name, size: seed.size, color: node.material ? palette.pane : palette.categories[node.category], category: node.category, material: node.material,
        // Every label goes to the choice of the ones that fit (MP1.3); a node of «только новое» from the book is dimmed (MP3.5).
        forceLabel: true, dim: node.dim, fixed: seed.fixed,
      };
      if (target.hasNode(node.key)) target.mergeNodeAttributes(node.key, attributes);
      else {
        target.addNode(node.key, { ...attributes, x: seed.x, y: seed.y });
        current.positions.set(node.key, { x: seed.x, y: seed.y });
        changed = true;
      }
    }
    const wantedLinks = new Set(graph.links.map((link) => link.key));
    target.forEachEdge((edge) => {
      if (!wantedLinks.has(edge)) {
        target.dropEdge(edge);
        changed = true;
      }
    });
    for (const link of graph.links) {
      const attributes = link.weak
        ? { size: 0.5, color: palette.weak }
        : { size: 0.8 + 2.2 * (Math.log2(1 + link.together) / Math.log2(1 + maxTogether)), color: palette.link };
      if (target.hasEdge(link.key)) target.mergeEdgeAttributes(link.key, attributes);
      else {
        target.addEdgeWithKey(link.key, link.source, link.target, attributes);
        changed = true;
      }
    }
    current.hero = hero;
    if (changed || !current.laidOut) {
      current.layout?.run({
        nodes: graph.nodes.map((node) => {
          const seed = seeded.get(node.key)!;
          const at = current.positions.get(node.key) ?? seed;
          return { key: node.key, size: seed.size, x: at.x, y: at.y, fixed: seed.fixed };
        }),
        links,
        iterations: current.laidOut ? 120 : 300,
      });
      current.laidOut = true;
    }
    current.renderer?.refresh();
  }, [graph, ready, theme]);

  useEffect(() => {
    const { renderer } = state.current;
    if (!renderer) return;
    renderer.refresh({ skipIndexation: true });
    if (!selected || !state.current.graph.hasNode(selected)) return;
    const data = renderer.getNodeDisplayData(selected);
    if (data) void renderer.getCamera().animate({ x: data.x, y: data.y }, { duration: 400 });
  }, [selected]);

  useEffect(() => {
    const current = state.current;
    current.hovered = pointed;
    current.renderer?.refresh({ skipIndexation: true });
  }, [pointed]);

  return <div aria-hidden="true" className={styles.canvas} data-testid="saga-map-canvas" ref={container} />;
}
