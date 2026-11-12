import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

// specs/2026-11-10-ux2-contact.md CT1.2, invariant «no place of a signed-in author with a working window opens the mail
// program»: a letter to support is written by one component where there is no window; the contacts of the service and the
// admin panel keep their addresses. Nothing else in src builds a `mailto:` link.

const root = path.resolve(import.meta.dirname, '../..');

function sources(directory: string): string[] {
  return readdirSync(directory).flatMap((name) => {
    const file = path.join(directory, name);
    if (statSync(file).isDirectory()) return name === 'generated' || name === 'test' || name === 'mock' ? [] : sources(file);
    return /\.(ts|tsx)$/u.test(name) && !/\.test(-helpers)?\.(ts|tsx)$/u.test(name) ? [file] : [];
  });
}

const allowed = new Set([
  'shared/support/support-contact.ts',
  'ui/feedback/SupportReference.tsx',
  'entities/contacts/ContactLinks.tsx',
]);

describe('a letter to support', () => {
  it('is built only by the letter of support, the contacts of the service and the admin panel', () => {
    const files = sources(root).map((file) => path.relative(root, file).split(path.sep).join('/'));
    const letters = files.filter((file) => readFileSync(path.join(root, file), 'utf8').includes('mailto:')
      && !allowed.has(file) && !file.startsWith('features/admin/'));
    expect(letters).toEqual([]);
  });
});
