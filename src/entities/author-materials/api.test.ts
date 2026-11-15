import { describe, expect, it } from 'vitest';
import { materialOriginLine, obsidianOrigin } from './api';

// specs/2026-11-15-desk-obsidian-notes.md ON2.1: the origin of a record from a note of Obsidian is one phrase — the path of the
// note —; every other source says the words it said before, to the sign.
describe('the origin of a record', () => {
  it('names the note of Obsidian a record came from', () => {
    const note = { kind: 'character' as const, section: 'Персонажи', sourceKind: 'obsidian' as const, sourceName: 'Персонажи/Мирон Сватов.md' };
    expect(obsidianOrigin(note)).toBe('из Obsidian: Персонажи/Мирон Сватов.md');
    expect(materialOriginLine(note)).toBe('Герой · из Obsidian: Персонажи/Мирон Сватов.md');
  });

  it('says the other sources as before', () => {
    expect(materialOriginLine({ kind: 'place', section: 'Места', sourceKind: 'xlsx', sourceName: 'навыки.xlsx' })).toBe('Место · из „навыки.xlsx“, лист „Места“');
    expect(materialOriginLine({ kind: 'place', section: 'Места', sourceKind: 'csv', sourceName: 'места.csv' })).toBe('Место · из „места.csv“, лист „Места“');
    expect(materialOriginLine({ kind: 'skill', section: 'Магия', sourceKind: 'docx', sourceName: 'мир.docx' })).toBe('Навык · из „мир.docx“, раздел „Магия“');
    expect(materialOriginLine({ kind: 'item', section: '', sourceKind: 'text', sourceName: '' })).toBe('Предмет · из вставленного текста');
    expect(materialOriginLine({ kind: 'character', section: '', sourceKind: 'manual', sourceName: '' })).toBe('Герой · добавлено вручную');
    for (const sourceKind of ['xlsx', 'docx', 'csv', 'text', 'manual'] as const) {
      expect(obsidianOrigin({ sourceKind, sourceName: 'навыки.xlsx' })).toBeNull();
    }
  });
});
