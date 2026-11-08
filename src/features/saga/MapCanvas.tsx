import Graph from 'graphology';
import { useEffect, useRef, useState } from 'react';
import type Sigma from 'sigma';
import type { SagaMapCategory } from '../../entities/saga/api';
import { createMapLayout, type LayoutLink, type Positions } from './map-layout';
import { seedLayout } from './map-layout-core';
import type { ViewGraph } from './map-model';
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
}

interface Palette {
  categories: Record<SagaMapCategory, string>;
  link: string;
  weak: string;
  dim: string;
  pane: string;
  label: string;
  font: string;
}

function readPalette(element: HTMLElement): Palette {
  const style = getComputedStyle(element);
  const token = (name: string, fallback: string) => style.getPropertyValue(name).trim() || fallback;
  return {
    categories: {
      character: token('--ck-map-character', '#5442c5'),
      place: token('--ck-map-place', '#2f7d5b'),
      faction: token('--ck-map-faction', '#b4532a'),
      item: token('--ck-map-item', '#2a6fb4'),
    },
    link: token('--ck-map-link', '#a1a1aa'),
    weak: token('--ck-map-link-weak', '#dcdcd8'),
    dim: token('--ck-map-dim', '#e4e4e1'),
    pane: token('--ck-reader-pane', '#f7f7f5'),
    label: token('--ck-foreground', '#18181b'),
    font: token('--ck-font-sans', 'sans-serif'),
  };
}

/** A record of the author has no chapters to count: a small node, but one to see and to hit. */
const materialNodeSize = 7;

/** Labels always shown: the hero, the open and the hovered node and the most mentioned ones. */
const alwaysLabeled = 8;

/**
 * The canvas of the saga map (specs/2026-11-04-saga-map.md, UC3): `sigma` draws the view with WebGL, the layout
 * worker moves the nodes; the hovered or open node lights up with its neighbors (R3.5). The graph is changed in
 * place, so laid out nodes keep their places when the chapter, the filters or the limit change (R4.3).
 */
export function MapCanvas({ graph, hoverable, onFocus, onSelect, onUnavailable, selected }: MapCanvasProps) {
  const container = useRef<HTMLDivElement>(null);
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
      const renderer = new SigmaRenderer(current.graph, element, {
        allowInvalidContainer: true,
        defaultEdgeColor: palette.link,
        labelColor: { color: palette.label },
        labelFont: palette.font,
        labelRenderedSizeThreshold: 10,
        labelSize: 13,
        labelWeight: '500',
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
          } else if (center && current.graph.hasNode(center) && !current.graph.areNeighbors(node, center)) {
            attributes.color = palette.dim;
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
          if (!center || !current.graph.hasNode(center)) return data;
          if (!current.graph.hasExtremity(edge, center)) return { ...data, color: palette.dim, zIndex: 0 };
          const color = palette.categories[current.graph.getNodeAttribute(center, 'category') as SagaMapCategory];
          return { ...data, color, zIndex: 1, size: Math.max(data.size, 1.5) };
        },
      });
      current.renderer = renderer;
      // A record of the author the book does not know has a dashed outline (R9.5): WebGL draws no dashes, a canvas above it does.
      renderer.createCanvasContext('materials', { style: { pointerEvents: 'none' } });
      renderer.resize(true);
      const outlineContext = renderer.getCanvases().materials!.getContext('2d');
      renderer.on('afterRender', () => {
        if (!outlineContext) return;
        const { width, height } = renderer.getDimensions();
        outlineContext.clearRect(0, 0, width, height);
        outlineContext.setLineDash([4, 3]);
        outlineContext.lineWidth = 2;
        current.graph.forEachNode((key, attributes) => {
          const shape = attributes.material ? renderer.getNodeDisplayData(key) : undefined;
          if (!shape || shape.hidden) return;
          const center = renderer.framedGraphToViewport(shape);
          outlineContext.strokeStyle = shape.color === palette.dim ? palette.dim : palette.categories[attributes.category as SagaMapCategory];
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
      setReady(true);
    }).catch(() => {
      if (!cancelled) onUnavailable();
    });
    return () => {
      cancelled = true;
      observer?.disconnect();
      current.layout?.dispose();
      current.layout = null;
      current.renderer?.kill();
      current.renderer = null;
    };
  }, [onUnavailable]);

  useEffect(() => {
    const current = state.current;
    const target = current.graph;
    const palette = current.palette;
    if (!palette) return;
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
    const labeled = new Set([...graph.nodes].sort((left, right) => right.count - left.count).slice(0, alwaysLabeled).map((node) => node.key));
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
        forceLabel: node.hero || node.material || labeled.has(node.key), fixed: seed.fixed,
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
  }, [graph, ready]);

  useEffect(() => {
    const { renderer } = state.current;
    if (!renderer) return;
    renderer.refresh({ skipIndexation: true });
    if (!selected || !state.current.graph.hasNode(selected)) return;
    const data = renderer.getNodeDisplayData(selected);
    if (data) void renderer.getCamera().animate({ x: data.x, y: data.y }, { duration: 400 });
  }, [selected]);

  return <div aria-hidden="true" className={styles.canvas} data-testid="saga-map-canvas" ref={container} />;
}
