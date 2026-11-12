import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

/**
 * The guard of the colours of the site (specs/2026-11-12-ux11-dark-core.md TH3.1, specs/2026-11-12-ux11-dark-sections.md
 * DS3): every style and every piece of markup in `src` paints by the tokens of src/styles/tokens.css only, so the dark theme
 * of the site paints it whole. A colour written in place would stay light on a dark site.
 */
const root = process.cwd();

/** The sets of colours themselves: the only files that write a colour by its value. */
const sets = new Set(['src/styles/tokens.css', 'src/styles/reading-themes.css']);

/**
 * The places that keep a written colour, each for its reason: a value of data, a mask, the shading computed by the code of
 * the scene. «It was so» is no reason. Key: `<file>:<the colour as written>`.
 */
const exceptions: Readonly<Record<string, string>> = {
  'src/features/drawing/scene.ts:rgb(': 'shade: a face of the camera of «Сменить ракурс» is its base from --ck-angle-* times the light of the face',
  'src/entities/illustrations/ui/region-shape.ts:#000': 'mask: the PNG of the brush for the artist — opaque is «leave», transparent is «redraw»',
  'src/features/drawing/RegionCanvas.tsx:black': 'mask: an SVG <mask> of the brush — black hides, white shows',
  'src/features/drawing/RegionCanvas.tsx:white': 'mask: an SVG <mask> of the brush — black hides, white shows',
  'src/entities/illustrations/ui/cells.module.css:#fff': 'mask: .maskCell is drawn in an SVG <mask> — white shows the old picture',
  'src/features/manuscript/shelf/cover-colours.ts:#4F46E5': 'data: a colour of a cover, kept with the book by the server',
  'src/features/manuscript/shelf/cover-colours.ts:#285f58': 'data: a colour of a cover, kept with the book by the server',
  'src/features/manuscript/shelf/cover-colours.ts:#4c3a5d': 'data: a colour of a cover, kept with the book by the server',
  'src/features/manuscript/shelf/cover-colours.ts:#30472b': 'data: a colour of a cover, kept with the book by the server',
  'src/features/manuscript/shelf/cover-colours.ts:#8a6500': 'data: a colour of a cover, kept with the book by the server',
  'src/api/mock/repository/state.ts:#4F46E5': 'data: the colour of a cover of a book of the mock server',
  'src/api/mock/repository/state.ts:#285f58': 'data: the colour of a cover of a book of the mock server',
  'src/api/mock/repository/state.ts:#4c3a5d': 'data: the colour of a cover of a book of the mock server',
  'src/api/mock/repository/state.ts:#30472b': 'data: the colour of a cover of a book of the mock server',
  'src/api/mock/repository/books.ts:#4F46E5': 'data: the colour of a cover of a new book of the mock server',
};

const colour = /#[0-9a-f]{3,8}\b|\b(?:rgba?|hsla?)\(|(?<![\w-])(?:white|black|red|green|blue|gray|grey|silver|yellow|orange)(?![\w-])/giu;
/** A token with a value of its own in case it is missing: a light colour that would show on a dark site (DS3.2). */
const fallback = /var\(\s*--ck-[\w-]+\s*,\s*(?:#|(?:rgba?|hsla?)\(|(?:white|black|red|green|blue|gray|grey)\b)/giu;

function files(directory: string): string[] {
  return readdirSync(path.join(root, directory)).flatMap((name) => {
    const file = `${directory}/${name}`;
    if (statSync(path.join(root, file)).isDirectory()) return files(file);
    return /\.(css|tsx?)$/u.test(name) && !/\.test\.tsx?$/u.test(name) && !sets.has(file) ? [file] : [];
  });
}

/** The text without its comments: a comment may name a colour, the code may not. */
function code(text: string, css: boolean) {
  const block = text.replace(/\/\*[\s\S]*?\*\//gu, '');
  return css ? block : block.replace(/(^|[^:'"`])\/\/.*$/gmu, '$1');
}

/** The colours written in a file, as keys `<file>:<colour>`. */
export function writtenColours(file: string, text: string) {
  const plain = code(text, file.endsWith('.css'));
  const found: string[] = [];
  for (const match of plain.matchAll(colour)) {
    // `white-space` is no colour.
    const around = plain.slice(Math.max(0, match.index - 1), match.index + match[0].length + 7);
    if (/^.?white-space/u.test(around)) continue;
    found.push(`${file}:${match[0]}`);
  }
  return found;
}

/** The tree of the open export (scripts/open-source/check.mjs runs its tests as the desktop edition). */
const openExport = process.env.CK_EDITION === 'desktop';
const all = files('src');
const texts = new Map(all.map((file) => [file, readFileSync(path.join(root, file), 'utf8')]));

describe('the colours of the site', () => {
  it('are the tokens everywhere in src, outside the list of exceptions with a reason', () => {
    expect(all.length).toBeGreaterThan(openExport ? 300 : 500);
    const found = all.flatMap((file) => writtenColours(file, texts.get(file)!)).filter((key) => !exceptions[key]);
    expect(found).toEqual([]);
  });

  it('would find a written colour that came back to a feature', () => {
    expect(writtenColours('src/features/drawing/Drawings.module.css', '.tileMark { background: rgb(24 24 27 / 86%); color: #fff; }'))
      .toEqual(['src/features/drawing/Drawings.module.css:rgb(', 'src/features/drawing/Drawings.module.css:#fff']);
    expect(writtenColours('src/entities/x/y.tsx', "/* #fff in a comment */ const look = { color: 'white', whiteSpace: 'nowrap' };"))
      .toEqual(['src/entities/x/y.tsx:white']);
    expect(writtenColours('src/features/x/y.module.css', '.x { white-space: nowrap; color: var(--ck-foreground); }')).toEqual([]);
  });

  it('keeps no token with a light value of its own', () => {
    expect(all.flatMap((file) => [...code(texts.get(file)!, file.endsWith('.css')).matchAll(fallback)].map((match) => `${file}: ${match[0]}`))).toEqual([]);
  });

  it('keeps no exception that has no colour left to excuse, and gives each its reason', () => {
    for (const [key, reason] of Object.entries(exceptions)) {
      const [file, written] = [key.slice(0, key.lastIndexOf(':')), key.slice(key.lastIndexOf(':') + 1)];
      // The open export (desktop edition) leaves the closed code of the site out; in the whole tree every file is there.
      if (!texts.has(file) && openExport) continue;
      expect([key, writtenColours(file, texts.get(file)!).includes(key)]).toEqual([key, true]);
      expect([key, /^(?:data|mask|shade): \S/u.test(reason)]).toEqual([key, true]);
      expect(written).not.toBe('');
    }
  });
});
