import { describe, expect, it } from 'vitest';
import { nextPhotoNumber, type SteerTimelineEvent } from './steer';

const row = (kind: string, extra: Partial<SteerTimelineEvent> & { text?: string } = {}): SteerTimelineEvent => ({ kind, ...extra });

// specs/2026-10-30-chat-steering.md R4.2.
describe('nextPhotoNumber', () => {
  it('starts after the images of the question of the last turn only', () => {
    expect(nextPhotoNumber([row('user', { images: [1, 2, 3] }), row('assistant'), row('user', { images: [4, 5] })], [])).toBe(3);
    expect(nextPhotoNumber([row('user')], [])).toBe(1);
  });

  it('goes on after the corrections of the turn, seen or still waiting', () => {
    const question = row('user', { images: [1] });
    const steer = row('steer', { images: [2, 3], firstPhoto: 2 });
    expect(nextPhotoNumber([question, steer], [])).toBe(4);
    expect(nextPhotoNumber([question, steer], [{ steerId: 's', text: 't', images: [9], firstPhoto: 4 }])).toBe(5);
    expect(nextPhotoNumber([question, row('steer', { text: 'без фото' })], [])).toBe(2);
  });
});
