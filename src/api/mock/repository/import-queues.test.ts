import { describe, expect, it } from 'vitest';
import { createMockApiClient, type MockRepository } from '../index';

const projectId = 'project-white-port';

function book(volume: number) {
  return new File([`volume-${volume}`], `том ${volume}.fb2`, { type: 'application/x-fictionbook+xml' });
}

async function importQueue(volumes: number[]) {
  const api = createMockApiClient();
  const created = await api.createImportQueue(projectId, {
    items: volumes.map((volume, index) => ({
      clientItemId: `client-${volume}`,
      position: index + 1,
      sourceFileName: `том ${volume}.fb2`,
      detectedBookOrder: volume,
    })),
  }, { idempotencyKey: `idem-create-${volumes.join('-')}` });

  for (const [index, item] of created.items.entries()) {
    await api.uploadImportQueueItem(projectId, created.id, {
      itemId: item.id,
      file: book(volumes[index]!),
    }, { idempotencyKey: `idem-upload-${volumes.join('-')}-${item.id}` });
  }
  const uploaded = await api.getImportQueue(projectId, created.id);
  const saved = await api.updateImportQueue(projectId, created.id, {
    expectedVersion: uploaded.version,
    items: uploaded.items.map((item) => ({ id: item.id, position: item.position, confirmedBookOrder: item.detectedBookOrder! })),
  });
  const started = await api.startImportQueue(projectId, created.id, {
    expectedVersion: saved.version,
  }, { idempotencyKey: `idem-start-${volumes.join('-')}` });
  return { api, queue: started };
}

/** Текстовый индекс черновика, который ждёт импортированная книга, проходит запуск и завершение. */
async function finishDraftTextIndex(api: MockRepository, bookId: string | null) {
  const status = await api.getIndexingStatus(projectId, { bookId: bookId!, sourceView: 'draft' });
  const jobId = status.steps.find((step) => step.kind === 'text_index')?.jobId;
  if (!jobId) throw new Error('Expected a draft text index Job.');
  await api.getJob(jobId);
  return api.getJob(jobId);
}

describe('mock automatic import queues', () => {
  it('advances to the successor once the draft text index makes the book ready', async () => {
    const { api, queue } = await importQueue([5, 6]);
    const first = queue.items[0]!;
    await api.getJob(first.importJobId!);

    let current = await api.getImportQueue(projectId, queue.id);
    const bookId = current.items[0]!.bookId;
    expect(current.items[0]).toMatchObject({
      processingStatus: 'indexing',
      links: { status: `/manuscript/books?projectId=${projectId}&bookId=${bookId}&sourceView=draft` },
    });
    expect(current.items[1]).toMatchObject({ processingStatus: 'queued', links: { status: null } });
    await expect(api.getBook(bookId!)).resolves.toMatchObject({ status: 'draft' });

    await finishDraftTextIndex(api, bookId);
    await expect(api.getBook(bookId!)).resolves.toMatchObject({ status: 'ready' });
    current = await api.getImportQueue(projectId, queue.id);
    expect(current.items[0]).toMatchObject({ processingStatus: 'succeeded' });
    expect(current.currentItemId).toBe(current.items[1]!.id);
  });

  it('completes the queue after the last ready book while chapter cards are still queued', async () => {
    const { api, queue } = await importQueue([5, 6]);
    await api.getJob(queue.items[0]!.importJobId!);
    await finishDraftTextIndex(api, (await api.getImportQueue(projectId, queue.id)).items[0]!.bookId);
    await api.getJob(queue.items[1]!.importJobId!);
    await finishDraftTextIndex(api, (await api.getImportQueue(projectId, queue.id)).items[1]!.bookId);

    const completed = await api.getImportQueue(projectId, queue.id);
    expect(completed).toMatchObject({ status: 'succeeded', currentItemId: null, links: { cancel: null } });
    expect(completed.items.map((item) => item.processingStatus)).toEqual(['succeeded', 'succeeded']);
    const cards = await api.listProjectJobs(projectId, { kind: 'chapter_cards' });
    expect(cards.data.some((job) => job.status === 'queued')).toBe(true);
  });

  // FB2, EPUB and DOCX publish the imported text at once (`publish_imported`), so the chat reads the book.
  it('publishes the imported book and prepares its published text for the chat', async () => {
    const { api, queue } = await importQueue([5]);
    await api.getJob(queue.items[0]!.importJobId!);
    const bookId = (await api.getImportQueue(projectId, queue.id)).items[0]!.bookId!;
    for (let round = 0; round < 4; round += 1) {
      const active = (await api.listProjectJobs(projectId)).data.filter((job) => job.status === 'queued' || job.status === 'running');
      for (const job of active) await api.getJob(job.id);
    }

    const status = await api.getIndexingStatus(projectId, { bookId, sourceView: 'published' });
    expect(status.steps.map((step) => [step.kind, step.status])).toEqual([
      ['import', 'succeeded'], ['text_index', 'succeeded'], ['cards', 'succeeded'], ['ready', 'succeeded'],
    ]);
  });

  it('keeps an imported volume indexing after the queue is canceled', async () => {
    const { api, queue } = await importQueue([5, 6]);
    await api.getJob(queue.items[0]!.importJobId!);

    const canceled = await api.cancelImportQueue(projectId, queue.id);
    expect(canceled.status).toBe('canceled');
    expect(canceled.items.map((item) => item.processingStatus)).toEqual(['indexing', 'canceled']);
    await expect(api.getJob(queue.items[1]!.importJobId!)).resolves.toMatchObject({ status: 'canceled' });

    await finishDraftTextIndex(api, canceled.items[0]!.bookId);
    const settled = await api.getImportQueue(projectId, queue.id);
    expect(settled.status).toBe('canceled');
    expect(settled.items.map((item) => item.processingStatus)).toEqual(['succeeded', 'canceled']);
  });
});
