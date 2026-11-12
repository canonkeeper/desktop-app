import { StickyNote } from 'lucide-react';
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import type { ReaderChapter, ReaderLocator } from '../../../entities/manuscript/api';
import { ReaderParagraph, ReaderText, type TextMark } from '../../../ui/ReaderParagraph';
import { TextSelectionMenu } from '../../../ui/TextSelectionMenu';
import styles from './ReaderCore.module.css';

/** The drawings of a chapter in the reader (illustrations R8.9): a sign beside a paragraph and, shown in the text, after it. */
export interface ReaderParagraphDrawings {
  /** The signs on the margin of the drawings that show the paragraph. */
  aside: (paragraphId: string) => ReactNode;
  /** «Показывать рисунки в тексте»: the drawings placed after the last paragraph they show. */
  after: (paragraphId: string) => ReactNode;
}

/** A note of the chapter on the margin of its paragraph (RD3.3). */
export interface MarginNote {
  id: string;
  paragraphId: string;
  title: string;
}

/** What was selected: the words, their paragraphs in reading order and where they stand on the screen. */
export interface ReaderSelection {
  text: string;
  rect: DOMRect;
  locator: ReaderLocator;
  paragraphIds: string[];
}

interface ReaderCoreProps {
  chapter: ReaderChapter;
  /** «Глава 12. Белый порт»: the one heading of the chapter (RD1.1). */
  title: string;
  drawings?: ReaderParagraphDrawings | undefined;
  notes?: readonly MarginNote[];
  onOpenNote?: (noteId: string) => void;
  /** The paragraph a link names: lit and in the middle of the screen. */
  activeParagraphId?: string | null;
  /** The words found by «Найти», by paragraph. */
  marks?: ReadonlyMap<string, readonly TextMark[]>;
  locator: ReaderLocator;
  onAsk?: (selection: ReaderSelection) => void;
  onCopySelection: (text: string) => void;
  onFindSelection: (text: string) => void;
  onCaptureSelection?: (text: string, locator: ReaderLocator) => void;
  /** «Нарисовать» with the selection: the window at the selection (RD3.2). */
  onDrawSelection?: ((selection: ReaderSelection) => void) | undefined;
  drawLocked?: boolean;
  /** What stands after the text: the step between chapters. */
  footer?: ReactNode;
}

/**
 * The text of a chapter (specs/2026-11-10-ux7-reader.md RD1.1, RD2.1, RD3.1, RD3.3): its heading once, the paragraphs
 * with their italic and bold, the words found lit, the signs of notes and drawings on the margin, the row of actions of a
 * selection and, at the end, the step between chapters.
 */
export function ReaderCore({
  activeParagraphId, chapter, drawLocked, drawings, footer, locator, marks, notes = [], onAsk, onCaptureSelection, onCopySelection,
  onDrawSelection, onFindSelection, onOpenNote, title,
}: ReaderCoreProps) {
  const [selection, setSelection] = useState<ReaderSelection | null>(null);
  const rootRef = useRef<HTMLElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const activeHeading = chapter.paragraphs.find((paragraph) => paragraph.id === activeParagraphId && paragraph.kind === 'heading');
  const current = [...(marks?.values() ?? [])].flat().find((mark) => mark.current);

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

  // The current place of «Найти» stands in the middle of the screen.
  useEffect(() => {
    if (!current) return;
    rootRef.current?.querySelector<HTMLElement>('[data-find-current]')?.scrollIntoView?.({ block: 'center' });
  }, [current, chapter]);

  // Capture the actual selection, anchored to its first paragraph, without navigation.
  const updateSelection = useCallback(() => {
    const now = window.getSelection();
    if (!now || now.rangeCount === 0 || now.isCollapsed) {
      setSelection(null);
      return;
    }
    const text = now.toString();
    const range = now.getRangeAt(0);
    const container = range.commonAncestorContainer;
    if (!text.trim() || !bodyRef.current?.contains(container)) {
      setSelection(null);
      return;
    }
    const selected = [...bodyRef.current.querySelectorAll<HTMLElement>('p[data-paragraph-id]')].filter((paragraph) => {
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

  const findOn = Boolean(marks?.size);
  return (
    <article className={styles.readerCore} data-reader-target={locator.paragraphId ?? 'chapter'}
      onPointerUp={updateSelection} ref={rootRef} tabIndex={-1}>
      <h1 className={styles.title} data-page-heading="" data-paragraph-id={activeHeading?.id} tabIndex={-1}>
        {activeHeading ? <ReaderText highlighted>{title}</ReaderText> : title}
      </h1>

      <div className={styles.readerBody} ref={bodyRef} role="document">
        {chapter.paragraphs.filter((paragraph) => paragraph.kind !== 'heading').map((paragraph) => {
          const paragraphMarks = marks?.get(paragraph.id);
          // A link lights its paragraph; while «Найти» is open only the found words are lit.
          const lit = !findOn && paragraph.id === activeParagraphId;
          const paragraphNotes = notes.filter((note) => note.paragraphId === paragraph.id);
          const aside = drawings?.aside(paragraph.id);
          const after = drawings?.after(paragraph.id);
          return (
            <div className={styles.paragraphRow} key={paragraph.id}>
              <ReaderParagraph highlighted={lit} id={paragraph.id} text={paragraph.text}
                {...(paragraph.markdown === undefined ? {} : { markdown: paragraph.markdown })} {...(paragraphMarks ? { marks: paragraphMarks } : {})} />
              {paragraphNotes.length || aside ? (
                <div className={styles.margin}>
                  {paragraphNotes.map((note) => (
                    <button aria-label={`Заметка: ${note.title}`} className={styles.mark} key={note.id} onClick={() => onOpenNote?.(note.id)}
                      title={note.title} type="button">
                      <StickyNote aria-hidden="true" size={16} />
                    </button>
                  ))}
                  {aside}
                </div>
              ) : null}
              {after}
            </div>
          );
        })}
      </div>

      {footer}

      {selection ? <TextSelectionMenu ceiling={() => textRoof(rootRef.current)} rect={selection.rect} onDismiss={() => setSelection(null)}
        drawLocked={drawLocked}
        noteWord="Заметка"
        onAsk={onAsk ? () => act(() => onAsk(selection)) : undefined}
        onDraw={onDrawSelection ? () => {
          const chosen = selection;
          window.getSelection()?.removeAllRanges();
          setSelection(null);
          onDrawSelection(chosen);
        } : undefined}
        onAddNote={onCaptureSelection ? () => act(() => onCaptureSelection(selection.text, selection.locator)) : undefined}
        onFind={() => act(() => onFindSelection(selection.text))} onCopy={() => act(() => onCopySelection(selection.text))} /> : null}
    </article>
  );
}

/**
 * Where the room of the text starts on the screen (specs/2026-11-11-ux2-polish.md PL4.1): under the top of the reader that
 * stays over the text — the line of the reader with the bar of «Найти», the thin title of «Только текст», or the top of the
 * scroll while the line is away — and under the title of the chapter while it is seen. The row of a selection stands over the
 * selection only below it.
 */
function textRoof(article: HTMLElement | null) {
  if (!article) return 0;
  const scroller = article.closest<HTMLElement>('[data-reader-scroll]');
  const top = scroller?.querySelector<HTMLElement>('[data-reader-bar]')?.closest('header');
  const title = article.querySelector<HTMLElement>('h1');
  return Math.max(scroller?.getBoundingClientRect().top ?? 0, top?.getBoundingClientRect().bottom ?? 0, title?.getBoundingClientRect().bottom ?? 0);
}
