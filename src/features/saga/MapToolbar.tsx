import { Brain, CircleHelp, EyeOff, MessageCircleQuestion, SlidersHorizontal } from 'lucide-react';
import { useEffect, useRef, useState, type MouseEvent } from 'react';
import { useSagaMapSearchQuery, type SagaApiClient, type SagaMap, type SourceView } from '../../entities/saga/api';
import { toHref, type RouteNavigate } from '../../shared/navigation/app-route';
import { Button, ButtonLink } from '../../ui/controls/Button';
import { SearchField } from '../../ui/controls/SearchField';
import { MoreMenu, type MoreMenuItem } from '../../ui/overlays/MoreMenu';
import { Sheet } from '../../ui/overlays/Sheet';
import { foundLine, mapCategories, searchNodes, type MapFilter } from './map-model';
import styles from './SagaMap.module.css';

const shownResults = 8;
const searchDelay = 250;

interface MapSearchProps {
  api: SagaApiClient;
  map: SagaMap;
  projectId: string;
  sourceView: SourceView;
  /** An editor of the saga: an empty search offers to ask the chat. */
  canEdit: boolean;
  onSelect: (key: string) => void;
  /** A new chat with the question in the field, not sent (MP3.3). */
  onAsk: (question: string) => void;
  /** A name put into the search from outside, «Найти похожее» of a lost card (MP3.4); a new id puts it again. */
  seed: { id: number; text: string } | null;
  /**
   * A phone: the field shares the one row with the heading «Карта», «Фильтры» and «⋯», and its hint is the short «Найти»; its
   * name stays «Найти на карте» (specs/2026-11-11-ux2-polish.md PL2.2).
   */
  phone?: boolean;
}

/**
 * «Найти на карте» (specs/2026-11-04-saga-map.md, R3.3): the nodes by any of their names; when they are few, the
 * server adds the entries that are not on the map (R10.6). A node found says by which other name and where it first
 * appears; nothing found leads to the chat (specs/2026-11-11-ux8-map.md MP3.3).
 */
function MapSearch({ api, canEdit, map, onAsk, onSelect, phone = false, projectId, seed, sourceView }: MapSearchProps) {
  const [text, setText] = useState('');
  const [settled, setSettled] = useState('');
  const [open, setOpen] = useState(false);
  const field = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const timer = window.setTimeout(() => setSettled(text.trim()), searchDelay);
    return () => window.clearTimeout(timer);
  }, [text]);
  useEffect(() => {
    if (!seed) return;
    setText(seed.text);
    setOpen(true);
    field.current?.focus();
  }, [seed]);
  const found = searchNodes(map, text, shownResults);
  const wantsServer = settled.length >= 2 && settled === text.trim() && found.length < shownResults;
  const server = useSagaMapSearchQuery(api, projectId, { sourceView, q: settled, limit: shownResults }, wantsServer);
  const onMap = new Set(map.nodes.map((node) => node.key));
  const elsewhere = wantsServer
    ? (server.data?.entries ?? []).filter((entry) => !onMap.has(entry.key)).slice(0, shownResults - found.length)
    : [];
  const pick = (key: string) => {
    setText('');
    onSelect(key);
  };
  const wanted = text.trim();
  const empty = wanted.length > 0 && found.length === 0 && elsewhere.length === 0 && !server.isFetching;
  return (
    <div className={styles.search} onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false); }} onFocus={() => setOpen(true)}>
      <SearchField
        autoComplete="off"
        inputRef={field}
        label="Найти на карте"
        labelHidden
        loading={server.isFetching && wantsServer}
        onChange={(event) => setText(event.target.value)}
        onClear={() => setText('')}
        onKeyDown={(event) => {
          const first = found[0]?.key ?? elsewhere[0]?.key;
          if (event.key === 'Enter' && first) {
            // A narrow window opens the card in a drawer that takes the focus: the key press would land on its close button.
            event.preventDefault();
            pick(first);
          }
          if (event.key === 'Escape') setText('');
        }}
        placeholder={phone ? 'Найти' : 'Найти на карте'}
        value={text}
      />
      {open && wanted ? (
        <ul aria-label="Найдено" className={styles.results}>
          {found.map((node) => {
            const line = foundLine(map, node, wanted);
            return (
              <li key={node.key}>
                <button onClick={() => pick(node.key)} type="button">
                  <span aria-hidden="true" className={styles.dot} data-category={node.category} />
                  <span className={styles.resultText}>
                    <span className={styles.resultLine}><strong>{node.name}</strong><span className={styles.meta}>{node.type}</span></span>
                    {line ? <span className={styles.resultSecond} title={line}>{line}</span> : null}
                  </span>
                </button>
              </li>
            );
          })}
          {elsewhere.map((entry) => (
            <li key={entry.key}>
              <button onClick={() => pick(entry.key)} type="button">
                {entry.material
                  ? <span aria-hidden="true" className={styles.dot} data-material="" />
                  : <span aria-hidden="true" className={styles.dot} />}
                <span className={styles.resultText}>
                  <span className={styles.resultLine}>
                    <strong>{entry.name}</strong>
                    <span className={styles.meta}>{entry.material ? 'из ваших материалов' : `${entry.type ? `${entry.type} · ` : ''}нет на карте`}</span>
                  </span>
                </span>
              </button>
            </li>
          ))}
          {empty ? (
            <li className={styles.noResults}>
              <span>На карте такого нет.</span>
              {canEdit ? (
                <Button onClick={() => { setText(''); onAsk(`Что в саге известно про «${wanted}»?`); }} size="sm" variant="ghost">
                  <MessageCircleQuestion aria-hidden="true" size={15} />Спросить в чате про „{wanted}“
                </Button>
              ) : null}
            </li>
          ) : null}
        </ul>
      ) : null}
    </div>
  );
}

export interface MapToolbarProps extends Omit<MapSearchProps, 'phone'> {
  counts: Record<MapFilter, number>;
  hidden: ReadonlySet<MapFilter>;
  navigate: RouteNavigate;
  /**
   * A phone: the bar is one row — the heading, the search, «Фильтры» in a sheet and «⋯» (specs/2026-11-11-ux8-map.md MP1.4,
   * specs/2026-11-11-ux2-polish.md PL2.2).
   */
  phone: boolean;
  /** How many decisions the author made about the map: the list opens when there are some (R11.3). */
  removed: number;
  onToggle: (category: MapFilter) => void;
  onOpenRemoved: () => void;
  /** «Как читать карту»: the legend comes back (MP1.2). */
  onLegend: () => void;
}

/**
 * «Что помнит помощник» from the map: a link to the part «Ваши материалы» of its page, the records the map shows
 * (specs/2026-11-10-ux2-memory-page.md MP1.5); a plain click stays in the app, a modified one opens a tab.
 */
function memoryLink(navigate: RouteNavigate, projectId: string) {
  const query = { projectId, part: 'materials' };
  return {
    href: toHref('/memory', query),
    onClick: (event: MouseEvent<HTMLAnchorElement>) => {
      if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
      event.preventDefault();
      navigate('/memory', query);
    },
  };
}

/** The kinds the map shows, with their counts: buttons that hide and show a kind (R3.4). */
function MapFilters({ counts, hidden, onToggle }: Pick<MapToolbarProps, 'counts' | 'hidden' | 'onToggle'>) {
  return (
    <div aria-label="Что показывать" className={styles.filters} role="group">
      {mapCategories.map(({ category, label }) => (
        <button aria-pressed={!hidden.has(category)} className={styles.filter} key={category} onClick={() => onToggle(category)} type="button">
          <span aria-hidden="true" className={styles.dot} data-category={category} />
          {label}
          <span className={styles.count}>{counts[category]}</span>
        </button>
      ))}
      {counts.material ? (
        <button aria-pressed={!hidden.has('material')} className={styles.filter} onClick={() => onToggle('material')} type="button">
          <span aria-hidden="true" className={styles.dot} data-material="" />
          Ещё не в книге
          <span className={styles.count}>{counts.material}</span>
        </button>
      ) : null}
    </div>
  );
}

/** «Фильтры» of a phone: the kinds in a sheet; the button says how many are hidden (MP1.4, К-08). */
function FiltersSheet(props: Pick<MapToolbarProps, 'counts' | 'hidden' | 'onToggle'>) {
  const [open, setOpen] = useState(false);
  const off = [...props.hidden].filter((filter) => filter !== 'material' || props.counts.material).length;
  return (
    <>
      <Button aria-haspopup="dialog" onClick={() => setOpen(true)} size="touch" variant="outline">
        <SlidersHorizontal aria-hidden="true" size={16} />Фильтры{off ? <span className={styles.count}> · {off}</span> : null}
      </Button>
      {open ? (
        <Sheet canDismiss className={styles.filtersSheet} label="Что показывать" onDismiss={() => setOpen(false)}>
          <h2 className={styles.sheetTitle}>Что показывать</h2>
          <MapFilters {...props} />
          <Button onClick={() => setOpen(false)} size="touch" variant="primary">Готово</Button>
        </Sheet>
      ) : null}
    </>
  );
}

/**
 * The panel of the map beside its heading: the search, the kinds with their counts, «Убранное с карты», «Что помнит
 * помощник» and «⋯» with «Как читать карту» (R3.3, R3.4, R6.3; specs/2026-11-11-ux8-map.md MP1.2, MP1.4). On a phone it is
 * one row with the heading.
 */
export function MapToolbar({ counts, hidden, navigate, onLegend, onOpenRemoved, onToggle, phone, removed, ...search }: MapToolbarProps) {
  const memory = memoryLink(navigate, search.projectId);
  const items: MoreMenuItem[] = [
    { label: 'Как читать карту', icon: CircleHelp, run: onLegend },
    ...(phone && removed > 0 ? [{ label: `Убранное с карты · ${removed}`, icon: EyeOff, run: onOpenRemoved }] : []),
    ...(phone ? [{ label: 'Что помнит помощник', icon: Brain, run: () => navigate('/memory', { projectId: search.projectId, part: 'materials' }) }] : []),
  ];
  return (
    <div aria-label="Панель карты" className={styles.toolbar} data-phone={phone || undefined} role="group">
      <MapSearch {...search} phone={phone} />
      {phone ? <FiltersSheet counts={counts} hidden={hidden} onToggle={onToggle} /> : <MapFilters counts={counts} hidden={hidden} onToggle={onToggle} />}
      <div className={styles.memory}>
        {phone ? null : <>
          {removed > 0 ? (
            <Button onClick={onOpenRemoved} size="sm" variant="ghost">
              <EyeOff aria-hidden="true" size={15} />Убранное с карты<span className={styles.count}>{removed}</span>
            </Button>
          ) : null}
          <ButtonLink {...memory} size="sm" variant="ghost"><Brain aria-hidden="true" size={15} />Что помнит помощник</ButtonLink>
        </>}
        <MoreMenu items={items} label="Ещё" size={phone ? 'touch' : 'sm'} />
      </div>
    </div>
  );
}
