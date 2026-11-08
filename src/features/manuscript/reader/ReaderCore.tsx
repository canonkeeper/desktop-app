import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import type { ReaderChapter, ReaderLocator } from '../../../entities/manuscript/api';
import { formatReaderChapterTitle } from '../../../ui/copy';
import { ReaderParagraph, ReaderText } from '../../../ui/ReaderParagraph';
import { TextSelectionMenu } from '../../../ui/TextSelectionMenu';
import styles from './ReaderCore.module.css';

/** The drawings of a chapter in the reader (illustrations R8.9): beside a paragraph and inside the text. */
export interface ReaderParagraphDrawings {
  /** Thumbnails of the drawings that show the paragraph, beside it. */
  aside: (paragraphId: string) => ReactNode;
  /** «Иллюстрированная книга»: the drawings placed after the last paragraph they show. */
  after: (paragraphId: string) => ReactNode;
  /** Some paragraph has thumbnails: the text leaves room for them. */
  margin: boolean;
}

interface ReaderCoreProps {
  chapter: ReaderChapter;
  drawings?: ReaderParagraphDrawings;
  /** «Нарисовать эту сцену» with the selected paragraphs, in reading order. */
  onDrawSelection?: (paragraphIds: string[]) => void;
  drawLocked?: boolean;
  activeParagraphId?: string | null;
  highlightedParagraphIds?: string[];
  locator: ReaderLocator;
  onCopySelection: (text: string) => void;
  onFindInBook: (text: string) => void;
  onCaptureSelection?: (text: string, locator: ReaderLocator) => void;
}

export function ReaderCore({
  chapter, activeParagraphId, drawLocked, drawings, highlightedParagraphIds, locator, onCopySelection, onDrawSelection, onFindInBook, onCaptureSelection,
}: ReaderCoreProps) {
  const [selection, setSelection] = useState<{ text: string; rect: DOMRect; locator: ReaderLocator; paragraphIds: string[] } | null>(null);
  const rootRef = useRef<HTMLElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const selectedHeading = chapter.paragraphs.find((paragraph) => paragraph.id === activeParagraphId && paragraph.kind === 'heading');

  useEffect(() => {
    setSelection(null);
    window.getSelection()?.removeAllRanges();
  }, [locator.chapterId, locator.paragraphId]);

  useEffect(() => {
    if (!activeParagraphId || !rootRef.current) return;
    const target = [...rootRef.current.querySelectorAll<HTMLElement>('[data-paragraph-id]')]
      .find((paragraph) => paragraph.dataset.paragraphId === activeParagraphId);
    target?.scrollIntoView?.({ block: 'center' });
  }, [activeParagraphId, chapter]);

  // Capture the actual selection, anchored to its first paragraph, without navigation.
  const updateSelection = useCallback(() => {
    const current = window.getSelection();
    if (!current || current.rangeCount === 0 || current.isCollapsed) {
      setSelection(null);
      return;
    }
    const text = current.toString();
    const range = current.getRangeAt(0);
    const container = range.commonAncestorContainer;
    if (!text.trim() || !bodyRef.current?.contains(container)) {
      setSelection(null);
      return;
    }
    const selected = [...bodyRef.current.querySelectorAll<HTMLElement>('[data-paragraph-id]')].filter((paragraph) => {
      if (!range.intersectsNode(paragraph)) return false;
      const part = range.cloneRange();
      if (!paragraph.contains(range.startContainer)) part.setStart(paragraph, 0);
      if (!paragraph.contains(range.endContainer)) part.setEnd(paragraph, paragraph.childNodes.length);
      return Boolean(part.toString().trim());
    });
    const paragraph = chapter.paragraphs.find((item) => item.id === selected[0]?.dataset.paragraphId);
    if (!paragraph) { setSelection(null); return; }
    setSelection({ text, rect: range.getBoundingClientRect(), paragraphIds: selected.map((element) => element.dataset.paragraphId!), locator: {
      ...locator, paragraphId: paragraph.id,
      range: { startOffset: 0, endOffset: paragraph.text.length, quote: paragraph.text },
    } });
  }, [chapter, locator]);

  useEffect(() => {
    document.addEventListener('selectionchange', updateSelection);
    return () => document.removeEventListener('selectionchange', updateSelection);
  }, [updateSelection]);

  useEffect(() => {
    const keyUp = (event: KeyboardEvent) => {
      if (event.target !== document.body && (!(event.target instanceof Node) || !rootRef.current?.contains(event.target))) return;
      if (event.key === 'Shift' || event.shiftKey) updateSelection();
    };
    document.addEventListener('keyup', keyUp);
    return () => document.removeEventListener('keyup', keyUp);
  }, [updateSelection]);

  function act(run: () => void) {
    rootRef.current?.focus({ preventScroll: true });
    window.getSelection()?.removeAllRanges();
    setSelection(null);
    run();
  }

  return (
    <article className={styles.readerCore} data-reader-target={locator.paragraphId ?? 'chapter'}
      onPointerUp={updateSelection} ref={rootRef} tabIndex={-1}>
      <header className={styles.readerHeader} data-paragraph-id={selectedHeading?.id}>
        <div>
          <p>Глава {chapter.navigation.displayNumber}</p>
          <h2>{selectedHeading ? <ReaderText highlighted>{selectedHeading.text}</ReaderText> : formatReaderChapterTitle(chapter.navigation.displayNumber, chapter.title)}</h2>
        </div>
      </header>

      <div className={styles.readerBody} data-drawing-margin={drawings?.margin || undefined} ref={bodyRef} role="document">
        {chapter.paragraphs.filter((paragraph) => paragraph.kind !== 'heading' || highlightedParagraphIds !== undefined && paragraph.id !== selectedHeading?.id).map((paragraph) => {
              const isTarget = highlightedParagraphIds ? highlightedParagraphIds.includes(paragraph.id) : paragraph.id === (activeParagraphId ?? locator.paragraphId);
              const text = <ReaderParagraph id={paragraph.id} key={paragraph.id} text={paragraph.text} highlighted={isTarget} />;
              const aside = drawings?.aside(paragraph.id);
              const after = drawings?.after(paragraph.id);
              if (!aside && !after) return text;
              return (
                <div className={styles.drawnParagraph} key={paragraph.id}>
                  {text}
                  {aside ? <div className={styles.paragraphDrawings}>{aside}</div> : null}
                  {after}
                </div>
              );
            })}
      </div>

      <footer className={styles.footer}>
          <span>
            {chapter.navigation.position} / {chapter.navigation.total}
          </span>
      </footer>

      {selection ? <TextSelectionMenu rect={selection.rect} onDismiss={() => setSelection(null)}
        drawLocked={drawLocked}
        onDraw={onDrawSelection ? () => act(() => onDrawSelection(selection.paragraphIds)) : undefined}
        onAddNote={onCaptureSelection ? () => act(() => onCaptureSelection(selection.text, selection.locator)) : undefined}
        onFind={() => act(() => onFindInBook(selection.text))} onCopy={() => act(() => onCopySelection(selection.text))} /> : null}
    </article>
  );
}
