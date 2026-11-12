import { readdirSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { createAppRoute, surfaces } from './app-route';
import { formatPageTitle, holdTitlePart, markHiddenTab, sectionTitle, sectionTitles, usePageTitlePart } from './page-title';

// specs/2026-11-10-ux1-shell.md SH1: one owner of the title of the tab.

const releases: (() => void)[] = [];
const hold = (...args: Parameters<typeof holdTitlePart>) => releases.push(holdTitlePart(...args));

function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: state });
  document.dispatchEvent(new Event('visibilitychange'));
}

afterEach(() => {
  releases.splice(0).forEach((release) => release());
  setVisibility('visible');
});

describe('the title of the tab', () => {
  it('reads the place from its parts, the object first and the saga last', () => {
    expect(formatPageTitle({})).toBe('CanonKeeper — помощник для вашей саги');
    expect(formatPageTitle({ section: 'Чат' })).toBe('Чат — CanonKeeper');
    expect(formatPageTitle({ section: 'Письмо', saga: 'Хроники Белого порта' })).toBe('Письмо · Хроники Белого порта — CanonKeeper');
    expect(formatPageTitle({ object: 'Глава 12', section: 'Письмо', saga: 'Хроники Белого порта' }))
      .toBe('Глава 12 · Письмо · Хроники Белого порта — CanonKeeper');
    expect(formatPageTitle({ mark: 'Ответ готов', section: 'Чат', saga: 'Хроники Белого порта' }))
      .toBe('● Ответ готов · Чат · Хроники Белого порта — CanonKeeper');
    expect(formatPageTitle({ mark: 'Сага готова' })).toBe('● Сага готова · CanonKeeper — помощник для вашей саги');
  });

  it('writes the parts held and drops a part its holder lets go', () => {
    hold('section', 'Письмо');
    hold('saga', 'Хроники Белого порта');
    const object = holdTitlePart('object', 'Глава 12');
    expect(document.title).toBe('Глава 12 · Письмо · Хроники Белого порта — CanonKeeper');
    object();
    expect(document.title).toBe('Письмо · Хроники Белого порта — CanonKeeper');
  });

  it('gives a part back to its former holder when the latest leaves', () => {
    hold('section', 'Чат');
    const next = holdTitlePart('section', 'Письмо');
    expect(document.title).toBe('Письмо — CanonKeeper');
    next();
    expect(document.title).toBe('Чат — CanonKeeper');
  });

  it('keeps the mark of a hidden tab over a new page and drops it when the author comes back (R6.3, FS4)', () => {
    hold('section', 'Чат');
    setVisibility('hidden');
    markHiddenTab('Ответ готов');
    expect(document.title).toBe('● Ответ готов · Чат — CanonKeeper');
    const section = holdTitlePart('section', 'Письмо');
    expect(document.title).toBe('● Ответ готов · Письмо — CanonKeeper');
    setVisibility('visible');
    expect(document.title).toBe('Письмо — CanonKeeper');
    section();
  });

  it('holds a part only while the component stands and only once the name has come', () => {
    hold('section', 'Рисование');
    const view = renderHook(({ name }: { name?: string }) => usePageTitlePart('object', name), { initialProps: {} as { name?: string } });
    expect(document.title).toBe('Рисование — CanonKeeper');
    view.rerender({ name: 'Кира у северных ворот' });
    expect(document.title).toBe('Кира у северных ворот · Рисование — CanonKeeper');
    view.unmount();
    expect(document.title).toBe('Рисование — CanonKeeper');
  });
});

describe('the section of the address (SH1.2)', () => {
  it('names every surface of the routes', () => {
    expect(Object.keys(sectionTitles).sort()).toEqual([...surfaces].sort());
    for (const surface of surfaces) {
      if (surface === 'home') expect(sectionTitles[surface]).toBeNull();
      else expect(sectionTitles[surface]?.trim()).toBeTruthy();
    }
  });

  it('tells the reader from the shelf and the pages of sign-in apart', () => {
    expect(sectionTitle(createAppRoute('/manuscript/books', { projectId: 'p1' }))).toBe('Книги');
    expect(sectionTitle(createAppRoute('/manuscript/books', { projectId: 'p1', mode: 'read' }))).toBe('Чтение');
    expect(sectionTitle(createAppRoute('/auth/login'))).toBe('Вход');
    expect(sectionTitle(createAppRoute('/auth/register'))).toBe('Регистрация');
    expect(sectionTitle(createAppRoute('/auth/verify-email'))).toBe('Подтверждение почты');
    // specs/2026-11-10-ux3-auth.md AU1.1: the step after the registration.
    expect(sectionTitle(createAppRoute('/auth/check-email'))).toBe('Подтвердите почту');
    expect(sectionTitle(createAppRoute('/auth/reset-pass' + 'word'))).toBe('Новый пароль');
    expect(sectionTitle(createAppRoute('/writing', { projectId: 'p1', draft: 'd1' }))).toBe('Письмо');
    expect(sectionTitle(createAppRoute('/'))).toBeNull();
  });
});

describe('one writer of document.title (SH1.1)', () => {
  const root = resolve(__dirname, '../../..');
  const sources = (directory: string): string[] => readdirSync(directory, { recursive: true, encoding: 'utf8' })
    .map((path) => join(directory, path))
    .filter((path) => /\.tsx?$/u.test(path) && !/\.test\.tsx?$/u.test(path));

  it('lets no other module of the product write the title', () => {
    const writers = sources(join(root, 'src'))
      .filter((path) => !path.endsWith(join('shared', 'navigation', 'page-title.ts')))
      .filter((path) => /document\.title\s*=(?!=)/u.test(readFileSync(path, 'utf8')))
      .map((path) => path.slice(root.length + 1));
    expect(writers).toEqual([]);
  });
});
