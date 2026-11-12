import type { ReactNode } from 'react';
import { fromMarkdown, type Run } from '../shared/text/faces';
import styles from './ReaderParagraph.module.css';

/**
 * The runs of a paragraph of the book by its Markdown subset (specs/2026-11-08-italic-bold-in-text.md TF1.2, TF5.1), when
 * they hold a face and their words are the paragraph's text; else null and the text is shown as it is. A row written
 * before the book kept faces, whose stars are its words, reads so as text, as does one without any face.
 */
export function readerRuns(text: string, markdown: string | undefined): Run[] | null {
  if (!markdown || markdown === text) return null;
  const runs = fromMarkdown(markdown);
  if (!runs.some((run) => run.i || run.b) || runs.map((run) => run.t).join('') !== text) return null;
  return runs;
}

function runNode(run: Run, key: number) {
  const bold = run.b ? <strong>{run.t}</strong> : run.t;
  return run.i ? <em key={key}>{bold}</em> : run.b ? <strong key={key}>{run.t}</strong> : run.t;
}

/** A found place in a paragraph (specs/2026-11-10-ux7-reader.md RD2.1): its range in the text; the current one is stronger. */
export interface TextMark {
  start: number;
  end: number;
  current?: boolean;
}

/** The runs cut at the edges of the marks, each piece with the mark it is in. */
function markedPieces(runs: readonly Run[], marks: readonly TextMark[]) {
  const pieces: { run: Run; mark: TextMark | null }[] = [];
  let offset = 0;
  for (const run of runs) {
    const end = offset + run.t.length;
    const cuts = new Set([offset, end]);
    for (const mark of marks) {
      if (mark.start > offset && mark.start < end) cuts.add(mark.start);
      if (mark.end > offset && mark.end < end) cuts.add(mark.end);
    }
    const points = [...cuts].sort((left, right) => left - right);
    for (let index = 0; index + 1 < points.length; index += 1) {
      const from = points[index]!;
      const to = points[index + 1]!;
      pieces.push({ run: { ...run, t: run.t.slice(from - offset, to - offset) }, mark: marks.find((mark) => mark.start <= from && to <= mark.end) ?? null });
    }
    offset = end;
  }
  return pieces;
}

/** A paragraph's words with its italic and bold (TF5.1); a selection, a link ¶ and the lit place go by `children`, the text. */
export function ReaderText({ children, highlighted = false, markdown, marks }: { children: string; highlighted?: boolean; markdown?: string; marks?: readonly TextMark[] }) {
  const runs = readerRuns(children, markdown);
  const content = marks?.length
    ? markedPieces(runs ?? [{ t: children }], marks).map(({ mark, run }, index) => mark
      ? <mark className={styles.found} data-find-current={mark.current || undefined} key={index}>{runNode(run, index)}</mark>
      : <span key={index}>{runNode(run, index)}</span>)
    : runs ? runs.map(runNode) : children;
  return highlighted ? <mark className={styles.highlight}>{content}</mark> : <>{content}</>;
}

export function ReaderParagraph({ id, text, markdown, highlighted = false, marks, onSelect, size = 'default', muted = false }: {
  id: string; text: string; markdown?: string; highlighted?: boolean; marks?: readonly TextMark[]; onSelect?: () => void; size?: 'default' | 'compact'; muted?: boolean;
}) {
  const content = <ReaderText highlighted={highlighted} {...(markdown === undefined ? {} : { markdown })} {...(marks ? { marks } : {})}>{text}</ReaderText>;
  return <p className={styles.paragraph} data-paragraph-id={id} data-size={size} data-muted={muted || undefined}>
    {onSelect ? <button aria-pressed={highlighted} className={styles.paragraphButton} onClick={onSelect} type="button">{content}</button> : content}
  </p>;
}

export function ReaderPassage({ children }: { children: ReactNode }) {
  return <div className={styles.passage}>{children}</div>;
}
