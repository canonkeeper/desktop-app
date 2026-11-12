import { useSagaMapHiddenQuery, type SagaApiClient, type SourceView } from '../../entities/saga/api';
import { Button } from '../../ui/controls/Button';
import { LinkAction } from '../../ui/controls/LinkAction';
import { hiddenLabel, type MapDecisions } from './use-map-decisions';
import { LoadError, LoadingRows } from './SagaParts';
import styles from './SagaMap.module.css';

export interface HiddenPanelProps {
  api: SagaApiClient;
  canEdit: boolean;
  decisions: MapDecisions;
  projectId: string;
  sourceView: SourceView;
  /** Opens the card of a name. */
  onOpen: (key: string) => void;
}

/** «Убранное с карты» (specs/2026-11-04-saga-map.md, R11.3): what the author took off the map, and the way back by one press. */
export function HiddenPanel({ api, canEdit, decisions, onOpen, projectId, sourceView }: HiddenPanelProps) {
  const list = useSagaMapHiddenQuery(api, projectId, sourceView);
  return (
    <section aria-label="Убранное с карты" className={styles.hiddenPanel}>
      <h2>Убранное с карты</h2>
      <p className={styles.meta}>Только на карте: книга и ответы помощника не меняются.</p>
      {list.isError ? <LoadError error={list.error} onRetry={() => void list.refetch()} title="Не открылся список убранного" /> : null}
      {list.isPending ? <LoadingRows /> : null}
      {list.data?.items.length === 0 ? <p className={styles.meta}>С карты ничего не убрано.</p> : null}
      {list.data?.items.length ? (
        <ul aria-label="Что убрано" className={styles.hiddenList}>
          {list.data.items.map((item) => (
            <li key={item.id}>
              <LinkAction className={styles.linkName} onClick={() => onOpen(item.nodeKey)}>{hiddenLabel(item)}</LinkAction>
              <span className={styles.meta}>{item.otherKey ? 'связь убрана' : 'убрано с карты'}</span>
              {canEdit ? <Button aria-label={`Вернуть: ${hiddenLabel(item)}`} disabled={decisions.pending} onClick={() => decisions.restore(item.id)} size="sm" variant="outline">Вернуть</Button> : null}
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}
