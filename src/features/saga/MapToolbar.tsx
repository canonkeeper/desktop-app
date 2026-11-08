import { Brain, EyeOff, FileText } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useSagaMapSearchQuery, type SagaApiClient, type SagaMap, type SourceView } from '../../entities/saga/api';
import { Button } from '../../ui/controls/Button';
import { SearchField } from '../../ui/controls/SearchField';
import { mapCategories, searchNodes, type MapFilter } from './map-model';
import styles from './SagaMap.module.css';

const shownResults = 8;
const searchDelay = 250;

interface MapSearchProps {
  api: SagaApiClient;
  map: SagaMap;
  projectId: string;
  sourceView: SourceView;
  onSelect: (key: string) => void;
}

/**
 * «Найти на карте» (specs/2026-11-04-saga-map.md, R3.3): the nodes by any of their names; when they are few, the
 * server adds the entries that are not on the map (R10.6).
 */
function MapSearch({ api, map, onSelect, projectId, sourceView }: MapSearchProps) {
  const [text, setText] = useState('');
  const [settled, setSettled] = useState('');
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const timer = window.setTimeout(() => setSettled(text.trim()), searchDelay);
    return () => window.clearTimeout(timer);
  }, [text]);
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
  const empty = text.trim().length > 0 && found.length === 0 && elsewhere.length === 0 && !server.isFetching;
  return (
    <div className={styles.search} onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false); }} onFocus={() => setOpen(true)}>
      <SearchField
        autoComplete="off"
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
        placeholder="Найти на карте"
        value={text}
      />
      {open && text.trim() ? (
        <ul aria-label="Найдено" className={styles.results}>
          {found.map((node) => (
            <li key={node.key}>
              <button onClick={() => pick(node.key)} type="button">
                <span aria-hidden="true" className={styles.dot} data-category={node.category} />
                <strong>{node.name}</strong>
                <span className={styles.meta}>{node.type}</span>
              </button>
            </li>
          ))}
          {elsewhere.map((entry) => (
            <li key={entry.key}>
              <button onClick={() => pick(entry.key)} type="button">
                {entry.material
                  ? <span aria-hidden="true" className={styles.dot} data-material="" />
                  : <span aria-hidden="true" className={styles.dot} />}
                <strong>{entry.name}</strong>
                <span className={styles.meta}>{entry.material ? 'из ваших материалов' : `${entry.type ? `${entry.type} · ` : ''}нет на карте`}</span>
              </button>
            </li>
          ))}
          {empty ? <li className={styles.noResults}>Ничего не нашлось</li> : null}
        </ul>
      ) : null}
    </div>
  );
}

export interface MapToolbarProps extends MapSearchProps {
  counts: Record<MapFilter, number>;
  hidden: ReadonlySet<MapFilter>;
  /** How many decisions the author made about the map: the list opens when there are some (R11.3). */
  removed: number;
  onToggle: (category: MapFilter) => void;
  onOpenMaterials: () => void;
  onOpenRemoved: () => void;
  onOpenMemory: () => void;
}

/** The bar above the map: the search, the four categories with their counts and «Что помнит помощник» (R3.3, R3.4, R6.3). */
export function MapToolbar({ counts, hidden, onOpenMaterials, onOpenMemory, onOpenRemoved, onToggle, removed, ...search }: MapToolbarProps) {
  return (
    <div className={styles.toolbar}>
      <MapSearch {...search} />
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
      <div className={styles.memory}>
        {removed > 0 ? (
          <Button onClick={onOpenRemoved} size="sm" variant="ghost">
            <EyeOff aria-hidden="true" size={15} />Убранное<span className={styles.count}>{removed}</span>
          </Button>
        ) : null}
        <Button onClick={onOpenMaterials} size="sm" variant="ghost">
          <FileText aria-hidden="true" size={15} />Материалы автора
        </Button>
        <Button onClick={onOpenMemory} size="sm" variant="ghost">
          <Brain aria-hidden="true" size={15} />Что помнит помощник
        </Button>
      </div>
    </div>
  );
}
