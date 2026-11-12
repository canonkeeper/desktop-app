/**
 * A field of formatted text the page draws itself (an editable element that is not an input): what it reads as text and
 * how words are written into it the way typing does (specs/2026-11-08-italic-bold-in-text.md TF6.5). The voice reads and
 * fills such a field by it, never by its DOM: its text is the plain words, and filling it keeps the faces around them.
 */
export interface RichField {
  /** The plain words of the field, a blank line between paragraphs. */
  text: () => string;
  /** The field takes these words as its text, as typing would; false when it cannot be written now. */
  fill: (text: string) => boolean;
  /** Selects a stretch of its words by offsets of `text`, as the author's selection — what `setSelectionRange` is to an input. */
  select: (start: number, end: number) => void;
}

const fields = new WeakMap<Element, RichField>();

/** The field behind an element; the returned function forgets it. */
export function registerRichField(element: Element, field: RichField) {
  fields.set(element, field);
  return () => {
    if (fields.get(element) === field) fields.delete(element);
  };
}

export function richFieldOf(element: Element): RichField | undefined {
  return fields.get(element);
}
