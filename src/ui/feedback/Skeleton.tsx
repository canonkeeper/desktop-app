import { useEffect, useState, type CSSProperties, type HTMLAttributes } from 'react';
import styles from './feedback.module.css';
import area from './Skeleton.module.css';

export type SkeletonProps = HTMLAttributes<HTMLDivElement>;

/** One shape of the content while it loads: a line, a row, a tile. It says nothing to the reader of the screen. */
export function Skeleton({ className, ...props }: SkeletonProps) {
  return <div {...props} aria-hidden="true" className={`${styles.skeleton}${className ? ` ${className}` : ''}`} />;
}

/** How long a load may take before its form shows (PS3.1): a quick answer shows the content at once, with no flash. */
export const skeletonDelayMs = 200;

/** The form of what comes: rows of a list, tiles, cards, a page with its heading and cards, lines of text, one block. */
export type SkeletonShape = 'rows' | 'tiles' | 'cards' | 'page' | 'lines' | 'block';

export interface SkeletonAreaProps {
  shape?: SkeletonShape;
  /** How many rows, tiles or lines; the cards and the page have their own. */
  count?: number;
  className?: string;
  style?: CSSProperties;
}

function pieces(shape: SkeletonShape, count: number) {
  switch (shape) {
    case 'page':
      return <>
        <Skeleton className={area.title} />
        <Skeleton className={area.line} />
        <Skeleton className={`${area.line} ${area.short}`} />
        <div className={area.cards}>{[0, 1, 2].map((index) => <Skeleton className={area.card} key={index} />)}</div>
      </>;
    case 'cards':
      return <div className={area.cards}>{[0, 1, 2].map((index) => <Skeleton className={area.card} key={index} />)}</div>;
    case 'tiles':
      return <div className={area.tiles}>{Array.from({ length: count }, (_, index) => <Skeleton className={area.tile} key={index} />)}</div>;
    case 'lines':
      return Array.from({ length: count }, (_, index) => <Skeleton className={`${area.line}${index === count - 1 ? ` ${area.short}` : ''}`} key={index} />);
    case 'rows':
      return Array.from({ length: count }, (_, index) => <Skeleton className={area.row} key={index} />);
    case 'block':
      return <Skeleton className={area.block} />;
  }
}

/**
 * A page or a large block while it loads (specs/2026-11-10-ux2-patterns-states.md PS3.1, П-8; master
 * `Pattern/Common/Skeleton`, board 15·09): the form of what will come in the place it will take, so the page does not
 * jump when it comes. The form shows only after 200 ms and does not flash before a quick answer; the block is announced as
 * busy, never by the words «Загружаем…».
 */
export function SkeletonArea({ className, count = 3, shape = 'rows', style }: SkeletonAreaProps) {
  const [shown, setShown] = useState(false);
  useEffect(() => {
    const timer = window.setTimeout(() => setShown(true), skeletonDelayMs);
    return () => window.clearTimeout(timer);
  }, []);
  return (
    <div aria-busy="true" className={`${area.area}${className ? ` ${className}` : ''}`} data-loading="" data-shape={shape}
      data-shown={shown ? '' : undefined} style={style}>
      {pieces(shape, count)}
    </div>
  );
}
