import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';
import { dictionary } from '../shared/copy/dictionary';

/**
 * The name of the product is one word (specs/2026-11-10-ux1-shell.md SH5.1): in the strings of the site, the tab, the top
 * bars, the menus, the first screen, «Страница не найдена», the screen of an outage, the letters of the service and the
 * table of the pages of the voice. The texts of the legal documents are kept as they are, whatever they say. The other
 * spellings are the row «имя продукта» of the dictionary (specs/2026-11-11-ux2-dictionary.md DC1.1); the table names them.
 */
const root = process.cwd();
const keptAsTheyAre = join('src', 'features', 'legal', 'content');
const theTable = join('src', 'shared', 'copy', 'dictionary.ts');
const otherSpellings = dictionary.find((row) => row.thing === 'имя продукта')!.hidden.map((word) => word.pattern);

function files(directory: string, pick: (path: string) => boolean): string[] {
  if (!existsSync(join(root, directory))) return [];
  return readdirSync(join(root, directory)).flatMap((name) => {
    const path = join(directory, name);
    if (path === keptAsTheyAre || path === theTable) return [];
    if (statSync(join(root, path)).isDirectory()) return files(path, pick);
    return pick(path) ? [path] : [];
  });
}

describe('the name of the product', () => {
  it('is CanonKeeper everywhere but in the texts of the legal documents', () => {
    const sources = [
      'index.html',
      join('backend', 'internal', 'delivery', 'http', 'voice', 'pages.json'),
      ...files('src', (path) => /\.(tsx?|css|json|md)$/u.test(path)),
      // The letters of the service: their templates and their sender live in the Go code of the backend.
      ...files('backend', (path) => path.endsWith('.go') && !path.endsWith('_test.go')),
    ];
    // The build of the open code has no closed files: what is not there is not read.
    const found = sources.filter((path) => existsSync(join(root, path))).filter((path) => otherSpellings.some((spelling) => spelling.test(readFileSync(join(root, path), 'utf8')))).map((path) => relative('', path));
    expect(found).toEqual([]);
  });
});
