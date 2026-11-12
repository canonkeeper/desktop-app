import { Fragment, Mark, Node, Schema, Slice, type MarkType } from 'prosemirror-model';
import {
  boldClose, boldOpen, closerOf, italicClose, italicOpen, literal, marked, openAt, writtenClose, writtenOpen, type Piece,
} from './faces';

/**
 * The working text of a chapter as the document of its editor (specs/2026-11-08-italic-bold-in-text.md TF6.1): paragraphs —
 * the text between blank lines —, a line break inside one, and the marks `italic`, `bold` and `written` (a block ⟦…⟧ of
 * the model's text, told from its neighbour by its number). The document and a piece of `body` turn into each other with
 * every word and every face kept; the faces are written as textface writes them (`marked`), so a text the server wrote
 * comes back sign for sign. What the document cannot hold is no word: an empty block ⟦⟧ and a mark of the model's text
 * that stands on a blank line move to the nearest words.
 *
 * A position of the document is one more than the offset of its plain text (`plain` of faces): a sign is one place, a line
 * break is one, and the end of a paragraph with the start of the next is the blank line between them.
 */
export const faceSchema = new Schema({
  nodes: {
    doc: { content: 'paragraph+' },
    paragraph: { content: 'inline*', group: 'block', parseDOM: [{ tag: 'p' }], toDOM: () => ['p', 0] },
    text: { group: 'inline' },
    hard_break: { inline: true, group: 'inline', selectable: false, leafText: () => '\n', parseDOM: [{ tag: 'br' }], toDOM: () => ['br'] },
  },
  marks: {
    // The model's text: no rule reads it from the clipboard, and what the author types at its edge is his.
    written: { attrs: { block: { default: 0 } }, inclusive: false, toDOM: () => ['span', { 'data-written': '' }, 0] },
    italic: {
      parseDOM: [
        { tag: 'i' }, { tag: 'em' }, { style: 'font-style=italic' },
        { style: 'font-style=normal', clearMark: (mark) => mark.type.name === 'italic' },
      ],
      toDOM: () => ['em', 0],
    },
    bold: {
      parseDOM: [
        { tag: 'strong' },
        // The clipboard of Google Docs wraps everything in <b style="font-weight:normal">.
        { tag: 'b', getAttrs: (node) => (node as HTMLElement).style.fontWeight !== 'normal' && null },
        { style: 'font-weight=400', clearMark: (mark) => mark.type.name === 'bold' },
        { style: 'font-weight=normal', clearMark: (mark) => mark.type.name === 'bold' },
        { style: 'font-weight', getAttrs: (value) => /^(bold(er)?|[5-9]\d{2,})$/u.test(value as string) && null },
      ],
      toDOM: () => ['strong', 0],
    },
  },
});

const { written, italic, bold } = faceSchema.marks as { written: MarkType; italic: MarkType; bold: MarkType };
export const hardBreak = faceSchema.nodes.hard_break!;
export const paragraph = faceSchema.nodes.paragraph!;
export const writtenMark = written;

/** The face a mark of the editor stands for. */
export type Face = 'italic' | 'bold';
export const faceMark = (face: Face) => (face === 'italic' ? italic : bold);

/** The marks a piece of a working text leaves open at its end, from those open at its start. */
export function openAfter(piece: string, open: readonly string[] = []) {
  return openAt(open.join('') + piece, open.join('').length + piece.length);
}

interface Sign {
  text: string;
  marks: readonly Mark[];
}

/** The marks of a sign: its block of the model's text and its faces, cached so equal ones are one array. */
function marksCache() {
  const cache = new Map<string, readonly Mark[]>();
  return (block: number, faces: number) => {
    const key = `${block}:${faces}`;
    let marks = cache.get(key);
    if (!marks) {
      marks = Mark.setFrom([
        ...(block ? [written.create({ block })] : []), ...(faces & 1 ? [italic.create()] : []), ...(faces & 2 ? [bold.create()] : []),
      ]);
      cache.set(key, marks);
    }
    return marks;
  };
}

/**
 * A piece of a working text as a document (TF6.1), the marks open at its start given: it is read as if they stood before it,
 * and the marks it leaves open at its end are closed after it. A blank line ends every face, as the server reads it.
 */
export function docOf(piece: string, open: readonly string[] = []): Node {
  const end = openAfter(piece, open);
  const text = open.join('') + piece + [...end].reverse().map(closerOf).join('');
  const marksOf = marksCache();
  const signs: Sign[] = [];
  let block = 0;
  let blocks = 0;
  let italics = 0;
  let bolds = 0;
  for (const sign of text) {
    if (sign === writtenOpen) block = ++blocks;
    else if (sign === writtenClose) block = 0;
    else if (sign === italicOpen) italics += 1;
    else if (sign === italicClose) italics = Math.max(italics - 1, 0);
    else if (sign === boldOpen) bolds += 1;
    else if (sign === boldClose) bolds = Math.max(bolds - 1, 0);
    else {
      const last = signs.at(-1);
      if (sign === '\n' && last?.text === '\n') {
        italics = 0;
        bolds = 0;
      }
      signs.push({ text: sign, marks: marksOf(block, (italics > 0 ? 1 : 0) | (bolds > 0 ? 2 : 0)) });
    }
  }
  const paragraphs: Node[] = [];
  let inline: Node[] = [];
  let run = '';
  let runMarks: readonly Mark[] = [];
  const flush = () => {
    if (run) inline.push(faceSchema.text(run, runMarks));
    run = '';
  };
  for (let at = 0; at < signs.length; at += 1) {
    const sign = signs[at]!;
    if (sign.text === '\n' && signs[at + 1]?.text === '\n') {
      flush();
      paragraphs.push(paragraph.create(null, inline));
      inline = [];
      at += 1;
    } else if (sign.text === '\n') {
      flush();
      inline.push(hardBreak.create(null, null, sign.marks));
    } else {
      if (sign.marks !== runMarks) flush();
      runMarks = sign.marks;
      run += sign.text;
    }
  }
  flush();
  paragraphs.push(paragraph.create(null, inline));
  return faceSchema.nodes.doc!.create(null, paragraphs);
}

const blockOf = (node: Node | null | undefined): number => (node ? written.isInSet(node.marks)?.attrs.block ?? 0 : 0);

/**
 * The document written back as the piece of a working text it stands for (TF6.1): the marks of the model's text where
 * its blocks open and close — a blank line between two paragraphs of one block stays in it —, the faces as textface writes
 * them, and the piece's edges joined to the marks open before it (`open`) and after it (`end`).
 */
export function bodyOf(doc: Node, open: readonly string[] = [], end: readonly string[] = []): string {
  const paragraphs: Node[] = [];
  doc.forEach((node) => paragraphs.push(node));
  // The block a blank line stands in: the one the words before it and after it both stand in. Before the first words of
  // the piece stands the block open before it, after the last ones the block open after it.
  const lastBefore: number[] = [];
  const filled = paragraphs.filter((node) => node.childCount > 0);
  // A piece of nothing but blank lines inside a block stays in it.
  const around = (node: Node | null | undefined) => (node ? blockOf(node) : -1);
  let last = open.includes(writtenOpen) ? around(filled[0]?.firstChild) : 0;
  paragraphs.forEach((node, index) => {
    lastBefore[index] = last;
    if (node.childCount) last = blockOf(node.lastChild);
  });
  const firstFrom: number[] = [];
  let first = end.includes(writtenOpen) ? around(filled.at(-1)?.lastChild) : 0;
  for (let index = paragraphs.length - 1; index >= 0; index -= 1) {
    if (paragraphs[index]!.childCount) first = blockOf(paragraphs[index]!.firstChild);
    firstFrom[index] = first;
  }
  const pieces: Piece[] = [];
  let block = 0;
  const enter = (next: number) => {
    if (next === block) return;
    if (block) pieces.push(writtenClose);
    if (next) pieces.push(writtenOpen);
    block = next;
  };
  paragraphs.forEach((node, index) => {
    if (index > 0) {
      enter(lastBefore[index] && lastBefore[index] === firstFrom[index] ? lastBefore[index]! : 0);
      pieces.push({ t: '\n\n' });
    }
    node.forEach((child) => {
      enter(blockOf(child));
      pieces.push({
        t: child.isText ? child.text! : '\n',
        ...(italic.isInSet(child.marks) ? { i: true } : {}),
        ...(bold.isInSet(child.marks) ? { b: true } : {}),
      });
    });
  });
  enter(0);
  return joined(marked(pieces), open, end);
}

/**
 * A balanced piece joined to the marks open around it: the marks it opens at its start that are open already stay
 * unwritten, the others open before it are closed first; the marks it closes at its end that stay open after it stay
 * unclosed, the others are opened again.
 */
function joined(text: string, open: readonly string[], end: readonly string[]) {
  const closed = (marks: readonly string[]) => [...marks].reverse().map(closerOf).join('');
  if (!text) {
    let same = 0;
    while (same < open.length && open[same] === end[same]) same += 1;
    return closed(open.slice(same)) + end.slice(same).join('');
  }
  let head = 0;
  while (head < open.length && text[head] === open[head]) head += 1;
  let tail = 0;
  while (tail < end.length && text.length - tail > head && text[text.length - 1 - tail] === closerOf(end[tail]!)) tail += 1;
  return closed(open.slice(head)) + text.slice(head, text.length - tail) + end.slice(tail).join('');
}

/** Words that came as text: the signs of marks made the brackets they look like (TF1.5), a line ending made one sign. */
export function literalText(text: string) {
  return literal(text.replace(/\r\n?/gu, '\n')).replace(/[⟦⟧]/gu, (sign) => (sign === writtenOpen ? '[' : ']'));
}

/** The plain words of a text — what the clipboard or the voice brings — as a slice of the document with the given marks. */
export function plainSlice(text: string, marks: readonly Mark[] = []): Slice {
  const words = literalText(text);
  const content: Node[] = [];
  docOf(words).forEach((node) => {
    const children: Node[] = [];
    node.forEach((child) => children.push(marks.length ? child.mark(Mark.setFrom([...child.marks, ...marks])) : child));
    content.push(paragraph.create(null, children));
  });
  return new Slice(Fragment.from(content), 1, 1);
}

/** The offset of the plain text a position of the document stands at (TF6.4). */
export function plainAt(doc: Node, pos: number) {
  return Math.min(Math.max(pos - 1, 0), doc.content.size - 2);
}

/** The position of the document an offset of its plain text stands at. */
export function positionAt(doc: Node, offset: number) {
  return Math.min(Math.max(offset + 1, 1), doc.content.size - 1);
}

/** The plain text of the document: a blank line between paragraphs, a line break for a break. */
export function plainOf(doc: Node) {
  return doc.textBetween(0, doc.content.size, '\n\n', '\n');
}

/** The faces every sign of a range holds, or that a typed sign takes when the range is empty. */
export function facesIn(doc: Node, from: number, to: number, stored: readonly Mark[] | null): Record<Face, boolean> {
  if (from === to) {
    const marks = stored ?? doc.resolve(from).marks();
    return { italic: Boolean(italic.isInSet(marks)), bold: Boolean(bold.isInSet(marks)) };
  }
  const all = { italic: true, bold: true };
  let any = false;
  doc.nodesBetween(from, to, (node) => {
    if (!node.isInline) return true;
    if (!node.isText) return false;
    any = true;
    if (!italic.isInSet(node.marks)) all.italic = false;
    if (!bold.isInSet(node.marks)) all.bold = false;
    return false;
  });
  return any ? all : { italic: false, bold: false };
}
