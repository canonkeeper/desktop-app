import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { PropsWithChildren } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type {
  ImportQueue,
  ImportQueueItem as ServerItem,
  ManuscriptApiClient,
} from '../../../entities/manuscript/api';
import { NetworkApiError } from '../../../shared/api';
import { useImportQueue } from './useImportQueue';

const projectId = 'project-1';

function file(volume: number, name = `Том ${volume}.epub`) {
  return new File([`volume-${volume}`], name, {
    type: name.endsWith('.docx')
      ? 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'
      : 'application/epub+zip',
  });
}

function clientKey(selected: File, index: number) {
  return `${index}:${selected.name}:${selected.size}:${selected.lastModified}`;
}

function serverItem(
  id: string,
  fileName: string,
  volume: number,
  overrides: Partial<ServerItem> = {},
): ServerItem {
  return {
    id,
    clientItemId: `client-${id}`,
    position: Number(id.at(-1)) || 1,
    sourceFileName: fileName,
    mimeType: 'application/epub+zip',
    sizeBytes: 24,
    detectedBookOrder: volume,
    confirmedBookOrder: volume,
    preflightStatus: 'ready',
    preflightError: null,
    processingStatus: 'ready',
    failureClass: null,
    failedStep: null,
    importJobId: null,
    bookId: null,
    updatedAt: '2026-08-20T10:00:00.000Z',
    links: { status: null },
    ...overrides,
  };
}

function serverQueue(
  status: ImportQueue['status'],
  items: ServerItem[],
  overrides: Partial<ImportQueue> = {},
): ImportQueue {
  return {
    id: 'queue-1',
    projectId,
    status,
    version: 3,
    intendedItemCount: items.length,
    nextBookOrder: 4,
    currentItemId: status === 'running' ? items[0]?.id ?? null : null,
    items,
    createdAt: '2026-08-20T10:00:00.000Z',
    updatedAt: '2026-08-20T10:00:00.000Z',
    startedAt: null,
    completedAt: null,
    links: { self: `/projects/${projectId}/import-queues/queue-1`, cancel: null },
    ...overrides,
  };
}

function constraints(nextBookOrder = 4) {
  return {
    allowedExtensions: ['.fb2', '.epub', '.docx'],
    allowedMimeTypes: [],
    maxFileSizeBytes: 50 * 1024 * 1024,
    nextBookOrder,
  } as unknown as Awaited<ReturnType<ManuscriptApiClient['getImportConstraints']>>;
}

function api(queues: ImportQueue[] = [], overrides: Partial<ManuscriptApiClient> = {}) {
  return {
    getImportConstraints: vi.fn(async () => constraints()),
    listImportQueues: vi.fn(async () => ({ data: queues })),
    getImportQueue: vi.fn(async (_projectId: string, queueId: string) => queues.find((queue) => queue.id === queueId)!),
    createImportQueue: vi.fn(),
    uploadImportQueueItem: vi.fn(),
    updateImportQueue: vi.fn(async () => queues[0]!),
    startImportQueue: vi.fn(async () => queues[0]!),
    cancelImportQueue: vi.fn(async () => queues[0]!),
    startProjectIndexing: vi.fn(async () => ({})),
    ...overrides,
  } as unknown as ManuscriptApiClient;
}

function setup(client: ManuscriptApiClient) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: PropsWithChildren) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  return { ...renderHook(() => useImportQueue({ api: client, projectId }), { wrapper }), queryClient };
}

type Hook = ReturnType<typeof setup>['result'];

// The local list is numbered and sent only after the server constraints are read (R3.1.2).
async function checked(result: Hook) {
  await waitFor(() => {
    expect(result.current.constraintsStatus).toBe('ready');
    expect(result.current.constraintsRefreshing).toBe(false);
  });
}

async function openChecked(result: Hook, files: File[]) {
  await checked(result);
  act(() => result.current.openFiles(files));
  await checked(result);
}

afterEach(() => vi.restoreAllMocks());

describe('useImportQueue', () => {
  it.each([
    ['one file', [file(4)]],
    ['many files', [file(4), file(5)]],
  ])('keeps %s local until transfer starts, then uploads sequentially and auto-starts', async (_label, files) => {
    const created = serverQueue('uploading', files.map((selected, index) => serverItem(
      `item-${index + 1}`,
      selected.name,
      index + 4,
      {
        clientItemId: clientKey(selected, index),
        preflightStatus: 'uploading',
        processingStatus: 'uploading',
      },
    )), { version: 1 });
    let uploadedCount = 0;
    const calls: string[] = [];
    const client = api([], {
      createImportQueue: vi.fn(async () => {
        calls.push('create');
        return created;
      }),
      uploadImportQueueItem: vi.fn(async (
        _projectId: string,
        _queueId: string,
        input: { itemId: string; file: File },
        options: { onUploadProgress?: (progress: { loadedBytes: number; totalBytes: number | null }) => void },
      ) => {
        calls.push(`upload:${input.file.name}`);
        options.onUploadProgress?.({ loadedBytes: input.file.size, totalBytes: input.file.size });
        uploadedCount += 1;
        return serverQueue(uploadedCount === files.length ? 'draft' : 'uploading', created.items.map((item, index) => ({
          ...item,
          preflightStatus: index < uploadedCount ? 'ready' : 'uploading',
          processingStatus: index < uploadedCount ? 'ready' : 'uploading',
        })), { version: uploadedCount + 1 });
      }),
      startImportQueue: vi.fn(async () => {
        calls.push('start');
        return serverQueue('running', created.items.map((item, index) => ({
          ...item,
          preflightStatus: 'ready',
          processingStatus: index === 0 ? 'importing' : 'queued',
        })), { version: files.length + 2, currentItemId: 'item-1' });
      }),
    });
    const { result } = setup(client);

    await openChecked(result, files);

    expect(client.createImportQueue).not.toHaveBeenCalled();
    expect(client.uploadImportQueueItem).not.toHaveBeenCalled();
    expect(client.startImportQueue).not.toHaveBeenCalled();
    expect(result.current.rows.map((row) => row.file)).toEqual(files);

    await act(async () => result.current.beginTransfer());

    expect(calls).toEqual(['create', ...files.map((selected) => `upload:${selected.name}`), 'start']);
    expect(result.current.rows.every((row) => row.file === undefined)).toBe(true);
  });

  it('confirms the shown volume numbers before it starts the uploaded queue', async () => {
    const selected = file(13, '13 том. Черновик.docx');
    const unconfirmed = { clientItemId: clientKey(selected, 0), confirmedBookOrder: null };
    const created = serverQueue('uploading', [serverItem('item-1', selected.name, 13, {
      ...unconfirmed, preflightStatus: 'uploading', processingStatus: 'uploading',
    })], { version: 1, nextBookOrder: 13 });
    const uploaded = serverQueue('draft', [serverItem('item-1', selected.name, 13, unconfirmed)], { version: 2, nextBookOrder: 13 });
    const confirmed = serverQueue('draft', [serverItem('item-1', selected.name, 13)], { version: 3, nextBookOrder: 13 });
    const calls: string[] = [];
    const client = api([], {
      createImportQueue: vi.fn(async () => {
        calls.push('create');
        return created;
      }),
      uploadImportQueueItem: vi.fn(async () => {
        calls.push('upload');
        return uploaded;
      }),
      updateImportQueue: vi.fn(async (_projectId: string, _queueId: string, body: { items: { confirmedBookOrder: number }[] }) => {
        calls.push(`save:${body.items.map((item) => item.confirmedBookOrder).join(',')}`);
        return confirmed;
      }),
      startImportQueue: vi.fn(async (_projectId: string, _queueId: string, body: { expectedVersion: number }) => {
        calls.push(`start:${body.expectedVersion}`);
        return serverQueue('running', confirmed.items, { version: 4, nextBookOrder: 13 });
      }),
      getImportConstraints: vi.fn(async () => constraints(13)),
    });
    const { result } = setup(client);

    await openChecked(result, [selected]);
    await act(async () => result.current.beginTransfer());

    expect(calls).toEqual(['create', 'upload', 'save:13', 'start:3']);
  });

  it('projects one active transfer while later local files wait', async () => {
    const files = [file(4), file(5)];
    const uploading = serverQueue('uploading', files.map((selected, index) => serverItem(
      `item-${index + 1}`,
      selected.name,
      index + 4,
      {
        clientItemId: clientKey(selected, index),
        preflightStatus: 'uploading',
        processingStatus: 'uploading',
      },
    )));
    let releaseUpload: () => void = () => undefined;
    const uploadGate = new Promise<void>((resolve) => { releaseUpload = resolve; });
    const client = api([uploading], {
      uploadImportQueueItem: vi.fn(async () => {
        await uploadGate;
        return uploading;
      }),
    });
    const { result } = setup(client);
    await waitFor(() => expect(result.current.status).toBe('uploading'));
    await checked(result);

    act(() => result.current.openFiles(files));
    await waitFor(() => expect(result.current.rows.map((row) => row.status)).toEqual(['uploading', 'pending']));
    releaseUpload();
    await waitFor(() => expect(client.uploadImportQueueItem).toHaveBeenCalledTimes(2));
  });

  it('projects a deterministic preflight failure as a repairable blocked queue', async () => {
    const invalid = serverQueue('uploading', [serverItem('item-1', 'broken.epub', 4, {
      preflightStatus: 'invalid',
      preflightError: { code: 'invalid_epub', message: 'Файл не прошёл проверку.' },
      processingStatus: 'uploading',
    })]);
    const replacement = file(4, 'replacement.epub');
    const uploadImportQueueItem = vi.fn(async () => serverQueue('draft', [serverItem('item-1', replacement.name, 4)]));
    const client = api([invalid], { uploadImportQueueItem });
    const { result } = setup(client);

    await waitFor(() => expect(result.current.status).toBe('blocked'));
    await checked(result);
    await act(async () => result.current.replaceFile('item-1', replacement));

    expect(uploadImportQueueItem).toHaveBeenCalledWith(
      projectId,
      'queue-1',
      expect.objectContaining({ itemId: 'item-1', file: replacement }),
      expect.objectContaining({ idempotencyKey: expect.any(String) }),
    );
  });

  it('continues sequential transfer after a row failure and starts only after that row retries', async () => {
    const files = [file(4), file(5)];
    const created = serverQueue('uploading', files.map((selected, index) => serverItem(
      `item-${index + 1}`,
      selected.name,
      index + 4,
      { clientItemId: clientKey(selected, index), preflightStatus: 'uploading', processingStatus: 'uploading' },
    )), { version: 1 });
    let firstAttempt = true;
    const uploadOrder: string[] = [];
    const uploadImportQueueItem = vi.fn(async (
      _projectId: string,
      _queueId: string,
      input: { itemId: string; file: File },
    ) => {
      uploadOrder.push(input.itemId);
      if (input.itemId === 'item-1' && firstAttempt) {
        firstAttempt = false;
        throw new NetworkApiError(new TypeError('offline'));
      }
      const ready = input.itemId === 'item-1';
      return serverQueue(ready ? 'draft' : 'uploading', created.items.map((item) => ({
        ...item,
        preflightStatus: ready || item.id === 'item-2' ? 'ready' : 'uploading',
        processingStatus: ready || item.id === 'item-2' ? 'ready' : 'uploading',
      })), { version: ready ? 3 : 2 });
    });
    const client = api([], {
      createImportQueue: vi.fn(async () => created),
      uploadImportQueueItem,
      startImportQueue: vi.fn(async () => serverQueue('running', created.items, { version: 4 })),
    });
    const { result } = setup(client);
    await openChecked(result, files);

    await act(async () => result.current.beginTransfer());

    expect(uploadOrder).toEqual(['item-1', 'item-2']);
    expect(client.startImportQueue).not.toHaveBeenCalled();
    expect(result.current.rows.find((row) => row.id === 'item-1')?.transferState).toBe('failed');

    await act(async () => result.current.retry());

    expect(uploadOrder).toEqual(['item-1', 'item-2', 'item-1']);
    expect(client.startImportQueue).toHaveBeenCalledTimes(1);
  });

  it('removes, replaces and reorders only the local selection', async () => {
    const files = [file(4), file(5), file(6)];
    const client = api();
    const { result } = setup(client);
    await openChecked(result, files);
    const [first, second, third] = result.current.rows;

    act(() => result.current.move(third!.id, first!.id));
    act(() => result.current.remove(second!.id));
    act(() => { void result.current.replaceFile(first!.id, file(7)); });

    expect(result.current.rows.map((row) => row.fileName)).toEqual(['Том 6.epub', 'Том 7.epub']);
    expect(client.createImportQueue).not.toHaveBeenCalled();
  });

  it('uses a fresh upload idempotency key when replacing the same server item', async () => {
    const draft = serverQueue('draft', [
      serverItem('item-1', 'ambiguous.epub', 4, { detectedBookOrder: null, confirmedBookOrder: null }),
    ]);
    const uploadKeys: string[] = [];
    const uploadImportQueueItem = vi.fn(async (
      _projectId: string,
      _queueId: string,
      _input: { itemId: string; file: File },
      options: { idempotencyKey: string },
    ) => {
      uploadKeys.push(options.idempotencyKey);
      return draft;
    });
    const client = api([draft], { uploadImportQueueItem });
    const { result } = setup(client);
    await waitFor(() => expect(result.current.status).toBe('editing'));
    await checked(result);

    await act(async () => result.current.replaceFile('item-1', file(4, 'replacement-a.epub')));
    await act(async () => result.current.replaceFile('item-1', file(5, 'replacement-b.epub')));

    expect(uploadImportQueueItem).toHaveBeenCalledTimes(2);
    expect(uploadKeys[1]).not.toBe(uploadKeys[0]);
  });

  it('saves the final corrected order, auto-starts it, and keeps accepted rows immutable', async () => {
    const ambiguous = serverQueue('draft', [
      serverItem('item-1', 'final.epub', 4, { detectedBookOrder: null, confirmedBookOrder: null }),
    ]);
    const saved = serverQueue('draft', [serverItem('item-1', 'final.epub', 4)], { version: 4 });
    const running = serverQueue('running', [serverItem('item-1', 'final.epub', 4, {
      importJobId: 'import-1', processingStatus: 'importing',
    })], { version: 5 });
    const client = api([ambiguous], {
      updateImportQueue: vi.fn(async () => saved),
      startImportQueue: vi.fn(async () => running),
    });
    const { result } = setup(client);
    await waitFor(() => expect(result.current.status).toBe('editing'));

    act(() => result.current.setVolume('item-1', 4));

    await waitFor(() => expect(client.startImportQueue).toHaveBeenCalledTimes(1));
    expect(client.updateImportQueue).toHaveBeenCalledWith(projectId, 'queue-1', {
      expectedVersion: 3,
      items: [{ id: 'item-1', position: 1, confirmedBookOrder: 4 }],
    });
    expect(result.current.canEdit).toBe(false);
    expect(result.current.rows[0]?.locked).toBe(true);

    act(() => result.current.move('item-1', 'item-1'));
    act(() => result.current.setVolume('item-1', 5));
    expect(client.updateImportQueue).toHaveBeenCalledTimes(1);
  });

  it('keeps multiple corrected volumes as a local draft until the payload is complete', async () => {
    const ambiguous = serverQueue('draft', [
      serverItem('item-1', 'first.epub', 4, { detectedBookOrder: null, confirmedBookOrder: null }),
      serverItem('item-2', 'second.epub', 5, { detectedBookOrder: null, confirmedBookOrder: null }),
    ]);
    const saved = serverQueue('draft', [
      serverItem('item-1', 'first.epub', 4),
      serverItem('item-2', 'second.epub', 5),
    ], { version: 4 });
    const updateImportQueue = vi.fn(async () => saved);
    const client = api([ambiguous], {
      updateImportQueue,
      startImportQueue: vi.fn(async () => serverQueue('running', saved.items, { version: 5 })),
    });
    const { result } = setup(client);
    await waitFor(() => expect(result.current.status).toBe('editing'));

    act(() => result.current.setVolume('item-1', 4));

    expect(result.current.rows.map((row) => row.volume)).toEqual([4, null]);
    expect(updateImportQueue).not.toHaveBeenCalled();

    act(() => result.current.setVolume('item-2', 5));

    await waitFor(() => expect(updateImportQueue).toHaveBeenCalledWith(projectId, 'queue-1', {
      expectedVersion: 3,
      items: [
        { id: 'item-1', position: 1, confirmedBookOrder: 4 },
        { id: 'item-2', position: 2, confirmedBookOrder: 5 },
      ],
    }));
  });

  it('locks an accepted draft when the next project volume is missing', async () => {
    const blocked = serverQueue('draft', [serverItem('item-1', 'Том 6.epub', 6)], { nextBookOrder: 5 });
    const { result } = setup(api([blocked]));

    await waitFor(() => expect(result.current.status).toBe('blocked'));

    expect(result.current.canEdit).toBe(false);
    expect(result.current.validation.message).toBe('Не хватает книги №5: номера должны идти подряд.');
  });

  it('hides processing without canceling and stays hidden after reconciliation', async () => {
    const running = serverQueue('running', [serverItem('item-1', 'Том 4.epub', 4, {
      importJobId: 'import-1', processingStatus: 'importing',
    })]);
    const client = api([running]);
    const { queryClient, result } = setup(client);
    await waitFor(() => expect(result.current.status).toBe('processing'));

    act(() => result.current.hide());
    await act(async () => queryClient.refetchQueries());

    expect(result.current.isDialogVisible).toBe(false);
    expect(result.current.canReopen).toBe(true);
    expect(client.cancelImportQueue).not.toHaveBeenCalled();
  });

  it('retries a failed accepted item by restarting the draft text index', async () => {
    const failed = serverQueue('failed', [serverItem('item-1', 'Том 4.epub', 4, {
      bookId: 'book-4',
      processingStatus: 'failed',
    })]);
    const client = api([failed]);
    const { result } = setup(client);
    await waitFor(() => expect(result.current.status).toBe('failed'));

    await act(async () => result.current.retry());

    expect(client.startProjectIndexing).toHaveBeenCalledWith(
      projectId,
      { sourceView: 'draft' },
      expect.objectContaining({ idempotencyKey: expect.any(String) }),
    );
  });

  it('takes the reason of a failed import from its Job', async () => {
    const failed = serverQueue('failed', [serverItem('item-1', 'Том 4.fb2', 4, {
      failedStep: 'import',
      failureClass: 'unexpected_deterministic_failure',
      importJobId: 'job-4',
      processingStatus: 'failed',
    })], { currentItemId: 'item-1' });
    const failedJob = {
      id: 'job-4',
      kind: 'import',
      status: 'failed',
      error: { type: 'about:blank', title: 'Job failed', status: 500, code: 'import.too_large', requestId: 'req-1' },
    } as unknown as Awaited<ReturnType<ManuscriptApiClient['getJob']>>;
    const client = api([failed], { getJob: vi.fn(async () => failedJob) });
    const { result } = setup(client);

    await waitFor(() => expect(result.current.rows[0]?.errorCode).toBe('import.too_large'));
    expect(client.getJob).toHaveBeenCalledWith('job-4');
  });

  it('leaves an older failed queue as history once a newer queue exists', async () => {
    const failed = serverQueue('failed', [serverItem('item-1', 'Том 12.fb2', 12, { processingStatus: 'failed' })], {
      id: 'queue-old', updatedAt: '2026-08-31T20:03:03.000Z',
    });
    const later = serverQueue('succeeded', [serverItem('item-2', 'Том 12. Черновик.fb2', 12, { processingStatus: 'succeeded' })], {
      id: 'queue-new', updatedAt: '2026-08-31T22:27:03.000Z',
    });
    const client = api([failed, later]);
    const { result } = setup(client);
    await waitFor(() => expect(client.listImportQueues).toHaveBeenCalled());
    await waitFor(() => expect(result.current.isOpen).toBe(false));
    expect(result.current.status).not.toBe('failed');
  });

  it('dismisses a terminal failed queue without canceling it', async () => {
    const failed = serverQueue('failed', [serverItem('item-1', 'Том 4.epub', 4, { processingStatus: 'failed' })]);
    const client = api([failed]);
    const { result } = setup(client);
    await waitFor(() => expect(result.current.status).toBe('failed'));

    act(() => result.current.dismiss());

    expect(result.current.isOpen).toBe(false);
    expect(client.cancelImportQueue).not.toHaveBeenCalled();
  });

  it('finishes a failed queue before returning to a new local replacement selection', async () => {
    const failed = serverQueue('failed', [serverItem('item-1', 'Том 4.epub', 4, { processingStatus: 'failed' })]);
    const canceled = serverQueue('canceled', failed.items, { version: 4 });
    const client = api([failed], { cancelImportQueue: vi.fn(async () => canceled) });
    const { result } = setup(client);
    await waitFor(() => expect(result.current.status).toBe('failed'));

    await act(async () => result.current.replaceQueue([file(5)]));

    expect(client.cancelImportQueue).toHaveBeenCalledWith(projectId, 'queue-1');
    expect(result.current.status).toBe('editing');
    expect(result.current.rows.map((row) => row.fileName)).toEqual(['Том 5.epub']);
    expect(client.createImportQueue).not.toHaveBeenCalled();
  });

  it('keeps the failed queue visible when replacement cannot cancel it', async () => {
    const failed = serverQueue('failed', [serverItem('item-1', 'Том 4.epub', 4, { processingStatus: 'failed' })]);
    const client = api([failed], {
      cancelImportQueue: vi.fn(async () => { throw new NetworkApiError(new TypeError('offline')); }),
    });
    const { result } = setup(client);
    await waitFor(() => expect(result.current.status).toBe('failed'));

    await act(async () => result.current.replaceQueue([file(5)]));

    expect(result.current.status).toBe('failed');
    expect(result.current.rows.map((row) => row.fileName)).toEqual(['Том 4.epub']);
    expect(result.current.errorMessage).toBeTruthy();
  });

  it('replays an unknown create and start outcome with stable idempotency keys', async () => {
    const selected = file(4);
    const created = serverQueue('uploading', [serverItem('item-1', selected.name, 4, {
      clientItemId: clientKey(selected, 0), preflightStatus: 'uploading', processingStatus: 'uploading',
    })], { version: 1 });
    const draft = serverQueue('draft', [serverItem('item-1', selected.name, 4, {
      clientItemId: clientKey(selected, 0),
    })], { version: 2 });
    const running = serverQueue('running', draft.items, { version: 3 });
    const createImportQueue = vi.fn()
      .mockRejectedValueOnce(new NetworkApiError(new TypeError('offline after create')))
      .mockResolvedValueOnce(created);
    const startImportQueue = vi.fn()
      .mockRejectedValueOnce(new NetworkApiError(new TypeError('offline after start')))
      .mockResolvedValueOnce(running);
    const client = api([], {
      createImportQueue,
      uploadImportQueueItem: vi.fn(async () => draft),
      startImportQueue,
    });
    const { result } = setup(client);
    await openChecked(result, [selected]);

    await act(async () => result.current.beginTransfer());
    await act(async () => result.current.retry());

    expect(createImportQueue.mock.calls[1]?.[2].idempotencyKey).toBe(createImportQueue.mock.calls[0]?.[2].idempotencyKey);
    expect(startImportQueue).toHaveBeenCalledTimes(1);

    await act(async () => result.current.retry());

    expect(startImportQueue.mock.calls[1]?.[3].idempotencyKey).toBe(startImportQueue.mock.calls[0]?.[3].idempotencyKey);
  });

  it('reattaches selected files to an uploading server queue and resumes only missing items', async () => {
    const selected = file(5);
    const partial = serverQueue('uploading', [
      serverItem('item-1', 'Том 4.epub', 4),
      serverItem('item-2', selected.name, 5, {
        clientItemId: clientKey(selected, 0), preflightStatus: 'uploading', processingStatus: 'uploading',
      }),
    ]);
    const draft = serverQueue('draft', partial.items.map((item) => ({
      ...item, preflightStatus: 'ready', processingStatus: 'ready',
    })), { version: 4 });
    const client = api([partial], {
      uploadImportQueueItem: vi.fn(async () => draft),
      startImportQueue: vi.fn(async () => serverQueue('running', draft.items, { version: 5 })),
    });
    const { result } = setup(client);
    await waitFor(() => expect(result.current.status).toBe('uploading'));
    await checked(result);

    act(() => result.current.openFiles([selected]));
    await waitFor(() => expect(client.startImportQueue).toHaveBeenCalledTimes(1));

    expect(client.createImportQueue).not.toHaveBeenCalled();
    expect(client.uploadImportQueueItem).toHaveBeenCalledWith(
      projectId,
      'queue-1',
      expect.objectContaining({ itemId: 'item-2', file: selected }),
      expect.objectContaining({ idempotencyKey: expect.any(String) }),
    );
    expect(client.startImportQueue).toHaveBeenCalledTimes(1);
  });

  // RED on TEST 28.10: a single book without a number stopped the queue «номера неоднозначны».
  it('numbers a single book without a number from the saga and starts it without a stop', async () => {
    const selected = file(0, 'Пламя Севера.docx');
    const created = serverQueue('uploading', [serverItem('item-1', selected.name, 1, {
      clientItemId: clientKey(selected, 0), confirmedBookOrder: null, preflightStatus: 'uploading', processingStatus: 'uploading',
    })], { version: 1, nextBookOrder: 1 });
    const uploaded = serverQueue('draft', [serverItem('item-1', selected.name, 1, {
      clientItemId: clientKey(selected, 0), confirmedBookOrder: null,
    })], { version: 2, nextBookOrder: 1 });
    const confirmed = serverQueue('draft', [serverItem('item-1', selected.name, 1)], { version: 3, nextBookOrder: 1 });
    const client = api([], {
      getImportConstraints: vi.fn(async () => constraints(1)),
      createImportQueue: vi.fn(async () => created),
      uploadImportQueueItem: vi.fn(async () => uploaded),
      updateImportQueue: vi.fn(async () => confirmed),
      startImportQueue: vi.fn(async () => serverQueue('running', confirmed.items, { version: 4, nextBookOrder: 1 })),
    });
    const { result } = setup(client);

    await openChecked(result, [selected]);
    expect(result.current.rows.map((row) => [row.volume, row.volumeSource])).toEqual([[1, 'order']]);
    expect(result.current.validation.valid).toBe(true);
    await act(async () => result.current.beginTransfer());

    expect(client.createImportQueue).toHaveBeenCalledWith(projectId, {
      items: [{ clientItemId: clientKey(selected, 0), position: 1, sourceFileName: selected.name, detectedBookOrder: 1 }],
    }, expect.anything());
    expect(client.updateImportQueue).toHaveBeenCalledWith(projectId, 'queue-1', {
      expectedVersion: 2, items: [{ id: 'item-1', position: 1, confirmedBookOrder: 1 }],
    });
    expect(client.startImportQueue).toHaveBeenCalledTimes(1);
  });

  it('sends the number the author typed for a file without one', async () => {
    const named = file(2, 'Том 2.fb2');
    const draft = file(0, 'черновик.docx');
    const client = api([], {
      getImportConstraints: vi.fn(async () => constraints(2)),
      createImportQueue: vi.fn(async () => { throw new NetworkApiError(new TypeError('offline')); }),
    });
    const { result } = setup(client);
    await openChecked(result, [named, draft]);
    const draftRow = result.current.rows.find((row) => row.fileName === draft.name)!;

    expect(result.current.validation.valid).toBe(false);
    act(() => result.current.setVolume(draftRow.id, 3));
    expect(result.current.rows.map((row) => [row.volume, row.volumeSource])).toEqual([[2, 'file'], [3, 'manual']]);
    expect(result.current.validation.valid).toBe(true);
    await act(async () => result.current.beginTransfer());

    expect(client.createImportQueue).toHaveBeenCalledWith(projectId, {
      items: [
        expect.objectContaining({ sourceFileName: named.name, detectedBookOrder: 2 }),
        expect.objectContaining({ sourceFileName: draft.name, detectedBookOrder: 3 }),
      ],
    }, expect.anything());
  });

  // R3.1.1: refused files stay out of the list and out of every request.
  it('keeps refused files out of the list and transfers only the accepted one', async () => {
    const good = file(4);
    const big = file(5, 'Том 5.docx');
    Object.defineProperty(big, 'size', { value: 60 * 1024 * 1024 });
    const client = api();
    const { result } = setup(client);

    await openChecked(result, [
      new File(['%PDF'], 'rukopis.pdf'),
      new File(['doc'], 'chernovik.doc'),
      new File(['zip'], 'kniga.fb2.zip'),
      big,
      good,
    ]);

    expect(result.current.rows.map((row) => row.fileName)).toEqual([good.name]);
    expect(result.current.rejectedFiles.map((rejected) => rejected.fileName).sort())
      .toEqual(['chernovik.doc', 'kniga.fb2.zip', 'rukopis.pdf', 'Том 5.docx']);
    expect(result.current.isOpen).toBe(true);
    expect(client.createImportQueue).not.toHaveBeenCalled();
    expect(client.uploadImportQueueItem).not.toHaveBeenCalled();
  });

  it('opens no dialog when no chosen file is accepted', async () => {
    const client = api();
    const { result } = setup(client);

    await openChecked(result, [new File(['%PDF'], 'rukopis.pdf')]);

    expect(result.current.isOpen).toBe(false);
    expect(result.current.rejectedFiles).toEqual([expect.objectContaining({ fileName: 'rukopis.pdf' })]);
    act(() => result.current.dismiss());
    expect(result.current.rejectedFiles).toEqual([]);
  });

  // R3.1.2: no unchecked path.
  it('waits for the constraints and offers a retry when they fail', async () => {
    let fail = true;
    const client = api([], {
      getImportConstraints: vi.fn(async () => {
        if (fail) throw new NetworkApiError(new TypeError('offline'));
        return constraints();
      }),
    });
    const { result } = setup(client);
    await waitFor(() => expect(result.current.constraintsStatus).toBe('error'));

    act(() => result.current.openFiles([file(4)]));
    await waitFor(() => expect(result.current.constraintsRefreshing).toBe(false));
    expect(result.current.isOpen).toBe(true);
    expect(result.current.rows).toEqual([]);
    expect(result.current.canRetry).toBe(true);
    await act(async () => result.current.beginTransfer());
    expect(client.createImportQueue).not.toHaveBeenCalled();

    fail = false;
    await act(async () => result.current.retry());
    await checked(result);
    expect(result.current.rows.map((row) => row.volume)).toEqual([4]);
  });

  // R3.5.1, R3.5.2: a reload during a transfer leaves the queue waiting for the files.
  it('shows an interrupted transfer and continues it with the chosen files', async () => {
    const first = file(4);
    const second = file(5);
    const interrupted = serverQueue('uploading', [
      serverItem('item-1', first.name, 4, { clientItemId: 'old-1' }),
      serverItem('item-2', second.name, 5, { clientItemId: 'old-2', preflightStatus: 'uploading', processingStatus: 'uploading' }),
    ]);
    const draft = serverQueue('draft', interrupted.items.map((item) => ({ ...item, preflightStatus: 'ready', processingStatus: 'ready' })), { version: 4 });
    const client = api([interrupted], {
      uploadImportQueueItem: vi.fn(async () => draft),
      startImportQueue: vi.fn(async () => serverQueue('running', draft.items, { version: 5 })),
    });
    const { result } = setup(client);
    await waitFor(() => expect(result.current.transferInterrupted).toBe(true));
    await checked(result);

    expect(result.current.rows.map((row) => row.transferState)).toEqual(['uploaded', null]);
    act(() => result.current.openFiles([first, second]));
    await waitFor(() => expect(client.startImportQueue).toHaveBeenCalledTimes(1));

    expect(client.uploadImportQueueItem).toHaveBeenCalledTimes(1);
    expect(client.uploadImportQueueItem).toHaveBeenCalledWith(
      projectId, 'queue-1', expect.objectContaining({ itemId: 'item-2', file: second }), expect.anything(),
    );
  });

  // R3.4.3: a file the author fixes is replaced, a transient failure is retried with its code.
  it.each([
    ['invalid_docx_document', false],
    ['worker.handler_failed', true],
  ])('offers a retry of a failed import only for a transient failure (%s)', async (code, retryable) => {
    const failed = serverQueue('failed', [serverItem('item-1', 'Том 4.docx', 4, {
      failedStep: 'import', importJobId: 'job-4', processingStatus: 'failed',
    })], { currentItemId: 'item-1' });
    const failedJob = {
      id: 'job-4',
      kind: 'import',
      status: 'failed',
      error: { type: 'about:blank', title: 'Job failed', status: 500, code, requestId: 'req-1' },
    } as unknown as Awaited<ReturnType<ManuscriptApiClient['getJob']>>;
    const { result } = setup(api([failed], { getJob: vi.fn(async () => failedJob) }));

    await waitFor(() => expect(result.current.rows[0]?.errorCode).toBe(code));
    expect(result.current.rows[0]?.supportCode).toBe('req-1');
    expect(result.current.canRetry).toBe(retryable);
  });
});
