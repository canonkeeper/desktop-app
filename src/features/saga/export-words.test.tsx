import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen } from '@testing-library/react';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { authorMaterialKinds } from '../../entities/author-materials/api';
import { hiddenIn } from '../../shared/copy/dictionary';
import { createAppRoute } from '../../shared/navigation/app-route';
import { ToastsProvider } from '../../ui/feedback/Toast';
import type { MapCanvasProps } from './MapCanvas';
import { mapCategories } from './map-model';
import { SagaMap } from './SagaMap';

// WebGL is not in jsdom: the canvas is a stand-in.
vi.mock('./MapCanvas', () => ({ MapCanvas: ({ graph }: MapCanvasProps) => <div data-nodes={graph.nodes.length} /> }));

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

/**
 * The words of the files of the export to Obsidian (specs/2026-11-15-desk-obsidian-export.md OE2.3, Data): the table the test of
 * the server reads too. `card` are what the card of a node of this page says, `categories` its categories, `kinds` the kinds of
 * the records of the author, `files` what only the files say.
 */
const table = JSON.parse(readFileSync(path.join(process.cwd(), 'backend/internal/domain/obsidian/testdata/export-words.json'), 'utf8')) as {
  card: Record<string, string | string[]> & { chapterForms: string[]; eventForms: string[]; together: string };
  categories: Record<string, string>;
  kinds: Record<string, string>;
  files: Record<string, string>;
};

type Api = ReturnType<typeof createMockApiClient>;

async function cardText(change: (card: Awaited<ReturnType<Api['getSagaMapNode']>>) => Awaited<ReturnType<Api['getSagaMapNode']>>) {
  vi.stubGlobal('matchMedia', (query: string) => ({ addEventListener: () => undefined, matches: false, media: query, removeEventListener: () => undefined }));
  const api = createMockApiClient();
  const mara = await api.getSagaMapNode(projectId, { sourceView: 'draft', key: 'мара' });
  vi.spyOn(api, 'getSagaMapNode').mockResolvedValue(change(mara));
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ToastsProvider>
      <SagaMap api={api} canEdit navigate={vi.fn()} projectId={projectId} route={createAppRoute('/saga', { projectId, node: 'мара' })} sourceView="draft" />
    </ToastsProvider></QueryClientProvider>,
  );
  await screen.findByRole('region', { name: 'По вашим материалам' });
  const text = document.body.textContent ?? '';
  cleanup();
  return text;
}

const record = {
  id: 'record-1', name: 'Мара', kind: 'character' as const, description: 'Мара родом с севера.', properties: [{ key: 'Возраст', value: '24' }],
  section: 'Герои', revision: 1, createdAt: '2026-11-04T10:00:00Z', updatedAt: '2026-11-04T10:00:00Z',
};

describe('the words of the export to Obsidian', () => {
  it('are the words the card of a node of the map says', async () => {
    const full = await cardText((mara) => {
      const chapter = mara.first!.chapter;
      return {
        ...mara, type: '', aliases: ['Марка'], mentions: 3, first: { chapter, paragraph: 3 },
        events: [{ chapter, event: 'Мара держит ворота', paragraph: 2, key: true }], eventsTotal: 41,
        changes: [{ chapter, subject: 'Мара', change: 'стала капитаном', paragraph: 4 }], changesTotal: 1,
        links: [
          { key: 'сава', name: 'Сава', category: 'character', together: 2, nearby: 0, changes: [], shared: [{ chapter, event: 'Мара и Сава у ворот', paragraph: 2, key: false }] },
          { key: 'трон', name: 'Трон', category: 'item', together: 0, nearby: 0, changes: [], fromMaterials: true },
          { key: 'глеб', name: 'Глеб', category: 'character', together: 0, nearby: 0, changes: [{ chapter, subject: 'Глеб', change: 'ушёл', paragraph: 1 }] },
        ],
        beside: [{ key: 'вера', name: 'Вера' }], namedIn: [{ id: 'record-2', name: 'Трон', kind: 'item' }],
        material: { ...record, sourceKind: 'obsidian', sourceName: 'Персонажи/Мара.md' },
      };
    });
    const plan = await cardText((mara) => ({
      ...mara, first: null, mentions: 0, events: [], eventsTotal: 0, changes: [], changesTotal: 0, links: [], namedIn: [],
      material: { ...record, sourceKind: 'xlsx', sourceName: 'навыки.xlsx' },
    }));
    const { card } = table;
    for (const [name, word] of Object.entries(card)) {
      if (Array.isArray(word) || name === 'together') continue;
      expect(full + plan, name).toContain(word);
    }
    expect(full).toContain(`${card.together} 2 ${card.eventForms[1]}`);
    expect(full).toContain(`в 3 ${card.chapterForms[1]}`);
    expect(plan).toContain(card.plan);
  });

  it('name the categories and the kinds as the page does', () => {
    expect(Object.fromEntries(mapCategories.map(({ category, label }) => [category, label]))).toEqual(table.categories);
    expect(Object.fromEntries(authorMaterialKinds.map(({ value, label }) => [value, label]))).toEqual(table.kinds);
  });

  it('hide no word the dictionary of the page of the map hides', () => {
    const words = [table.card, table.categories, table.kinds, table.files].flatMap((part) => Object.values(part).flat());
    for (const word of words) {
      expect(hiddenIn(word, 'src/features/saga/NodeCard.tsx').map((hidden) => hidden.says), word).toEqual([]);
    }
  });
});
