import { X } from 'lucide-react';
import { useState } from 'react';
import { IconButton } from '../../ui/controls/IconButton';
import styles from './SagaMap.module.css';

/** Whether this browser hid the legend of the map: only the mark lives here (specs/2026-11-11-ux8-map.md MP1.2). */
const storageKey = 'canonkeeper.saga-map.legend';

function storedHidden() {
  try {
    return window.localStorage.getItem(storageKey) === 'hidden';
  } catch {
    return false;
  }
}

function store(hidden: boolean) {
  try {
    if (hidden) window.localStorage.setItem(storageKey, 'hidden');
    else window.localStorage.removeItem(storageKey);
  } catch {
    // Without storage the choice lasts until the page reloads.
  }
}

/** The legend shown or hidden, as the browser remembers it; «Как читать карту» brings it back. */
export function useMapLegend() {
  const [hidden, setHidden] = useState(storedHidden);
  const change = (next: boolean) => {
    store(next);
    setHidden(next);
  };
  return { shown: !hidden, hide: () => change(true), show: () => change(false) };
}

/** One line under the bar of the map: what a circle and a line mean and what a press does (MP1.2). */
export function MapLegend({ onHide }: { onHide: () => void }) {
  return (
    <div className={styles.legend}>
      <p>Кружок — герой, место или предмет: чем чаще в книге, тем больше. Линия — связь: чем толще, тем больше общего. Нажмите на кружок — кто это и с кем связан</p>
      <IconButton aria-label="Скрыть подсказку" onClick={onHide} size="sm" title="Скрыть подсказку"><X aria-hidden="true" size={16} /></IconButton>
    </div>
  );
}
