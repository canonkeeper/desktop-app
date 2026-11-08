import { describe, expect, it } from 'vitest';
import { createMockApiClient } from './repository';

describe('server-owned source navigation', () => {
  it('keeps chapter precision when no persisted paragraph matches', async () => {
    const api = createMockApiClient();
    const result = (await api.searchProject('project-white-port', 'совпадение между абзацами', 'published', 'chapters')).data[0];
    if (result?.kind !== 'chapter') throw new Error('Missing chapter search result');
    expect(result.locator.sourceAnchor.kind).toBe('chapter');
    expect(result.locator.sourceAnchor.locator).not.toHaveProperty('paragraphId');
  });
});
