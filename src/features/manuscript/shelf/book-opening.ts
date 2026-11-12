import { bytesOf, zipEntries, zipText, type ZipEntry } from '../../../shared/files/zip';

/**
 * How a file of a book begins, read in the browser before any transfer (specs/2026-11-11-ux9-shelf-chat.md SC1.3): the
 * text of its first pages in every kind the shelf takes — FB2 and FB2 in a zip, EPUB, Word, ODT, RTF, text and Markdown.
 * The page compares it with how each book of the saga begins (`Book.opening`, the rule is `similarBook` of
 * `upload-files.ts`): a new version of a volume under another name is asked about as that volume. Nothing is sent and
 * nothing is kept; a file the page cannot read here begins with nothing, and the server reads it as usual.
 */

/** The bytes of a plain file the page reads, and the letters of its beginning it keeps. */
const readLimit = 800_000;
const textLimit = 60_000;

export async function fileOpening(file: File): Promise<string | null> {
  try {
    const name = file.name.toLowerCase();
    const text = name.endsWith('.fb2.zip') ? await fb2FromZip(file)
      : name.endsWith('.fb2') ? fb2Text(xmlText(await bytesOf(file.slice(0, readLimit))))
        : name.endsWith('.epub') ? await epubText(await bytesOf(file))
          : name.endsWith('.docx') ? docxText(await zipText(await bytesOf(file), 'word/document.xml'))
            : name.endsWith('.odt') ? markupText(await zipText(await bytesOf(file), 'content.xml'))
              : name.endsWith('.rtf') ? rtfText(await bytesOf(file.slice(0, readLimit)))
                : name.endsWith('.txt') || name.endsWith('.md') ? new TextDecoder().decode(await bytesOf(file.slice(0, readLimit)))
                  : null;
    return text ? text.slice(0, textLimit) : null;
  } catch {
    return null;
  }
}

const entities: Record<string, string> = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: ' ', laquo: '«', raquo: '»', mdash: '—', ndash: '–', hellip: '…' };

/** The text of XML or HTML: tags are spaces, the references are their letters. */
function markupText(markup: string | undefined) {
  if (!markup) return null;
  return markup
    .replace(/<head[\s>][\s\S]*?<\/head>/giu, ' ')
    .replace(/<(script|style)[\s>][\s\S]*?<\/\1>/giu, ' ')
    .replace(/<[^>]*>/gu, ' ')
    .replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/giu, (whole, code: string) => {
      if (code[0] === '#') {
        const point = code[1] === 'x' || code[1] === 'X' ? Number.parseInt(code.slice(2), 16) : Number(code.slice(1));
        return Number.isFinite(point) && point > 0 && point <= 0x10ffff ? String.fromCodePoint(point) : ' ';
      }
      return entities[code.toLowerCase()] ?? whole;
    });
}

/** XML bytes by the encoding their declaration names: an FB2 of a Russian book is often windows-1251. */
function xmlText(bytes: Uint8Array) {
  const head = new TextDecoder('latin1').decode(bytes.subarray(0, 200));
  const declared = /encoding\s*=\s*["']([\w-]+)["']/iu.exec(head)?.[1];
  try {
    return new TextDecoder(declared ?? 'utf-8').decode(bytes);
  } catch {
    return new TextDecoder().decode(bytes);
  }
}

/** The text of an FB2: its first body, without the pictures it carries. */
function fb2Text(xml: string) {
  const body = xml.search(/<body[\s>]/u);
  return markupText((body >= 0 ? xml.slice(body) : xml).replace(/<binary[\s\S]*?<\/binary>/gu, ' '));
}

async function fb2FromZip(file: File) {
  const entry = zipEntries(await bytesOf(file)).find((each) => each.name.toLowerCase().endsWith('.fb2'));
  const bytes = entry ? await entry.read() : undefined;
  return bytes ? fb2Text(xmlText(bytes)) : null;
}

/** The text of a Word body: its runs, a paragraph a new line. */
function docxText(xml: string | undefined) {
  if (!xml) return null;
  return markupText([...xml.matchAll(/<w:t(?:\s[^>]*)?>([^<]*)<\/w:t>|<\/w:p>/gu)].map((match) => match[1] ?? '\n').join(''));
}

/** The text of an EPUB: its documents in the order of its spine, until the beginning is long enough. */
async function epubText(bytes: Uint8Array) {
  const entries = zipEntries(bytes);
  const byName = new Map(entries.map((entry) => [entry.name, entry]));
  const read = async (entry: ZipEntry | undefined) => {
    const content = entry ? await entry.read() : undefined;
    return content ? new TextDecoder().decode(content) : '';
  };
  const container = await read(byName.get('META-INF/container.xml'));
  const opfPath = /full-path\s*=\s*["']([^"']+)["']/u.exec(container)?.[1];
  const opf = opfPath ? await read(byName.get(opfPath)) : '';
  const base = opfPath?.includes('/') ? opfPath.slice(0, opfPath.lastIndexOf('/') + 1) : '';
  const items = new Map([...opf.matchAll(/<item\b[^>]*>/gu)].map((match) => {
    const tag = match[0];
    return [/\bid\s*=\s*["']([^"']+)["']/u.exec(tag)?.[1] ?? '', /\bhref\s*=\s*["']([^"']+)["']/u.exec(tag)?.[1] ?? ''] as const;
  }));
  const spine = [...opf.matchAll(/<itemref\b[^>]*\bidref\s*=\s*["']([^"']+)["']/gu)].map((match) => items.get(match[1]!) ?? '')
    .filter(Boolean).map((href) => base + decodeURIComponent(href.split('#')[0]!));
  const documents = spine.length ? spine
    : entries.map((entry) => entry.name).filter((name) => /\.x?html?$/iu.test(name)).sort();
  let text = '';
  for (const path of documents) {
    text += ` ${markupText(await read(byName.get(path))) ?? ''}`;
    if (text.length >= textLimit) break;
  }
  return text;
}

/** Groups of an RTF that are no text of the book: the fonts, colours, styles, the info, pictures and the hidden ones. */
const skipped = /^\\(?:\*|fonttbl|colortbl|stylesheet|info|pict|listtable|listoverridetable|rsidtbl|generator|themedata|colorschememapping|latentstyles|datastore|xmlnstbl)/u;

/** The text of an RTF: its letters by the code page it names, `\u` letters as they are, `\par` a new line. */
function rtfText(bytes: Uint8Array) {
  const source = new TextDecoder('latin1').decode(bytes);
  const page = /\\ansicpg(\d+)/u.exec(source)?.[1] ?? '1251';
  let decoder: TextDecoder;
  try {
    decoder = new TextDecoder(`windows-${page}`);
  } catch {
    decoder = new TextDecoder('windows-1251');
  }
  let text = '';
  let pending: number[] = [];
  const flush = () => {
    if (pending.length) text += decoder.decode(new Uint8Array(pending));
    pending = [];
  };
  let skipUntil = -1;
  let depth = 0;
  for (let at = 0; at < source.length; at += 1) {
    const char = source[at]!;
    if (char === '{') {
      depth += 1;
      if (skipUntil < 0 && skipped.test(source.slice(at + 1, at + 40))) skipUntil = depth;
      continue;
    }
    if (char === '}') {
      if (skipUntil === depth) skipUntil = -1;
      depth -= 1;
      continue;
    }
    if (skipUntil >= 0) continue;
    if (char === '\\') {
      const next = source[at + 1];
      if (next === "'") {
        pending.push(Number.parseInt(source.slice(at + 2, at + 4), 16));
        at += 3;
        continue;
      }
      flush();
      if (next === '\\' || next === '{' || next === '}') {
        text += next;
        at += 1;
        continue;
      }
      const word = /^\\([a-z]+)(-?\d+)? ?/u.exec(source.slice(at, at + 40));
      if (!word) continue;
      at += word[0].length - 1;
      if (word[1] === 'u' && word[2]) {
        const point = Number(word[2]);
        text += String.fromCharCode(point < 0 ? point + 65_536 : point);
        // The letter for a reader that knows no `\u`, which stands right after it.
        if (source[at + 1] === '?') at += 1;
      } else if (word[1] === 'par' || word[1] === 'line') {
        text += '\n';
      }
      continue;
    }
    if (char === '\r' || char === '\n') continue;
    // A letter written as it is, not as `\'hh`, is a byte of the code page too.
    if (char.charCodeAt(0) >= 128) {
      pending.push(char.charCodeAt(0));
      continue;
    }
    flush();
    text += char;
    if (text.length >= textLimit) break;
  }
  flush();
  return text;
}
