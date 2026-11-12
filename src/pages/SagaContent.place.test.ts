import { describe, expect, it } from 'vitest';
import { createAppRoute } from '../shared/navigation/app-route';
import { sectionPlace } from './SagaContent';

// specs/2026-11-03-voice-assistant.md V2.4: a section without an object of its own names its place; «Рисунки» name their own
// (the picture open in «Мои картинки» — specs/2026-11-10-ux6-drawings-section.md DS2.1, drawings-route.test.ts).
describe('sectionPlace', () => {
  it('names the map with its node and the memory, and leaves the drawings to the section', () => {
    expect(sectionPlace('saga', createAppRoute('/saga', { projectId: 'p1', node: 'мара' }))).toEqual({ page: 'saga', entry: 'мара' });
    expect(sectionPlace('memory', createAppRoute('/memory', { projectId: 'p1' }))).toEqual({ page: 'memory' });
    expect(sectionPlace('drawing', createAppRoute('/drawing', { projectId: 'p1', view: 'mine', image: '12' }))).toBeNull();
  });
});
