import { readFileSync } from 'node:fs';
import path from 'node:path';
import { Slice } from 'prosemirror-model';
import { describe, expect, it } from 'vitest';
import { bodyOf, docOf, facesIn, openAfter, plainAt, plainOf, plainSlice, positionAt } from './face-doc';
import { normalize, openAt, plain, runs, valid } from './faces';

/** The table of the server and of faces.ts (backend/internal/domain/textface/testdata/faces.json). */
const table = JSON.parse(readFileSync(path.join(process.cwd(), 'backend/internal/domain/textface/testdata/faces.json'), 'utf8')) as {
  convert: Array<{ marks: string }>;
  valid: string[];
  normalize: Array<{ want: string }>;
  replace: Array<{ want: string }>;
};

const roundTrip = (text: string, open: readonly string[] = []) => bodyOf(docOf(text, open), open, openAfter(text, open));
/** Which signs of a text stand between the marks of the model's text. */
const writtenSigns = (text: string) => {
  let inside = false;
  return [...text].flatMap((sign) => {
    if (sign === '⟦' || sign === '⟧') inside = sign === '⟦';
    return /[⟦⟧⟨⟩⟪⟫]/u.test(sign) ? [] : [inside];
  }).join('');
};
/** Two working texts hold the same words in the same faces, written by the same hand. */
const same = (left: string, right: string) => valid(left) && JSON.stringify(runs(left)) === JSON.stringify(runs(right))
  && writtenSigns(left) === writtenSigns(right);

// specs/2026-11-08-italic-bold-in-text.md TF6.1, TF6.4: the document of the editor and the working text, one into the other.
describe('the document of the editor of a chapter', () => {
  it('gives back every working text the server writes sign for sign, and any other one with its words, faces and blocks', () => {
    const written = [
      ...table.convert.map((example) => example.marks),
      ...table.replace.map((example) => example.want),
      '', '\n', '\n\n', 'a\n\n\nb', 'a\n\n\n\n\nb', '\n\nтекст\n\n',
      'Автор.\n\n⟦Сцена один.\n\nДальше.⟧\n\n⟦Сцена два.⟧ И снова автор.',
      '⟦а⟧⟦б⟧ ⟦в⟧\n\n⟦г⟧', '⟦а\n\n\n\nб⟧', '⟦а\nб⟧\nв', 'Он ⟨сказал⟩:\n― ⟪Стой⟫.\n\n⟦⟨Тише.⟩⟧',
    ];
    for (const text of written) {
      expect(roundTrip(text), JSON.stringify(text)).toBe(text);
      expect(plainOf(docOf(text)), text).toBe(plain(text).text);
    }
    for (const text of [...table.valid, ...table.normalize.map((example) => example.want)].filter((item) => item !== '⟦⟧' && item !== 'а\n⟨\nб⟩')) {
      expect(same(roundTrip(text), text), JSON.stringify(text)).toBe(true);
    }
  });

  it('keeps every word of what it cannot hold and writes valid marks', () => {
    for (const [text, want] of [['а ⟦⟧б', 'а б'], ['а\n⟨\nб⟩', 'а\n\nб'], ['⟦а\n\n⟧б', '⟦а⟧\n\nб'], ['x⟦\n\nа⟧', 'x\n\n⟦а⟧']] as const) {
      expect(roundTrip(text)).toBe(want);
    }
  });

  it('reads a piece cut inside marks and joins it back to the marks around it', () => {
    const body = 'Начало ⟨курсив ⟦блок, и он идёт⟧ дальше⟩ и ⟪жирный⟫ конец.\n\n⟦Сцена ⟨два⟩\n\nи ещё.⟧';
    for (let start = 0; start <= body.length; start += 1) {
      for (let end = start; end <= body.length; end += 1) {
        const piece = body.slice(start, end);
        const joinedBody = body.slice(0, start) + roundTrip(piece, openAt(body, start)) + body.slice(end);
        expect(valid(joinedBody), `${start}-${end}`).toBe(true);
        expect(same(joinedBody, body), `${start}-${end}`).toBe(true);
      }
    }
    // A piece cut inside a block alone comes back as it was.
    const scene = body.indexOf('Сцена');
    expect(roundTrip(body.slice(scene), openAt(body, scene))).toBe(body.slice(scene));
  });

  it('closes and opens again what an edit at the edge of a piece took away', () => {
    const body = 'а ⟦блок начат здесь и кончится там⟧ б';
    const start = body.indexOf('начат');
    const piece = body.slice(start);
    const open = openAt(body, start);
    const doc = docOf(piece, open);
    // The words of the block at the start of the piece are taken out.
    const edited = doc.replace(1, 1 + 'начат здесь и кончится там'.length, Slice.empty);
    const next = body.slice(0, start) + bodyOf(edited, open, openAfter(piece, open));
    expect(next).toBe('а ⟦блок ⟧ б');
    expect(valid(normalize(next))).toBe(true);
  });

  it('stands its positions one past the offsets of the plain text', () => {
    const text = 'Раз ⟨два⟩\nтри\n\n⟦четыре⟧';
    const doc = docOf(text);
    const words = plain(text).text;
    for (let offset = 0; offset <= words.length; offset += 1) {
      expect(plainAt(doc, positionAt(doc, offset))).toBe(offset);
      if (offset < words.length && words[offset] !== '\n') expect(doc.textBetween(positionAt(doc, offset), positionAt(doc, offset + 1))).toBe(words[offset]);
    }
  });

  it('brings plain words with the marks around and no sign of a mark', () => {
    const slice = plainSlice('один ⟨два⟩\r\n\r\nтри ⟦четыре⟧');
    expect(slice.content.childCount).toBe(2);
    expect(slice.content.textBetween(0, slice.content.size, '\n\n', '\n')).toBe('один ‹два›\n\nтри [четыре]');
  });

  it('says which faces a range holds', () => {
    const doc = docOf('а ⟨бв⟩ г');
    expect(facesIn(doc, 3, 5, null)).toEqual({ italic: true, bold: false });
    expect(facesIn(doc, 1, 5, null)).toEqual({ italic: false, bold: false });
  });
});
