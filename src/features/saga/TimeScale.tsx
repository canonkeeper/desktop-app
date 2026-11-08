import { ChevronLeft, ChevronRight } from 'lucide-react';
import type { SagaChapterRef } from '../../entities/saga/api';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { chapterLabel } from './map-model';
import styles from './SagaMap.module.css';

interface TimeScaleProps {
  chapters: readonly SagaChapterRef[];
  /** The chapter index the scale stands at: the last one for the whole saga. */
  value: number;
  /** The scale moves: the map follows at once (R4.1). */
  onMove: (index: number) => void;
}

/**
 * «К главе N» (specs/2026-11-04-saga-map.md, UC4): a scale over every chapter of the text version, a chapter back
 * and forward, «Вся сага» at the end.
 */
export function TimeScale({ chapters, onMove, value }: TimeScaleProps) {
  const last = chapters.length - 1;
  if (last < 0) return null;
  const chapter = chapters[value]!;
  const whole = value >= last;
  const label = whole ? 'Вся сага' : `К главе ${chapterLabel(chapter).replace(/^гл\. /u, '')}`;
  return (
    <div className={styles.scale}>
      <IconButton aria-label="На главу назад" disabled={value <= 0} onClick={() => onMove(value - 1)} size="sm">
        <ChevronLeft aria-hidden="true" size={16} />
      </IconButton>
      <input aria-label="К главе" aria-valuetext={whole ? 'Вся сага' : `${chapterLabel(chapter)} — ${chapter.title}`} className={styles.slider}
        max={last} min={0} onChange={(event) => onMove(Number(event.target.value))} step={1} type="range" value={value} />
      <IconButton aria-label="На главу вперёд" disabled={whole} onClick={() => onMove(value + 1)} size="sm">
        <ChevronRight aria-hidden="true" size={16} />
      </IconButton>
      <p className={styles.scaleLabel} title={whole ? undefined : chapter.title}>
        <strong>{label}</strong>
        {whole ? null : <span>{chapter.title}</span>}
      </p>
      {whole ? null : <Button className={styles.scaleAll} onClick={() => onMove(last)} size="sm" variant="ghost">Вся сага</Button>}
    </div>
  );
}
