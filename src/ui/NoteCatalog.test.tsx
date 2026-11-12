import { act, fireEvent, render, screen } from '@testing-library/react';
import { createRef } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { NoteCatalog, type NoteCatalogHandle, type NoteCatalogItem } from './NoteCatalog';

const items: NoteCatalogItem[] = Array.from({ length: 527 }, (_, index) => ({
  key: `personal:${index}`, line: `Заметка ${index + 1}`, place: 'Своя мысль', updatedAt: '2026-09-19T18:40:00Z',
}));
let resize: (width: number, height: number) => void;
beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class {
    constructor(callback: ResizeObserverCallback) { resize = (width, height) => callback([{ contentRect: { width, height } } as ResizeObserverEntry], this as unknown as ResizeObserver); }
    observe() { resize(1000, 360); }
    disconnect() {}
  });
});
afterEach(() => vi.unstubAllGlobals());

describe('NoteCatalog virtual scrolling', () => {
  it('keeps the focused row mounted across responsive row-height changes', () => {
    const ref = createRef<NoteCatalogHandle>();
    render(<NoteCatalog items={items} totalCount={527} onSelect={vi.fn()} ref={ref} />);
    const viewport = screen.getByRole('listbox').parentElement!;
    fireEvent.scroll(viewport, { target: { scrollTop: 7200 } });
    act(() => ref.current!.focus('personal:101'));
    const row = screen.getByRole('option', { name: /Заметка 102\b/ });
    expect(document.activeElement).toBe(row);
    act(() => resize(600, 360));
    expect(document.activeElement).toBe(row);
    act(() => resize(1000, 360));
    expect(document.activeElement).toBe(row);
  });

  it('keeps a bounded window for hundreds of notes and preserves its anchor when width changes', () => {
    render(<NoteCatalog items={items} totalCount={527} onSelect={vi.fn()} />);
    const viewport = screen.getByRole('listbox', { name: 'Список заметок' }).parentElement!;
    expect(screen.getAllByRole('option').length).toBeLessThan(20);
    fireEvent.scroll(viewport, { target: { scrollTop: 7200 } });
    expect(screen.getByRole('option', { name: /Заметка 101\b/ })).toBeTruthy();
    expect(screen.queryByRole('option', { name: /^Заметка 1\b/ })).toBeNull();
    expect(screen.getAllByRole('option').filter((row) => row.tabIndex === 0)).toHaveLength(1);
    act(() => resize(600, 360));
    expect(viewport.scrollTop).toBe(8800);
    expect(screen.getAllByRole('option').length).toBeLessThan(20);
  });
  it('loads at the edge, stops on error, and exposes only an explicit retry', () => {
    const load = vi.fn();
    const props = { items: items.slice(0, 30), totalCount: 527, hasMore: true, onLoadMore: load, onSelect: vi.fn() };
    const { rerender } = render(<NoteCatalog {...props} />);
    expect(load).not.toHaveBeenCalled();
    fireEvent.scroll(screen.getByRole('listbox').parentElement!, { target: { scrollTop: 1800 } });
    expect(load).toHaveBeenCalledTimes(1);
    rerender(<NoteCatalog {...props} loadError />);
    expect(load).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
    expect(load).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole('button', { name: 'Показать ещё' })).toBeNull();
  });
});

describe('NoteCatalog count (R5.3)', () => {
  it.each([
    [1, 'Показана 1 заметка'],
    [2, 'Показаны все 2 заметки'],
    [5, 'Показаны все 5 заметок'],
    [11, 'Показаны все 11 заметок'],
    [21, 'Показана 21 заметка'],
  ])('declines all %i shown notes', (count, text) => {
    render(<NoteCatalog items={items.slice(0, count)} totalCount={count} onSelect={vi.fn()} />);
    expect(screen.getByRole('status').textContent).toBe(text);
  });
});
