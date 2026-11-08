import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';

const clientApiBaseEnvKey = ['VITE', 'API', 'BASE', 'URL'].join('_');

describe('createCanonKeeperApiClient', () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it('keeps the default API base same-origin even when client env has a URL', async () => {
    vi.stubEnv(clientApiBaseEnvKey, 'https://api.example.test/not-same-origin');
    vi.resetModules();
    const { createCanonKeeperApiClient } = await import('./index');
    const api = createCanonKeeperApiClient();

    expect((api as unknown as { baseUrl: string }).baseUrl).toBe('/api/v1');
  });

  it('does not expose an API base override through client env', async () => {
    const source = await readFile(join(process.cwd(), 'src/shared/api/index.ts'), 'utf8');

    expect(source.includes(clientApiBaseEnvKey)).toBe(false);
  });
});
