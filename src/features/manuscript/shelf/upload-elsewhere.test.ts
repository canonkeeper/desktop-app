import { describe, expect, it } from 'vitest';
import type { ImportQueue, ImportQueueItem } from '../../../entities/manuscript/api';
import { volumeViews, volumeWord } from './upload-files';

function item(position: number, patch: Partial<ImportQueueItem> = {}): ImportQueueItem {
  return {
    id: `item-${position}`, clientItemId: `client-${position}`, position, sourceFileName: `Книга ${position}.md`,
    mimeType: null, sizeBytes: null, preflightStatus: 'uploading', preflightError: null, processingStatus: 'uploading',
    failureClass: null, failedStep: null, importJobId: null, bookId: null, updatedAt: '2026-11-15T10:00:00Z', links: { status: null }, ...patch,
  };
}

const uploading: ImportQueue = {
  id: 'queue-1', projectId: 'project-1', status: 'uploading', version: 1, intendedItemCount: 2, currentItemId: null,
  items: [item(1), item(2)], createdAt: '2026-11-15T10:00:00Z', updatedAt: '2026-11-15T10:00:00Z', startedAt: null, completedAt: null,
  links: { self: '/q', cancel: null },
};

// specs/2026-11-15-desk-obsidian-web.md OW4.6: the volumes whose files another work of the build sends — the books of an Obsidian
// folder of the app — are loaded, not lost; without it (the site) a volume this tab does not hold is «Файл не дошёл» as before.
describe('the volumes another work of the build sends', () => {
  it('are «Загружается» while it sends them, and «Файл не дошёл» without it', () => {
    expect(volumeViews(uploading, {}, new Set(), true).map((view) => [view.state, volumeWord(view)])).toEqual([['loading', 'Загружается'], ['loading', 'Загружается']]);
    expect(volumeViews(uploading, {}, new Set()).map(volumeWord)).toEqual(['Файл не дошёл', 'Файл не дошёл']);
  });

  it('do not change what this tab knows of the files it sends itself', () => {
    const transfers = { 'client-1': { state: 'failed', sent: 0, total: null }, 'client-2': { state: 'sending', sent: 1, total: 2 } } as const;
    expect(volumeViews(uploading, transfers, new Set(), true).map(volumeWord)).toEqual(['Файл не дошёл', 'Загружается · 50 %']);
  });
});
