import { describe, expect, it } from 'vitest';
import { createAppRoute, surfaces } from '../../shared/navigation/app-route';
import { sectionTitles } from '../../shared/navigation/page-title';
import { sagaSections, sectionGroups, sectionTable } from '../saga-sections';
import { activeSection, contentWidth, narrowColumn, sagaInAddress, sectionWord, shellOfSurface } from './shell-routes';

// specs/2026-11-10-ux2-shell-core.md SC1.1, SC1.2, SC2.1: one table of the surfaces and one table of the sections.
describe('the table of the shell', () => {
  it('names every surface of the routes', () => {
    expect(Object.keys(shellOfSurface).sort()).toEqual([...surfaces].sort());
  });

  it('puts every page of a signed-in author in the shell and the pages before the product apart', () => {
    const shelled = surfaces.filter((surface) => shellOfSurface[surface]);
    expect(shelled.sort()).toEqual(['account', 'chat', 'drawing', 'legal', 'manuscript', 'memory', 'notes',
      'promo', 'saga', 'tariff', 'unknown', 'writing']);
  });

  it('marks the reader as «Книги» and a page without a saga as no section', () => {
    expect(activeSection(createAppRoute('/manuscript/books', { mode: 'read' }))).toBe('manuscript');
    expect(activeSection(createAppRoute('/memory'))).toBe('memory');
    expect(activeSection(createAppRoute('/tariff'))).toBeNull();
    expect(sagaInAddress(createAppRoute('/notes'))).toBe(true);
    expect(sagaInAddress(createAppRoute('/account'))).toBe(false);
  });

  it('keeps the sections narrow on an open chapter, an open drawing and the reader', () => {
    expect(narrowColumn(createAppRoute('/writing', { draft: 'd1' }))).toBe(true);
    expect(narrowColumn(createAppRoute('/writing'))).toBe(false);
    expect(narrowColumn(createAppRoute('/drawing', { drawing: 'x1' }))).toBe(true);
    expect(narrowColumn(createAppRoute('/drawing', { drawing: 'new' }))).toBe(false);
    expect(narrowColumn(createAppRoute('/manuscript/books', { mode: 'read' }))).toBe(true);
    expect(narrowColumn(createAppRoute('/manuscript/books', { projectId: 'p1' }))).toBe(false);
    expect(narrowColumn(createAppRoute('/saga'))).toBe(false);
  });

  // specs/2026-11-10-ux2-patterns-states.md PS1.2: two widths, by the surface.
  it('gives the reading and the forms 720 px and the lists and the workplaces the whole width', () => {
    const reading = surfaces.filter((surface) => shellOfSurface[surface] && contentWidth({ surface }) === 'reading');
    expect(reading.sort()).toEqual(['account', 'legal', 'promo', 'tariff', 'unknown']);
    for (const surface of ['manuscript', 'saga', 'memory', 'chat', 'writing', 'drawing', 'notes'] as const) {
      expect(contentWidth({ surface })).toBe('wide');
    }
  });

  it('names a section in the top bar by the word of its menu item, the same word as the tab', () => {
    for (const surface of ['manuscript', 'saga', 'memory', 'chat', 'writing', 'drawing', 'notes'] as const) {
      expect(sectionWord({ surface }, null)).toBe(sectionTitles[surface]);
    }
    expect(sectionWord({ surface: 'tariff' }, 'Тариф')).toBe('Тариф');
  });
});

describe('the table of the sections', () => {
  it('has three groups in the order of the menu and no «Чтение»', () => {
    expect(sectionGroups.map((group) => group.label)).toEqual(['Сага', 'Работа', 'Моё']);
    expect(sectionGroups.map((group) => sectionTable.filter((entry) => entry.group === group.group).map((entry) => entry.label))).toEqual([
      ['Книги', 'Карта', 'Что помнит помощник'],
      ['Чат', 'Письмо', 'Рисунки'],
      ['Заметки', 'Недавно удалённые'],
    ]);
    expect(sectionTable.map((entry) => entry.label)).not.toContain('Чтение');
    expect(sagaSections).toEqual(sectionTable.map((entry) => entry.section));
  });

  it('gives every page section its path and «Недавно удалённые» a window', () => {
    expect(sectionTable.map((entry) => [entry.section, entry.path])).toEqual([
      ['manuscript', '/manuscript/books'], ['saga', '/saga'], ['memory', '/memory'], ['chat', '/chat'], ['writing', '/writing'],
      ['drawing', '/drawing'], ['notes', '/notes'], ['deleted', null],
    ]);
  });
});
