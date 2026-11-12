import { useEffect, useId, useImperativeHandle, useLayoutEffect, useRef, useState, type KeyboardEvent, type Ref } from 'react';
import { formatCount, pluralize } from './copy';
import { Button } from './controls/Button';
import { SkeletonArea } from './feedback/Skeleton';
import styles from './NoteCatalog.module.css';

/**
 * A row of the catalog of notes (specs/2026-11-11-ux7-notes.md NT1.3): the line of the note — its thought or the beginning
 * of its cited words — and, under it, its place and the date.
 */
export interface NoteCatalogItem {
  key: string;
  line: string;
  place: string;
  updatedAt: string;
}
export interface NoteCatalogHandle { focus: (key: string) => void }

/** «19 сент., 18:40» — the day and the time of the last change of a note. */
function noteDate(value: string) {
  const date = new Date(value);
  return `${date.toLocaleDateString('ru-RU', { day: 'numeric', month: 'short' })}, ${date.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' })}`;
}

export function NoteCatalog({ items, totalCount, selectedKey, onSelect, hasMore = false, loadingMore = false, loadError = false, onLoadMore, ref }: {
  items: NoteCatalogItem[];
  totalCount: number;
  selectedKey?: string;
  onSelect: (key: string) => void;
  hasMore?: boolean;
  loadingMore?: boolean;
  loadError?: boolean;
  onLoadMore?: () => void;
  ref?: Ref<NoteCatalogHandle>;
}) {
  const viewportRef = useRef<HTMLDivElement>(null);
  const anchorRef = useRef<{ key?: string; index: number; offset: number }>({ index: 0, offset: 0 });
  const [viewport, setViewport] = useState({ width: 1000, height: 360 });
  const [scrollTop, setScrollTop] = useState(0);
  const [focusKey, setFocusKey] = useState(items[0]?.key);
  const [focusWithin, setFocusWithin] = useState(false);
  const [pendingFocus, setPendingFocus] = useState<string | null>(null);
  const prefix = useId();
  const compact = viewport.width < 760;
  const rowHeight = compact ? 88 : 72;
  const keys = items.map((item) => item.key).join(',');
  const start = Math.max(0, Math.floor(scrollTop / rowHeight) - 6);
  const end = Math.min(items.length, Math.ceil((scrollTop + viewport.height) / rowHeight) + 6);
  const activeIndex = Math.max(0, items.findIndex((item) => item.key === focusKey));
  const visible = new Set(Array.from({ length: Math.max(0, end - start) }, (_, offset) => start + offset));
  if (focusWithin && items[activeIndex]) visible.add(activeIndex);
  const pendingIndex = items.findIndex((item) => item.key === pendingFocus);
  if (pendingIndex >= 0) visible.add(pendingIndex);
  const tabIndex = focusWithin || visible.has(activeIndex) ? activeIndex : Math.min(items.length - 1, Math.ceil(scrollTop / rowHeight));

  useLayoutEffect(() => {
    const element = viewportRef.current!;
    const measure = (width: number, height: number) => setViewport({ width, height });
    const rect = element.getBoundingClientRect();
    if (rect.width && rect.height) measure(rect.width, rect.height);
    const observer = new ResizeObserver(([entry]) => {
      if (entry?.contentRect.width && entry.contentRect.height) measure(entry.contentRect.width, entry.contentRect.height);
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  useLayoutEffect(() => {
    const anchor = anchorRef.current;
    const index = items.findIndex((item) => item.key === anchor.key);
    const top = Math.max(0, index < 0 ? Math.min(anchor.index, items.length - 1) : index) * rowHeight + Math.min(anchor.offset, rowHeight - 1);
    viewportRef.current!.scrollTop = top;
    setScrollTop(top);
    // The anchor is presentation state, never a copy of server note data.
  }, [keys, rowHeight]);

  useEffect(() => {
    if (hasMore && !loadingMore && !loadError && end >= items.length - 6) onLoadMore?.();
  }, [end, hasMore, items.length, loadError, loadingMore, onLoadMore]);

  useLayoutEffect(() => {
    if (!pendingFocus) return;
    const index = items.findIndex((item) => item.key === pendingFocus);
    document.getElementById(`${prefix}-${index}`)?.focus({ preventScroll: true });
    setPendingFocus(null);
  }, [items, pendingFocus, prefix]);

  function focus(index: number) {
    const item = items[index];
    const element = viewportRef.current;
    if (!item || !element) return;
    let top = element.scrollTop;
    if (index * rowHeight < top) top = index * rowHeight;
    else if ((index + 1) * rowHeight > top + viewport.height) top = (index + 1) * rowHeight - viewport.height;
    element.scrollTop = Math.max(0, top);
    rememberScroll();
    setFocusKey(item.key);
    setPendingFocus(item.key);
  }
  useImperativeHandle(ref, () => ({ focus: (key) => focus(items.findIndex((item) => item.key === key)) }));

  function rememberScroll() {
    const top = viewportRef.current!.scrollTop;
    const index = Math.floor(top / rowHeight);
    anchorRef.current = { key: items[index]?.key, index, offset: top % rowHeight };
    setScrollTop(top);
  }
  function move(event: KeyboardEvent, index: number) {
    const page = Math.max(1, Math.floor(viewport.height / rowHeight));
    const next = { ArrowDown: index + 1, ArrowUp: index - 1, Home: 0, End: items.length - 1, PageDown: index + page, PageUp: index - page }[event.key];
    if (next === undefined) return;
    event.preventDefault();
    focus(Math.max(0, Math.min(items.length - 1, next)));
  }

  return <div className={`${styles.catalog} ${compact ? styles.compact : ''}`}>
    <div className={styles.viewport} onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) setFocusWithin(false); }} onFocus={() => setFocusWithin(true)} onScroll={rememberScroll} ref={viewportRef}>
      <div aria-label="Список заметок" className={styles.window} role="listbox" style={{ height: items.length * rowHeight }}>
        {[...visible].sort((a, b) => a - b).map((index) => {
          const item = items[index];
          if (!item) return null;
          return <Button aria-posinset={index + 1} aria-selected={selectedKey === item.key} aria-setsize={totalCount} className={styles.row} data-note-key={item.key} id={`${prefix}-${index}`} key={item.key} onClick={() => onSelect(item.key)} onFocus={() => setFocusKey(item.key)} onKeyDown={(event) => move(event, index)} role="option" style={{ top: index * rowHeight, height: rowHeight }} tabIndex={index === tabIndex ? 0 : -1} variant="ghost">
            <span className={styles.summary}><span className={styles.title}>{item.line}</span>
              <span className={styles.meta}>{item.place} · <time dateTime={item.updatedAt}>{noteDate(item.updatedAt)}</time></span></span>
          </Button>;
        })}
      </div>
      <div className={styles.tail}>
        {loadError ? <><span role="status">Не удалось загрузить ещё заметки</span><Button onClick={onLoadMore} size="touch" variant="outline">Повторить</Button></>
          : loadingMore ? <SkeletonArea count={2} shape="rows" style={{ width: '100%' }} />
            : !hasMore ? <span role="status">{pluralize(totalCount, 'Показана', 'Показаны все', 'Показаны все')} {formatCount(totalCount, 'заметка', 'заметки', 'заметок')}</span> : null}
      </div>
    </div>
  </div>;
}
