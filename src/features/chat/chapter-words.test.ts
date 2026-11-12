import { deflateRawSync } from 'node:zlib';
import { describe, expect, it } from 'vitest';
import { chapterFileWords, roundWords } from './chapter-words';

/** A zip of one entry, as Word writes a .docx: a local header, the data, the central directory and its end. */
function zip(name: string, content: string, deflate = true) {
  const raw = Buffer.from(content, 'utf8');
  const data = deflate ? deflateRawSync(raw) : raw;
  const file = Buffer.from(name, 'utf8');
  const local = Buffer.alloc(30);
  local.writeUInt32LE(0x04034b50, 0);
  local.writeUInt16LE(deflate ? 8 : 0, 8);
  local.writeUInt32LE(data.length, 18);
  local.writeUInt32LE(raw.length, 22);
  local.writeUInt16LE(file.length, 26);
  const central = Buffer.alloc(46);
  central.writeUInt32LE(0x02014b50, 0);
  central.writeUInt16LE(deflate ? 8 : 0, 10);
  central.writeUInt32LE(data.length, 20);
  central.writeUInt32LE(raw.length, 24);
  central.writeUInt16LE(file.length, 28);
  central.writeUInt32LE(0, 42);
  const start = 30 + file.length + data.length;
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(1, 8);
  end.writeUInt16LE(1, 10);
  end.writeUInt32LE(46 + file.length, 12);
  end.writeUInt32LE(start, 16);
  return Buffer.concat([local, file, data, central, file, end]);
}

const body = '<w:document><w:body><w:p><w:r><w:t>Кайл шёл</w:t></w:r><w:r><w:t xml:space="preserve"> к реке.</w:t></w:r></w:p><w:p><w:r><w:t>Ольм отстал.</w:t></w:r></w:p></w:body></w:document>';

// specs/2026-11-11-ux4-after-answer.md AA4.2: the words of a chapter before it goes, a Word file too.
describe('the words of a chapter file', () => {
  it.each([true, false])('reads the body of a Word file (deflated: %s)', async (deflate) => {
    const file = new File([zip('word/document.xml', body, deflate)], 'глава.docx');
    expect(await chapterFileWords(file)).toBe(6);
  });

  it('counts a text file and gives up on a file that is no zip', async () => {
    expect(await chapterFileWords(new File(['Кайл шёл к реке.'], 'глава.md'))).toBe(4);
    expect(await chapterFileWords(new File(['не zip'], 'глава.docx'))).toBeUndefined();
  });

  it('says a size, not a count', () => {
    expect([42, 238, 2_437, 12_480].map(roundWords)).toEqual([42, 240, 2_400, 12_500]);
  });
});
