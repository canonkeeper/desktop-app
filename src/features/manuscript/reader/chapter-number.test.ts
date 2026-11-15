import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { chapterHeading } from '../../../ui/copy';

// specs/2026-11-15-reader-chapter-number.md RN1.1, RN1.3: the number of a chapter and its title without it come from the
// server; the head of the reader only writes them.
describe('the head of a chapter', () => {
  it('writes the number and the title of the server', () => {
    expect(chapterHeading('12', 'Белый порт')).toBe('Глава 12. Белый порт');
    expect(chapterHeading('12', '')).toBe('Глава 12');
    expect(chapterHeading('XIV', 'Письмо')).toBe('Глава XIV. Письмо');
  });
});

// The pages that name a chapter by its number — the reader, the editor of the app, the search of chapters, «Недавно
// удалённые» — and the mock of their operations read no number from a title: the one rule is the server's
// (`sagamemory.TitledNumber`), and the mock keeps its copy in places.ts only, over the shared table (RN1.5). The search of
// the mock reads «глава 12» from the query the author typed, as the service of the server does — not from a title.
const pages = [
  'src/ui/copy.ts',
  'src/features/manuscript/reader/ManuscriptReaderMode.tsx',
  'src/features/manuscript/reader/ReaderContents.tsx',
  'src/features/manuscript/reader/ReaderNavigation.tsx',
  'src/features/manuscript/reader/ReaderChapter.tsx',
  'src/features/manuscript/reader/use-find-places.ts',
  'src/features/manuscript/editor/EditorScreen.tsx',
  'src/features/manuscript/editor/editor-words.ts',
  'src/features/manuscript/SearchPanel.tsx',
  'src/app/shell/recently-deleted-words.ts',
  'src/api/mock/repository/chapters.ts',
  'src/api/mock/repository/trash.ts',
  'src/api/mock/repository/saga.ts',
  'src/api/mock/repository/notes.ts',
];
/** A regular expression or a test of a string that reads «Глава …» of a title. */
const readsATitle = /\/\^?\(?\??:?[Гг]лава[^/\n]*\/[a-z]*|startsWith\(`Глава|formatReaderChapterTitle/u;

describe('a rule of the number of a chapter', () => {
  it.each(pages)('is not kept by %s', (file) => {
    expect(readFileSync(file, 'utf8')).not.toMatch(readsATitle);
  });

  it('is kept by the mock in one place, and its search reads only the query', () => {
    expect(readFileSync('src/api/mock/repository/places.ts', 'utf8').match(new RegExp(readsATitle, 'gu'))).toHaveLength(2);
    expect(readFileSync('src/api/mock/repository/reader.ts', 'utf8').match(new RegExp(readsATitle, 'gu'))).toEqual(['/^(?:глава\\s+)?(\\d+)$/']);
  });
});
