import { describe, expect, it } from 'vitest';
import { createAppRoute } from '../shared/navigation/app-route';
import { sectionPlace } from './ProjectWorkspace';

// specs/2026-11-03-voice-assistant.md V5.4: «Галерея» names the drawing open in its lightbox by number — «этот рисунок» of
// the voice; a number that is not one names nothing.
describe('sectionPlace', () => {
  it('names the open drawing of the gallery', () => {
    expect(sectionPlace('gallery', createAppRoute('/gallery', { projectId: 'p1', image: '12' }))).toEqual({ page: 'gallery', imageNumber: 12 });
    expect(sectionPlace('gallery', createAppRoute('/gallery', { projectId: 'p1' }))).toEqual({ page: 'gallery' });
    expect(sectionPlace('gallery', createAppRoute('/gallery', { projectId: 'p1', image: 'x' }))).toEqual({ page: 'gallery' });
    expect(sectionPlace('memory', createAppRoute('/memory', { projectId: 'p1' }))).toEqual({ page: 'memory' });
  });
});
