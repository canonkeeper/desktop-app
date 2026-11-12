import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

// specs/2026-11-10-ux2-patterns-actions.md, Tests: the common rules of actions hold in every section — one area of the
// toasts, one confirmation, no question of the browser. The admin panel keeps its own area and its own windows (Не входит).

const root = path.resolve(import.meta.dirname, '../..');

function sources(directory: string): string[] {
  return readdirSync(directory).flatMap((name) => {
    const file = path.join(directory, name);
    if (statSync(file).isDirectory()) return name === 'generated' || name === 'mock' ? [] : sources(file);
    return /\.(ts|tsx)$/u.test(name) && !/\.test(-helpers)?\.(ts|tsx)$/u.test(name) ? [file] : [];
  });
}

const files = sources(root)
  .map((file) => ({ file: path.relative(root, file).split(path.sep).join('/'), text: readFileSync(file, 'utf8') }))
  .filter(({ file }) => !file.startsWith('features/admin/'));

describe('one area of the toasts (UC1)', () => {
  it('is the root one: a section has no area and no queue of its own', () => {
    // The kit itself, and the root that owns the one queue of the tab.
    const owners = new Set(['ui/feedback/Toast.tsx', 'ui/feedback/useToastQueue.ts', 'App.tsx']);
    const own = files.filter(({ file, text }) => !owners.has(file) && /<ToastRegion\b|\buseToastQueue\(|<ToastsProvider\b/u.test(text));
    expect(own.map(({ file }) => file)).toEqual([]);
  });
});

/**
 * The windows that ask a question of the author and still are not the one confirmation, each with its reason — none now.
 * A window of a choice between several actions or a form is not a question (PA2.2): its heading asks nothing.
 */
const ownQuestions = new Map<string, string>([]);

describe('one confirmation (UC2)', () => {
  it('asks through the one confirmation, never window.confirm', () => {
    expect(files.filter(({ text }) => /\bwindow\.confirm\(|(?<![.\w])confirm\(\s*['"`]/u.test(text)).map(({ file }) => file)).toEqual([]);
  });

  it('has no window of a question of its own: a heading or a name that asks stands only in Confirm', () => {
    const window = /<(Dialog|Sheet|ActionSheet)\b/u;
    const question = /\?\s*<\/h[1-6]>|\blabel=(?:"[^"]*\?"|\{`[^`]*\?`\})/u;
    const own = files.filter(({ file, text }) => file !== 'ui/overlays/Confirm.tsx' && !ownQuestions.has(file) && window.test(text) && question.test(text));
    expect(own.map(({ file }) => file)).toEqual([]);
  });

  it('says «Отмена» for the way out of a question, not «Закрыть», «Понятно» or «Готово»', () => {
    const confirm = files.find(({ file }) => file === 'ui/overlays/Confirm.tsx')!.text;
    expect(confirm).toContain('>Отмена</Button>');
  });
});

describe('one phrase and one place of a deletion (UC3)', () => {
  it('promises 7 days only where the server gives back — a saga, a book, a drawing — and says no other promise', () => {
    const returns = files.filter(({ text }) => /deletionLines\.returns/u.test(text)).map(({ file }) => file).sort();
    // specs/2026-11-11-ux7-trash.md TB3.2: a whole drawing lies 7 days in «Недавно удалённые» as a book does. The drawings
    // are the site's: the open export has no such files.
    const drawings = ['features/drawing/Choice.tsx', 'features/drawing/DrawingWorkspace.tsx', 'features/drawing/deleted-line.tsx']
      .filter((name) => files.some(({ file }) => file === name));
    // The line after the deletion of a saga or a book says the same phrase as its window.
    expect(returns).toEqual([
      ...drawings, 'features/manuscript/shelf/ShelfLayers.tsx', 'features/manuscript/shelf/useDeletedSagaToast.tsx', 'ui/shell/SagaSettings.tsx',
    ]);
    // No other promise is said: the row «удалённое на 7 дней» of the dictionary (src/shared/copy/dictionary.ts) hides them.
  });

  it('has no bin without a word: a deletion is an item «Удалить …» of «⋯» with its word', () => {
    const bin = /<IconButton\b[^>]*aria-label=(?:"Удалить|\{`Удалить)/u;
    expect(files.filter(({ text }) => bin.test(text)).map(({ file }) => file)).toEqual([]);
  });
});
