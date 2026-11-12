import { Minus, Plus } from 'lucide-react';
import { useState, type MutableRefObject } from 'react';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { Confirm } from '../../ui/overlays/Confirm';
import type { MapView } from './map-view';
import styles from './SagaMap.module.css';

/** «+», «−» and «Показать всё» in the corner of the graph (specs/2026-11-11-ux8-map.md MP1.1); a target of 44 px on a phone. */
export function MapViewControl({ phone, view }: { phone: boolean; view: MutableRefObject<MapView | null> }) {
  const size = phone ? 'touch' : 'sm';
  return (
    <div aria-label="Вид карты" className={styles.viewControl} role="group">
      <IconButton aria-label="Приблизить" onClick={() => view.current?.zoomIn()} size={size} title="Приблизить (+)"><Plus aria-hidden="true" size={16} /></IconButton>
      <IconButton aria-label="Отдалить" onClick={() => view.current?.zoomOut()} size={size} title="Отдалить (−)"><Minus aria-hidden="true" size={16} /></IconButton>
      <Button onClick={() => view.current?.showAll()} size={size} title="Исходный вид и масштаб (0)" variant="ghost">Показать всё</Button>
    </div>
  );
}

/** More names than this make the map slower: «Все» asks first (MP1.5). */
export const allWithoutQuestion = 300;

/**
 * Which of the names the map shows (MP1.5, К-09): «Показаны 60 самых частых из 420 · Ещё 60 · Все» and «Редкое имя — через
 * поиск»; «Все» of a big saga asks with the one confirmation.
 */
export function MapCount({ onAll, onMore, shown, step, total }: { onAll: () => void; onMore: () => void; shown: number; step: number; total: number }) {
  const [asking, setAsking] = useState(false);
  const all = () => {
    if (total > allWithoutQuestion) setAsking(true);
    else onAll();
  };
  return (
    <div className={styles.overlayBottom}>
      {shown < total ? <>
        <span>Показаны {shown} самых частых из {total}</span>
        <Button onClick={onMore} size="sm" variant="ghost">Ещё {Math.min(step, total - shown)}</Button>
        <Button onClick={all} size="sm" variant="ghost">Все</Button>
        <span className={styles.countHint}>Редкое имя — через поиск</span>
      </> : <span>Показаны все: {total}</span>}
      {asking ? (
        <Confirm action="Показать все" consent="no" onCancel={() => setAsking(false)} onConfirm={() => { setAsking(false); onAll(); }} title={`Показать все ${total}?`}>
          Карта станет медленнее.
        </Confirm>
      ) : null}
    </div>
  );
}
