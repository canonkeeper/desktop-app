/**
 * The face of the text of a chapter — italic and bold — in its two forms (specs/2026-11-08-italic-bold-in-text.md, TF1):
 * the marks of the working text of a draft, ⟨…⟩ for italic and ⟪…⟫ for bold beside the marks ⟦…⟧ of the model's text,
 * and the Markdown subset of a paragraph of the book — *italic*, **bold**, ***both***, \* and \\. The same rules as the
 * server's package textface, one table of examples for both (backend/internal/domain/textface/testdata/faces.json);
 * offsets here are those of a JavaScript string.
 */

export const writtenOpen = '⟦';
export const writtenClose = '⟧';
export const italicOpen = '⟨';
export const italicClose = '⟩';
export const boldOpen = '⟪';
export const boldClose = '⟫';

/** A run of a paragraph: its text in one face (chapterfile.Run on the server). */
export interface Run {
  t: string;
  i?: boolean;
  b?: boolean;
}

const italic = 1;
const bold = 2;
type Face = number;

const faceSigns = new Set([italicOpen, italicClose, boldOpen, boldClose]);
const openers = new Set([writtenOpen, italicOpen, boldOpen]);

/** A sign is a mark of a face. */
export const isFace = (sign: string) => faceSigns.has(sign);
/** A sign is a mark of a working text: of the model's text or of a face. */
export const isMark = (sign: string) => sign === writtenOpen || sign === writtenClose || faceSigns.has(sign);
/** Two signs open and close a mark of one kind. */
export const pair = (open: string, close: string) =>
  (open === writtenOpen && close === writtenClose) || (open === italicOpen && close === italicClose) || (open === boldOpen && close === boldClose);
/** A text holds a mark of a face. */
export const hasFaces = (text: string) => /[⟨⟩⟪⟫]/u.test(text);

const opener = (face: Face) => (face === italic ? italicOpen : boldOpen);
const closer = (face: Face) => (face === italic ? italicClose : boldClose);

/** A text that came as text with the signs of the marks of faces made the brackets they look like (TF1.5). */
export function literal(text: string) {
  return hasFaces(text) ? text.replace(/[⟨⟩⟪⟫]/gu, (sign) => ({ '⟨': '‹', '⟩': '›', '⟪': '«', '⟫': '»' })[sign]!) : text;
}

function literalAll(text: string) {
  return literal(text).replace(/[⟦⟧]/gu, (sign) => (sign === writtenOpen ? '[' : ']'));
}

/** A text with some of its marks taken off and where each of its signs stood in the text, one more for the end. */
export interface Projection {
  text: string;
  /** Null when nothing was taken off. */
  toSource: number[] | null;
}

function project(text: string, off: (sign: string) => boolean, signs: RegExp): Projection {
  if (!signs.test(text)) return { text, toSource: null };
  let plainText = '';
  const toSource: number[] = [];
  for (let at = 0; at < text.length; at += 1) {
    if (off(text[at]!)) continue;
    plainText += text[at];
    toSource.push(at);
  }
  toSource.push(text.length);
  return { text: plainText, toSource };
}

/** Every mark off: what models, voice and counts read (TF2.1). */
export const plain = (text: string) => project(text, isMark, /[⟦⟧⟨⟩⟪⟫]/u);
/** Only the marks of faces off, ⟦…⟧ stay: where a cited place is found (TF2.2). */
export const faceless = (text: string) => project(text, isFace, /[⟨⟩⟪⟫]/u);

/** The offset of the text a sign of the projection stands at: right before it, after the marks before it. */
export function sourceAt(projection: Projection, at: number) {
  return projection.toSource ? projection.toSource[at]! : at;
}

/** A place of the projection as offsets of the text, the marks at its edges outside it (TF2.3). */
export function sourceRange(projection: Projection, start: number, end: number): [number, number] {
  if (end <= start) {
    const at = sourceAt(projection, start);
    return [at, at];
  }
  return [sourceAt(projection, start), sourceAt(projection, end - 1) + 1];
}

/** The offset of the projection an offset of the text falls at: how many of its signs stand before it. */
export function projectedAt(projection: Projection, at: number) {
  if (!projection.toSource) return at;
  let low = 0;
  let high = projection.toSource.length - 1;
  while (low < high) {
    const middle = (low + high) >> 1;
    if (projection.toSource[middle]! < at) low = middle + 1;
    else high = middle;
  }
  return low;
}

/**
 * The marks of a working text make a tree (TF1.1): every mark is closed in order, none is inside a mark of its kind, a
 * face holds a sign and does not cross a paragraph (a blank line). A block of the model's text may be empty.
 */
export function valid(text: string) {
  const open: string[] = [];
  let newline = false;
  let last = '';
  const faces = () => open.some(isFace);
  for (const sign of text) {
    if (openers.has(sign)) {
      if (open.includes(sign)) return false;
      if (sign !== writtenOpen && !faces()) newline = false;
      open.push(sign);
    } else if (isMark(sign)) {
      const top = open.at(-1);
      if (top === undefined || !pair(top, sign) || (sign !== writtenClose && last === top)) return false;
      open.pop();
    } else if (sign === '\n') {
      if (newline && faces()) return false;
      newline = true;
    } else {
      newline = false;
    }
    last = sign;
  }
  return open.length === 0;
}

interface Sign {
  text: string;
  mark: string;
  faces: Face;
}

/** The faces of the signs of a text: an opener adds its face, a closer takes it away; a paragraph ends every face. */
function parse(text: string): Sign[] {
  const signs: Sign[] = [];
  const depth = [0, 0];
  let last = -1;
  for (const sign of text) {
    if (sign === italicOpen) depth[0]! += 1;
    else if (sign === italicClose) depth[0] = Math.max(depth[0]! - 1, 0);
    else if (sign === boldOpen) depth[1]! += 1;
    else if (sign === boldClose) depth[1] = Math.max(depth[1]! - 1, 0);
    else if (sign === writtenOpen || sign === writtenClose) signs.push({ text: '', mark: sign, faces: 0 });
    else {
      if (sign === '\n' && last >= 0 && signs[last]!.text === '\n') {
        signs[last]!.faces = 0;
        depth[0] = 0;
        depth[1] = 0;
      }
      signs.push({ text: sign, mark: '', faces: (depth[0]! > 0 ? italic : 0) | (depth[1]! > 0 ? bold : 0) });
      last = signs.length - 1;
    }
  }
  return signs;
}

const covers = (signs: Sign[], face: Face) => signs.every((sign) => sign.mark !== '' || (sign.faces & face) !== 0);

/** Faces that open at once, the one whose characters go on longer outside; italic when they end together. */
function outerFirst(signs: Sign[], at: number, faces: Face): Face[] {
  if (faces === 0) return [];
  if (faces !== (italic | bold)) return [faces];
  for (const sign of signs.slice(at)) {
    if (sign.mark !== '') continue;
    const hasItalic = (sign.faces & italic) !== 0;
    const hasBold = (sign.faces & bold) !== 0;
    if (hasItalic && !hasBold) return [italic, bold];
    if (hasBold && !hasItalic) return [bold, italic];
    if (!hasItalic) return [italic, bold];
  }
  return [italic, bold];
}

/** Signs written with the fewest marks of faces that make a tree (textface write). */
function write(signs: Sign[]) {
  let out = '';
  const stack: Face[] = [];
  let base = 0;
  const closeTo = (height: number) => {
    while (stack.length > height) out += closer(stack.pop()!);
  };
  const open = (at: number, faces: Face) => {
    for (const face of outerFirst(signs, at, faces)) {
      out += opener(face);
      stack.push(face);
    }
  };
  signs.forEach((current, index) => {
    if (current.mark === writtenOpen) {
      let end = index + 1;
      while (end < signs.length && signs[end]!.mark !== writtenClose) end += 1;
      const block = signs.slice(index + 1, end);
      let keep = 0;
      while (keep < stack.length && covers(block, stack[keep]!)) keep += 1;
      closeTo(keep);
      const after = signs[end + 1];
      if (after && after.mark === '' && block.some((sign) => sign.mark === '')) {
        let wanted = 0;
        for (const face of [italic, bold]) {
          if (!stack.includes(face) && covers(block, face) && (after.faces & face) !== 0) wanted |= face;
        }
        open(index + 1, wanted);
      }
      out += writtenOpen;
      base = stack.length;
    } else if (current.mark === writtenClose) {
      closeTo(base);
      out += writtenClose;
      base = 0;
    } else {
      const lowest = stack.findIndex((face) => (current.faces & face) === 0);
      if (lowest >= 0) closeTo(lowest);
      let missing = 0;
      for (const face of [italic, bold]) if ((current.faces & face) !== 0 && !stack.includes(face)) missing |= face;
      open(index, missing);
      out += current.text;
    }
  });
  closeTo(0);
  return out;
}

const joined = /⟨⟩|⟪⟫|⟩⟨|⟫⟪/gu;

/**
 * The marks of faces of a working text made whole (TF1.1): an empty face goes, two faces of one kind side by side are
 * one; marks that still do not make a tree are written anew from the faces of the characters. A text without faces stays.
 */
export function normalize(text: string) {
  if (!hasFaces(text)) return text;
  for (let tidy = text.replace(joined, ''); tidy !== text; tidy = text.replace(joined, '')) text = tidy;
  return valid(text) ? text : write(parse(text));
}

/** The marks of faces a piece of a text cuts: closers of faces opened before it, openers of faces it leaves open. */
export function around(piece: string): [string, string] {
  let closes = '';
  const opens: string[] = [];
  for (const sign of piece) {
    if (sign === italicOpen || sign === boldOpen) opens.push(sign);
    else if (sign === italicClose || sign === boldClose) {
      if (opens.length && pair(opens.at(-1)!, sign)) opens.pop();
      else closes += sign;
    }
  }
  return [closes, opens.join('')];
}

/** A piece of a text with its faces whole: a face it starts inside opens at its start, one it ends inside closes at its end. */
export function whole(piece: string) {
  if (!hasFaces(piece)) return piece;
  const [closes, opens] = around(piece);
  const before = [...closes].reverse().map((sign) => (sign === italicClose ? italicOpen : boldOpen)).join('');
  const after = [...opens].reverse().map((sign) => (sign === italicOpen ? italicClose : boldClose)).join('');
  return normalize(before + piece + after);
}

/** A piece of a text, [start, end), in the faces it stands in. */
export function piece(text: string, start: number, end: number) {
  return whole(around(text.slice(0, start))[1] + text.slice(start, end));
}

/** A text with a place given way to new words: a face around the whole place gives them its face, one it cuts keeps the rest (TF2.3). */
export function replace(text: string, start: number, end: number, words: string) {
  const [closes, opens] = around(text.slice(start, end));
  return normalize(text.slice(0, start) + closes + words + opens + text.slice(end));
}

const facesOf = (run: Run): Face => (run.i ? italic : 0) | (run.b ? bold : 0);

function appendRun(runs: Run[], text: string, faces: Face) {
  if (!text) return;
  const last = runs.at(-1);
  if (last && facesOf(last) === faces) {
    last.t += text;
    return;
  }
  runs.push({ t: text, ...((faces & italic) !== 0 ? { i: true } : {}), ...((faces & bold) !== 0 ? { b: true } : {}) });
}

/** The runs of a paragraph of a working text: its pieces in one face, the marks of the model's text gone. */
export function runs(paragraph: string): Run[] {
  const out: Run[] = [];
  for (const sign of parse(paragraph)) if (sign.mark === '') appendRun(out, sign.text, sign.faces);
  return out;
}

/** A run of a working text, or a mark of the model's text where its block opens or closes. */
export type Piece = Run | typeof writtenOpen | typeof writtenClose;

/**
 * Runs written as a working text with the fewest marks of faces that make a tree, the marks of the model's text where they
 * stand (an editor's document read back, TF6.1); a blank line ends every face, signs of marks in a run made brackets (TF1.5).
 */
export function marked(pieces: readonly Piece[]) {
  const signs: Sign[] = [];
  let last: Sign | undefined;
  for (const run of pieces) {
    if (typeof run === 'string') {
      signs.push({ text: '', mark: run, faces: 0 });
      continue;
    }
    for (const sign of literalAll(run.t)) {
      const current: Sign = { text: sign, mark: '', faces: facesOf(run) };
      if (sign === '\n' && last && last.text === '\n') {
        last.faces = 0;
        current.faces = 0;
      }
      signs.push(current);
      last = current;
    }
  }
  return write(signs);
}

/** The marks open at an offset of a working text, the outermost first: what a piece of it cut there starts inside. */
export function openAt(text: string, at: number): string[] {
  const open: string[] = [];
  for (const sign of text.slice(0, at)) {
    if (openers.has(sign)) open.push(sign);
    else if (isMark(sign) && open.length && pair(open.at(-1)!, sign)) open.pop();
  }
  return open;
}

/** The sign that closes a mark opened by the given one. */
export const closerOf = (open: string) => (open === writtenOpen ? writtenClose : open === italicOpen ? italicClose : boldClose);

const delimiters = ['', '*', '**', '***'];

/** Runs written in the Markdown subset of a paragraph of the book: stars and backslashes escaped, faces between stars (TF1.2). */
export function markdown(pieces: Run[]) {
  let out = '';
  let current = 0;
  for (const run of pieces) {
    if (!run.t) continue;
    const next = facesOf(run);
    out += delimiters[current ^ next]! + run.t.replace(/[\\*]/gu, (sign) => `\\${sign}`);
    current = next;
  }
  return out + delimiters[current]!;
}

/**
 * A paragraph of the book in the Markdown subset read into runs (TF1.2): one, two or three unescaped stars change the
 * faces; a change that is not closed in the paragraph, four stars and more and a backslash before another sign stay text.
 */
export function fromMarkdown(source: string): Run[] {
  const tokens: Array<{ text: string; stars: Face }> = [];
  let text = '';
  const flush = () => {
    if (text) tokens.push({ text, stars: 0 });
    text = '';
  };
  for (let at = 0; at < source.length;) {
    const sign = source[at]!;
    if (sign === '\\') {
      const next = source[at + 1];
      if (next === '\\' || next === '*') {
        text += next;
        at += 2;
      } else {
        text += '\\';
        at += 1;
      }
    } else if (sign === '*') {
      let count = 0;
      while (source[at + count] === '*') count += 1;
      if (count > 3) text += source.slice(at, at + count);
      else {
        flush();
        tokens.push({ text: '', stars: [0, italic, bold, italic | bold][count]! });
      }
      at += count;
    } else {
      text += sign;
      at += 1;
    }
  }
  flush();
  for (const face of [italic, bold]) {
    let last = -1;
    let count = 0;
    tokens.forEach((token, index) => {
      if ((token.stars & face) !== 0) {
        last = index;
        count += 1;
      }
    });
    if (count % 2 === 1) {
      tokens[last]!.stars &= ~face;
      tokens[last]!.text += delimiters[face]!;
    }
  }
  const out: Run[] = [];
  let current = 0;
  for (const token of tokens) {
    current ^= token.stars;
    appendRun(out, token.text, current);
  }
  return out;
}

/** Runs without the spaces at their edges (the server's textface.Trimmed). */
function trimmed(pieces: Run[]): Run[] {
  const out = pieces.filter((run) => run.t).map((run) => ({ ...run }));
  while (out.length && !(out[0]!.t = out[0]!.t.replace(/^\s+/u, ''))) out.shift();
  while (out.length && !(out.at(-1)!.t = out.at(-1)!.t.replace(/\s+$/u, ''))) out.pop();
  return out;
}

/**
 * The paragraphs of the book from a working text (TF3.2, the server's textface.Book): every line with words in the
 * Markdown subset, its edges trimmed, the marks of the model's text gone, a face over a line break closed at its end.
 */
export function book(text: string): string[] {
  const lines: Run[][] = [[]];
  for (const run of runs(text)) {
    run.t.split('\n').forEach((part, index) => {
      if (index > 0) lines.push([]);
      appendRun(lines.at(-1)!, part, facesOf(run));
    });
  }
  return lines.map(trimmed).filter((line) => line.length).map(markdown);
}

/** The working text of a chapter of the book: its paragraphs in the Markdown subset as marks, a blank line between them. */
export const fromBook = (paragraphs: string[]) => paragraphs.map((paragraph) => marked(fromMarkdown(paragraph))).join('\n\n');

/** The plain text of a paragraph of the book in the Markdown subset: what its `text` holds. */
export const markdownPlain = (source: string) => fromMarkdown(source).map((run) => run.t).join('');
