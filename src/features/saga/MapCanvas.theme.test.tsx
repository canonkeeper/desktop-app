import { act, render, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import '../../styles/tokens.css';
import { paintSiteTheme, useSiteThemeStore } from '../../shared/theme/site-theme';
import type { ViewGraph } from './map-model';

/** The renderer of the canvas: what the map gave it and asked of it. */
const sigma = vi.hoisted(() => ({
  settings: {} as Record<string, unknown>,
  graph: null as null | { getNodeAttribute(node: string, name: string): unknown; getEdgeAttribute(edge: string, name: string): unknown },
  refreshes: 0,
}));

vi.mock('sigma', () => ({
  default: class {
    constructor(graph: typeof sigma.graph, _element: HTMLElement, settings: Record<string, unknown>) {
      sigma.graph = graph;
      sigma.settings = { ...settings };
    }
    createCanvasContext() {}
    resize() {}
    getCanvases() { return { materials: { getContext: () => null }, labels: { getContext: () => null } }; }
    on() {}
    setCustomBBox() {}
    refresh() { sigma.refreshes += 1; }
    scheduleRefresh() {}
    kill() {}
    setSetting(name: string, value: unknown) { sigma.settings[name] = value; }
  },
}));

vi.mock('./map-layout', () => ({ createMapLayout: () => ({ run() {}, dispose() {} }) }));

const { MapCanvas } = await import('./MapCanvas');

const graph = {
  nodes: [
    { key: 'mara', name: 'Мара', category: 'character', count: 4, hero: true, material: false, dim: false },
    { key: 'port', name: 'Белый порт', category: 'place', count: 2, hero: false, material: false, dim: false },
  ],
  links: [{ key: 'mara-port', source: 'mara', target: 'port', together: 2, nearby: 0, weak: false }],
} as unknown as ViewGraph;

afterEach(() => {
  act(() => useSiteThemeStore.setState({ choice: 'light', theme: 'light' }));
  document.documentElement.removeAttribute('data-theme');
});

// specs/2026-11-12-ux11-dark-core.md TH3.2: the canvas draws in the colours of the theme and draws again when it changes.
describe('the canvas of the map', () => {
  it('takes the dark colours of the map when the site turns dark, without a new canvas', async () => {
    // jsdom does not pass custom properties down: the canvas reads what <html> holds, as a browser gives it by inheritance.
    const computed = window.getComputedStyle.bind(window);
    vi.spyOn(window, 'getComputedStyle').mockImplementation(() => computed(document.documentElement));
    paintSiteTheme('light');
    render(<MapCanvas graph={graph} hoverable onFocus={() => undefined} onSelect={() => undefined} onUnavailable={() => undefined} selected={null} />);
    await waitFor(() => expect(sigma.graph?.getNodeAttribute('mara', 'color')).toBe('#6d4fc4'));
    expect(sigma.settings.labelColor).toEqual({ color: '#18181b' });
    expect(sigma.graph?.getEdgeAttribute('mara-port', 'color')).toBe('#a1a1aa');
    const first = sigma.graph;
    const before = sigma.refreshes;
    act(() => {
      paintSiteTheme('dark');
      useSiteThemeStore.setState({ choice: 'dark', theme: 'dark' });
    });
    expect(sigma.graph).toBe(first);
    expect(sigma.graph?.getNodeAttribute('mara', 'color')).toBe('#a08ff0');
    expect(sigma.graph?.getNodeAttribute('port', 'color')).toBe('#3cc49a');
    expect(sigma.graph?.getEdgeAttribute('mara-port', 'color')).toBe('#6b6b74');
    expect(sigma.settings.labelColor).toEqual({ color: '#f4f4f2' });
    expect(sigma.refreshes).toBeGreaterThan(before);
    expect(typeof sigma.settings.defaultDrawNodeHover).toBe('function');
  });
});
