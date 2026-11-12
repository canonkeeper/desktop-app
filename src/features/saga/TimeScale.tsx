import { ChevronLeft, ChevronRight } from 'lucide-react';
import { useEffect, useId, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent } from 'react';
import type { SagaChapterRef } from '../../entities/saga/api';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { useTouchTargets } from '../../ui/use-narrow-screen';
import styles from './TimeScale.module.css';

interface TimeScaleProps {
  chapters: readonly SagaChapterRef[];
  /** The chapter index the scale stands at: the last one for the whole saga. */
  value: number;
  /** The scale moves: the map follows at once (R4.1). */
  onMove: (index: number) => void;
}

/** The first chapter of every book, in reading order: the marks of the scale (MP2.3). */
export function bookStarts(chapters: readonly SagaChapterRef[]) {
  return chapters.flatMap((chapter, index) => (index === 0 || chapters[index - 1]!.bookId !== chapter.bookId
    ? [{ index, order: chapter.bookOrder, title: chapter.bookTitle }]
    : []));
}

/** The width of the track, for the marks that have room for their number; 0 before it is measured. */
function useWidth() {
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const element = ref.current;
    if (!element || typeof ResizeObserver !== 'function') return undefined;
    const observer = new ResizeObserver(() => setWidth(element.clientWidth));
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  return { ref, width };
}

/** The room a number of a book needs beside the one before it: its target, 44 px under a finger (SC2.1). */
const markRoom = { fine: 22, touch: 44 } as const;

/**
 * «Сага на момент главы» (specs/2026-11-04-saga-map.md UC4; specs/2026-11-11-ux8-map.md MP2): a scale over every chapter of
 * the text version with the marks of the books, a chapter back and forward, «Вся сага» at the end. The chapter it stands at
 * reads as its place, «Книга 2, гл. 12 „Белый порт“»: the server names it, the key is never shown (specs/2026-11-10-ux1-names.md
 * N4.2). The arrows move by a chapter, PageUp and PageDown by a book, Home and End to the start and to the whole saga.
 */
export function TimeScale({ chapters, onMove, value }: TimeScaleProps) {
  const nameId = useId();
  const track = useWidth();
  const room = useTouchTargets() ? markRoom.touch : markRoom.fine;
  const starts = useMemo(() => bookStarts(chapters), [chapters]);
  const last = chapters.length - 1;
  if (last < 0) return null;
  const chapter = chapters[value]!;
  const whole = value >= last;
  const place = whole ? 'Вся сага' : chapter.label;
  const at = (index: number) => (last ? index / last : 0);
  const byBook = (event: KeyboardEvent<HTMLInputElement>) => {
    const target = event.key === 'PageDown' ? starts.find((start) => start.index > value)?.index ?? last
      : event.key === 'PageUp' ? [...starts].reverse().find((start) => start.index < value)?.index ?? 0
        : event.key === 'Home' ? 0
          : event.key === 'End' ? last : null;
    if (target === null) return;
    event.preventDefault();
    onMove(target);
  };
  let lastLabeled = -Infinity;
  const marks = starts.map((start) => {
    const x = at(start.index) * track.width;
    const labeled = track.width === 0 || x - lastLabeled >= room;
    if (labeled) lastLabeled = x;
    return { ...start, labeled };
  });
  return (
    <div aria-labelledby={nameId} className={styles.scale} role="group" style={{ '--scale-fill': `${at(value) * 100}%` } as CSSProperties}>
      <strong className={styles.scaleName} id={nameId}>Сага на момент главы</strong>
      <span className={styles.scaleHint}>Что известно к этой главе — без того, что случилось позже</span>
      <span aria-hidden="true" className={styles.scaleBreak} />
      <IconButton aria-label="На главу назад" className={styles.scaleBack} disabled={value <= 0} onClick={() => onMove(value - 1)} size="sm">
        <ChevronLeft aria-hidden="true" size={16} />
      </IconButton>
      <div className={styles.scaleTrack} ref={track.ref}>
        <input aria-label="Сага на момент главы" aria-valuetext={place} className={styles.slider} max={last} min={0}
          onChange={(event) => onMove(Number(event.target.value))} onKeyDown={byBook} step={1} type="range" value={value} />
        {starts.length > 1 ? (
          <ol aria-label="Начала книг" className={styles.marks}>
            {marks.map((mark) => (
              <li key={mark.index} style={{ '--mark-at': at(mark.index) } as CSSProperties}>
                {mark.labeled ? (
                  <button aria-label={`К началу книги ${mark.order} «${mark.title}»`} className={styles.mark} onClick={() => onMove(mark.index)}
                    title={`Книга ${mark.order} «${mark.title}»`} type="button">{mark.order}</button>
                ) : <span aria-hidden="true" className={styles.markLine} />}
              </li>
            ))}
          </ol>
        ) : null}
      </div>
      <IconButton aria-label="На главу вперёд" className={styles.scaleForward} disabled={whole} onClick={() => onMove(value + 1)} size="sm">
        <ChevronRight aria-hidden="true" size={16} />
      </IconButton>
      <p className={styles.scaleValue} title={whole ? undefined : chapter.label}>{place}</p>
      {whole ? null : <Button className={styles.scaleAll} onClick={() => onMove(last)} size="sm" variant="ghost">Вся сага</Button>}
    </div>
  );
}
