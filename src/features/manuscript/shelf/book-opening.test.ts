import { describe, expect, it } from 'vitest';
import { zipFiles } from '../../../shared/files/zip';
import { fileOpening } from './book-opening';

// specs/2026-11-11-ux9-shelf-chat.md SC1.3: the page reads how a file of a book begins in every kind the shelf takes.
const text = 'Туман лежал на Белом порту.';
const utf8 = (value: string) => new TextEncoder().encode(value);
const cp1251 = (value: string) => Uint8Array.from([...value].map((char) => {
  const code = char.charCodeAt(0);
  if (code < 128) return code;
  if (char === 'ё') return 0xb8;
  if (char === 'Ё') return 0xa8;
  return code - 0x410 + 0xc0;
}));
const named = (name: string, bytes: Uint8Array) => new File([bytes.slice()], name);

describe('how a file of a book begins', () => {
  it('reads an FB2 by the encoding it names, past its description and without its pictures', async () => {
    const fb2 = `<?xml version="1.0" encoding="windows-1251"?><FictionBook><description><title-info><book-title>Хроники</book-title></title-info></description><body><section><title><p>Глава 1</p></title><p>${text}</p></section></body><binary id="c">AAAA</binary></FictionBook>`;
    const opening = await fileOpening(named('Том 1.fb2', cp1251(fb2)));
    expect(opening).toContain(text);
    expect(opening).not.toContain('AAAA');
    const zipped = zipFiles([{ name: 'Том 1.fb2', bytes: cp1251(fb2) }]);
    expect(await fileOpening(named('Том 1.fb2.zip', zipped))).toContain(text);
  });

  it('reads a Word body, an ODT and an EPUB in the order of its spine', async () => {
    const docx = zipFiles([{ name: 'word/document.xml', bytes: utf8(`<w:document><w:body><w:p><w:r><w:t>Туман лежал</w:t></w:r><w:r><w:t xml:space="preserve"> на Белом порту.</w:t></w:r></w:p></w:body></w:document>`) }]);
    expect(await fileOpening(named('рукопись.docx', docx))).toContain(text);
    const odt = zipFiles([{ name: 'content.xml', bytes: utf8(`<office:document-content><office:body><text:p>${text}</text:p></office:body></office:document-content>`) }]);
    expect(await fileOpening(named('рукопись.odt', odt))).toContain(text);
    const epub = zipFiles([
      { name: 'META-INF/container.xml', bytes: utf8('<container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>') },
      { name: 'OEBPS/content.opf', bytes: utf8('<package><manifest><item id="b" href="text/b.xhtml"/><item id="a" href="text/a.xhtml"/></manifest><spine><itemref idref="a"/><itemref idref="b"/></spine></package>') },
      { name: 'OEBPS/text/b.xhtml', bytes: utf8('<html><body><p>Вторая страница.</p></body></html>') },
      { name: 'OEBPS/text/a.xhtml', bytes: utf8(`<html><head><title>Обложка</title></head><body><p>${text}</p></body></html>`) },
    ]);
    const opening = await fileOpening(named('книга.epub', epub));
    expect(opening!.indexOf(text)).toBeLessThan(opening!.indexOf('Вторая страница.'));
    expect(opening).not.toContain('Обложка');
  });

  it('reads an RTF by its code page and its letters of \\u, a text and Markdown as they are', async () => {
    const letters = [...'Туман лежал'].map((char) => (char === ' ' ? ' ' : `\\'${(cp1251(char)[0]!).toString(16)}`)).join('');
    const rtf = `{\\rtf1\\ansi\\ansicpg1251{\\fonttbl{\\f0 Times;}}{\\*\\generator Word;}\\f0 ${letters} \\u1085?\\u1072? Белом порту.\\par}`;
    const opening = await fileOpening(named('рукопись.rtf', Uint8Array.from([...rtf].map((char) => (char.charCodeAt(0) < 128 ? char.charCodeAt(0) : cp1251(char)[0]!)))));
    expect(opening).toContain(text);
    expect(opening).not.toContain('Times');
    expect(await fileOpening(named('рукопись.md', utf8(`# Глава 1\n\n${text}`)))).toContain(text);
  });

  it('begins with nothing for a file it cannot read', async () => {
    expect(await fileOpening(named('рукопись.docx', utf8('не zip')))).toBeNull();
    expect(await fileOpening(named('рукопись.pdf', utf8('%PDF')))).toBeNull();
  });
});
