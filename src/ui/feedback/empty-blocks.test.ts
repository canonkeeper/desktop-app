import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

// specs/2026-11-10-ux2-patterns-states.md PS2.1–PS2.3: an empty section says what will appear, why and its first step
// through the one `EmptyState`, nothing by a filter or a search through `EmptyFilter`. A section keeps no empty block of its
// own: the words that open one — «Пока нет…», «Пока ничего…», «Ничего не найдено…» — stand in `src/features` and
// `src/pages` only inside those two components, or in the places of this list, each for its reason.

const root = path.resolve(import.meta.dirname, '../..');

function sources(directory: string): string[] {
  return readdirSync(directory).flatMap((name) => {
    const file = path.join(directory, name);
    if (statSync(file).isDirectory()) return name === 'generated' || name === 'test' || name === 'mock' ? [] : sources(file);
    return /\.tsx?$/u.test(name) && !/\.test(-helpers)?\.tsx?$/u.test(name) ? [file] : [];
  });
}

/** The code without the elements of the two components: what they say is theirs. */
function outsideTheComponents(text: string) {
  let rest = '';
  let index = 0;
  for (;;) {
    const start = text.slice(index).search(/<Empty(State|Filter)\b/u);
    if (start < 0) return rest + text.slice(index);
    rest += text.slice(index, index + start);
    let depth = 0;
    let at = index + start;
    for (; at < text.length; at += 1) {
      const char = text[at];
      if (char === '{') depth += 1;
      else if (char === '}') depth -= 1;
      else if (depth === 0 && text.startsWith('/>', at)) break;
    }
    index = at + 2;
  }
}

const opening = /[Пп]ока нет [а-яё]+|[Пп]ока ничего|Ничего не найдено|Ничего не нашлось/u;

/** The places that keep such words, and why. */
const exceptions: Readonly<Record<string, string>> = {
  // «Что помнит помощник» is the page of ux2-memory-page: its blocks are its own (the brief of this slice).
  'features/saga/materials/MaterialsPart.tsx': 'часть «Ваши материалы» страницы «Что помнит помощник» — слайс ux2-memory-page',
  // The words of a refusal and of a step of the upload: an answer of the server, not an empty section.
  'features/manuscript/shelf/ManuscriptShelfPage.tsx': 'ответ сервера на «Скачать» пустой книги, не раздел',
  'features/manuscript/shelf/import-queue.ts': 'строка о порядке книг в окне загрузки, не раздел',
};

describe('the empty blocks of the sections', () => {
  it('go through EmptyState and EmptyFilter but in the places of the list', () => {
    const files = [...sources(path.join(root, 'features')), ...sources(path.join(root, 'pages'))]
      .map((file) => path.relative(root, file).split(path.sep).join('/'))
      .filter((file) => !file.startsWith('features/admin/'));
    const own = files.filter((file) => opening.test(outsideTheComponents(readFileSync(path.join(root, file), 'utf8'))));
    expect(own.filter((file) => !(file in exceptions))).toEqual([]);
    // The open tree has no closed pages of the site: an exception counts where its file is.
    expect(Object.keys(exceptions).filter((file) => existsSync(path.join(root, file)) && !own.includes(file))).toEqual([]);
  });

  it('reads the words of an empty block inside its component as the component’s', () => {
    expect(opening.test(outsideTheComponents('<EmptyState icon={<Library size={28} />} title="В саге пока нет книг" />'))).toBe(false);
    expect(opening.test(outsideTheComponents('<div><h2>Пока нет заметок</h2></div>'))).toBe(true);
  });
});
