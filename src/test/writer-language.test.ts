import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import ts from 'typescript';

/**
 * Writer language (specs/2026-10-28-writer-first-success.md, UC5 R5.1, R5.5): the strings the
 * interface shows speak of sagas, books and questions, not of the system's internal words.
 */
const internalWords = [
  /агент/i,
  /поручени/i,
  /рабоч\p{L}* мест/iu,
  /рабоч\p{L}* област/iu,
  /ревизи/i,
  /индексац/i,
  /Проект загружается/,
];

/** R5.5: the impersonation texts only an administrator sees stay as they are. */
const impersonationFiles = new Set(['src/features/auth/AdminImpersonation.tsx']);
const impersonationTexts = new Set([
  // App.tsx#IdentityTransition — shown only while an administrator starts or ends impersonation.
  'Переключение рабочей области',
  'Переключаем рабочую область…',
  'Данные предыдущей рабочей области скрыты, пока не подтвердится новая учётная запись.',
]);

const root = process.cwd();

function sourceFiles() {
  return readdirSync(join(root, 'src'), { recursive: true, encoding: 'utf8' })
    .map((path) => `src/${path}`)
    .filter((path) => /\.tsx?$/.test(path))
    .filter((path) => !/\.test\.tsx?$/.test(path) && !/\.d\.ts$/.test(path))
    .filter((path) => !path.startsWith('src/test/') && !path.startsWith('src/api/mock/') && !path.includes('/generated/'))
    .filter((path) => !impersonationFiles.has(path));
}

function visibleStrings(path: string) {
  const file = ts.createSourceFile(path, readFileSync(join(root, path), 'utf8'), ts.ScriptTarget.Latest, true,
    path.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
  const strings: string[] = [];
  const visit = (node: ts.Node) => {
    if (ts.isStringLiteralLike(node) || ts.isTemplateLiteralToken(node)) strings.push(node.text);
    else if (ts.isJsxText(node) && node.text.trim()) strings.push(node.text.trim());
    ts.forEachChild(node, visit);
  };
  visit(file);
  return strings;
}

describe('writer language', () => {
  it('keeps the internal words out of the interface strings', () => {
    const found = sourceFiles().flatMap((path) => visibleStrings(path)
      .filter((text) => !impersonationTexts.has(text) && internalWords.some((word) => word.test(text)))
      .map((text) => `${path}: ${text}`));
    expect(found).toEqual([]);
  });
});
