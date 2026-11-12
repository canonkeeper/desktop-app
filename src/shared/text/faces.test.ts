import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { around, book, faceless, fromBook, fromMarkdown, literal, markdown, markdownPlain, marked, normalize, piece, plain, replace, runs, sourceAt, sourceRange, valid, whole, type Run } from './faces';

/** The table the server reads too (backend/internal/domain/textface/textface_test.go). */
const table = JSON.parse(readFileSync(path.join(process.cwd(), 'backend/internal/domain/textface/testdata/faces.json'), 'utf8')) as {
  convert: Array<{ marks: string; markdown: string; plain: string; runs: Run[] }>;
  valid: string[];
  invalid: string[];
  normalize: Array<{ text: string; want: string }>;
  replace: Array<{ text: string; quote: string; with: string; want: string }>;
  project: Array<{ text: string; plain: string; faceless: string }>;
  lenient: Array<{ markdown: string; runs: Run[] }>;
  book: Array<{ text: string; book: string[]; back: string }>;
};

// specs/2026-11-08-italic-bold-in-text.md TF1, TF2: the same rules as the server, by one table.
describe('the faces of the text', () => {
  it('go between the marks of a working text, the Markdown of the book and runs', () => {
    expect(table.convert.length).toBeGreaterThan(5);
    for (const example of table.convert) {
      expect(runs(example.marks), example.marks).toEqual(example.runs);
      expect(marked(example.runs), example.marks).toBe(example.marks);
      expect(markdown(example.runs), example.marks).toBe(example.markdown);
      expect(fromMarkdown(example.markdown), example.markdown).toEqual(example.runs);
      expect(plain(example.marks).text, example.marks).toBe(example.plain);
      expect(valid(example.marks), example.marks).toBe(true);
    }
    for (const example of table.lenient) expect(fromMarkdown(example.markdown), example.markdown).toEqual(example.runs);
  });

  it('go into the paragraphs of the book and back as the server takes them', () => {
    expect(table.book.length).toBeGreaterThan(2);
    for (const example of table.book) {
      expect(book(example.text), example.text).toEqual(example.book);
      expect(example.book.map(markdownPlain), example.text).toEqual(plain(example.text).text.split('\n').map((line) => line.trim()).filter(Boolean));
      expect(fromBook(example.book), example.text).toBe(example.back);
    }
  });

  it('make a tree of the marks and are made whole', () => {
    for (const text of table.valid) expect(valid(text), text).toBe(true);
    for (const text of table.invalid) expect(valid(text), text).toBe(false);
    for (const example of table.normalize) {
      const got = normalize(example.text);
      expect(got, example.text).toBe(example.want);
      expect(valid(got), got).toBe(true);
      expect(normalize(got), got).toBe(got);
    }
  });

  it('find a quote through the marks of faces and keep the faces around a replaced place', () => {
    for (const example of table.replace) {
      const view = faceless(example.text);
      const at = view.text.indexOf(example.quote);
      expect(at, example.quote).toBeGreaterThanOrEqual(0);
      const [start, end] = sourceRange(view, at, at + example.quote.length);
      expect(plain(example.text.slice(start, end)).text).toBe(plain(example.quote).text);
      expect(replace(example.text, start, end, example.with), example.text).toBe(example.want);
    }
    for (const example of table.project) {
      expect(plain(example.text).text).toBe(example.plain);
      expect(faceless(example.text).text).toBe(example.faceless);
      const view = plain(example.text);
      for (let at = 0; at < view.text.length; at += 1) expect(example.text[sourceAt(view, at)]).toBe(view.text[at]);
      expect(sourceAt(view, view.text.length)).toBe(example.text.length);
    }
  });

  it('make any marks whole without moving a character or a mark of the model\'s text', () => {
    const alphabet = ['а', 'б', ' ', '\n', '⟨', '⟩', '⟪', '⟫', '⟦', '⟧'];
    let seed = 7;
    const next = () => {
      seed = (Math.imul(seed, 1103515245) + 12345) >>> 0;
      return seed >>> 16;
    };
    for (let round = 0; round < 5000; round += 1) {
      let text = '';
      let written = false;
      for (let count = next() % 14; count > 0; count -= 1) {
        const sign = alphabet[next() % alphabet.length]!;
        if ((sign === '⟦' && written) || (sign === '⟧' && !written)) continue;
        if (sign === '⟦' || sign === '⟧') written = !written;
        text += sign;
      }
      if (written) text += '⟧';
      const got = normalize(text);
      expect(valid(got) && normalize(got) === got && plain(got).text === plain(text).text, `${text} → ${got}`).toBe(true);
      expect(got.replace(/[⟨⟩⟪⟫]/gu, ''), text).toBe(text.replace(/[⟨⟩⟪⟫]/gu, ''));
      expect(runs(marked(runs(got))), got).toEqual(runs(got));
      expect(fromMarkdown(markdown(runs(got))), got).toEqual(runs(got));
    }
  });

  it('keep a piece whole and the signs that came as text brackets', () => {
    expect(whole('б⟩ в ⟪г')).toBe('⟨б⟩ в ⟪г⟫');
    expect(around('б⟩ в ⟪г')).toEqual(['⟩', '⟪']);
    expect(piece('а ⟨б в⟩ г', 3, 4)).toBe('⟨б⟩');
    expect(literal('⟨а⟩ ⟪б⟫ ⟦в⟧')).toBe('‹а› «б» ⟦в⟧');
    expect(marked([{ t: '⟨а⟩ ⟦б⟧', i: true }])).toBe('⟨‹а› [б]⟩');
  });
});
