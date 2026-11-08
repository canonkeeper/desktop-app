import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { projectId } from '../../../api/mock/repository/fixtures';
import { manuscriptQueryKeys } from '../../../entities/manuscript/api';
import { NetworkApiError } from '../../../shared/api';
import { parseRoute } from '../../../shared/navigation/app-route';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { ManuscriptShelfPage } from './ManuscriptShelfPage';

beforeEach(() => vi.stubGlobal('WebSocket', FakeWebSocket));
afterEach(() => {
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function openTransfer(files = [new File(['book'], 'Том 5.epub', { type: 'application/epub+zip' })], search = `?projectId=${projectId}`) {
  const api = createMockApiClient();
  api.setScenario({ actorRole: 'editor', preset: 'normal' });
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(<QueryClientProvider client={queryClient}>
    <ManuscriptShelfPage
      api={api}
      navigate={vi.fn()}
      onLogout={vi.fn()}
      route={parseRoute(`http://localhost/manuscript/books${search}`)}
      userName="Мира Волкова"
    />
  </QueryClientProvider>);
  const input = await screen.findByLabelText('Файлы книг: FB2, EPUB или DOCX');
  await waitFor(() => expect(input).toHaveProperty('disabled', false));
  fireEvent.change(input, {
    target: { files },
  });
  return { api, queryClient, dialog: await screen.findByRole('dialog', { name: 'Загрузка книг' }) };
}

describe('files refused before the transfer (UC3.1)', () => {
  // R3.1.1: a drop of only unsupported files opens no dialog; the reason stays on the shelf.
  it('keeps the reason of a dropped PDF on the shelf without a dialog or a request', async () => {
    const api = createMockApiClient();
    api.setScenario({ actorRole: 'editor', preset: 'normal' });
    const create = vi.spyOn(api, 'createImportQueue');
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(<QueryClientProvider client={queryClient}>
      <ManuscriptShelfPage
        api={api}
        navigate={vi.fn()}
        onLogout={vi.fn()}
        route={parseRoute(`http://localhost/manuscript/books?projectId=${projectId}`)}
        userName="Мира Волкова"
      />
    </QueryClientProvider>);
    const zone = (await screen.findByText('Перетащите FB2, EPUB или DOCX')).closest('button')!;
    await waitFor(() => expect(zone).toHaveProperty('disabled', false));

    fireEvent.drop(zone, { dataTransfer: { files: [new File(['%PDF'], 'rukopis.pdf', { type: 'application/pdf' })] } });

    const reasons = await screen.findByRole('alert', { name: 'Не добавлены: 1' });
    expect(reasons.textContent).toContain('PDF не поддерживается');
    expect(screen.queryByRole('dialog', { name: 'Загрузка книг' })).toBeNull();
    expect(create).not.toHaveBeenCalled();
    fireEvent.click(within(reasons).getByRole('button', { name: 'Скрыть причины' }));
    expect(screen.queryByRole('alert', { name: 'Не добавлены: 1' })).toBeNull();
  });
});

describe('shelf transfer creation dismissal', () => {
  it.each(['cancel', 'close', 'escape', 'scrim'] as const)(
    'keeps the local dialog attached to pending create after %s', async (action) => {
      const { api, dialog } = await openTransfer();
      const create = api.createImportQueue.bind(api);
      let releaseCreate!: () => void;
      const gate = new Promise<void>((resolve) => { releaseCreate = resolve; });
      const createSpy = vi.spyOn(api, 'createImportQueue').mockImplementation(async (...args) => {
        await gate;
        return create(...args);
      });
      const upload = vi.spyOn(api, 'uploadImportQueueItem');
      const start = vi.spyOn(api, 'startImportQueue');
      const cancel = vi.spyOn(api, 'cancelImportQueue');
      fireEvent.click(within(dialog).getByRole('button', { name: 'Загрузить' }));
      await waitFor(() => expect(createSpy).toHaveBeenCalledTimes(1));
      await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Загрузить' })).toHaveProperty('disabled', true));

      try {
        if (action === 'cancel') fireEvent.click(within(dialog).getByRole('button', { name: 'Отмена' }));
        if (action === 'close') fireEvent.click(within(dialog).getByRole('button', { name: 'Закрыть очередь' }));
        if (action === 'escape') fireEvent.keyDown(dialog, { key: 'Escape' });
        if (action === 'scrim') fireEvent.pointerDown(dialog.closest('[data-overlay-scrim]')!);
        expect(screen.queryByRole('dialog', { name: 'Загрузка книг' })).not.toBeNull();
        expect(within(dialog).getByRole('button', { name: 'Отмена' })).toHaveProperty('disabled', true);
        expect(within(dialog).getByRole('button', { name: 'Закрыть очередь' })).toHaveProperty('disabled', true);
        expect(upload).not.toHaveBeenCalled();
      } finally {
        releaseCreate();
        await waitFor(() => expect(start).toHaveBeenCalledTimes(1));
      }

      expect(createSpy).toHaveBeenCalledTimes(1);
      expect(upload).toHaveBeenCalledTimes(1);
      expect(cancel).not.toHaveBeenCalled();
      await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Скрыть очередь' })).toHaveProperty('disabled', false));
      fireEvent.click(within(dialog).getByRole('button', { name: 'Скрыть очередь' }));
      expect(screen.queryByRole('dialog', { name: 'Загрузка книг' })).toBeNull();
      expect(cancel).not.toHaveBeenCalled();
    },
  );

  it('cancels the local selection before the first request', async () => {
    const { api, dialog } = await openTransfer();
    const create = vi.spyOn(api, 'createImportQueue');
    const upload = vi.spyOn(api, 'uploadImportQueueItem');
    const start = vi.spyOn(api, 'startImportQueue');
    const cancel = vi.spyOn(api, 'cancelImportQueue');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Отмена' }));
    expect(screen.queryByRole('dialog', { name: 'Загрузка книг' })).toBeNull();
    expect(create).not.toHaveBeenCalled();
    expect(upload).not.toHaveBeenCalled();
    expect(start).not.toHaveBeenCalled();
    expect(cancel).not.toHaveBeenCalled();
  });

  it('unlocks dismissal after queue creation fails', async () => {
    const { api, dialog } = await openTransfer();
    vi.spyOn(api, 'createImportQueue').mockRejectedValue(new Error('queue unavailable'));
    const upload = vi.spyOn(api, 'uploadImportQueueItem');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Загрузить' }));
    const cancel = within(dialog).getByRole('button', { name: 'Отмена' });
    await within(dialog).findByRole('alert', { name: 'Не удалось создать очередь импорта.' });
    await waitFor(() => expect(cancel).toHaveProperty('disabled', false));
    fireEvent.click(cancel);
    expect(screen.queryByRole('dialog', { name: 'Загрузка книг' })).toBeNull();
    expect(upload).not.toHaveBeenCalled();
  });

  it('closes normally after authoritative queue cancellation', async () => {
    const { api, dialog } = await openTransfer();
    const cancel = vi.spyOn(api, 'cancelImportQueue');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Загрузить' }));
    const cancelButton = await within(dialog).findByRole('button', { name: 'Отменить очередь' });
    await waitFor(() => expect(cancelButton).toHaveProperty('disabled', false));
    fireEvent.click(cancelButton);
    await within(dialog).findByRole('heading', { name: 'Очередь отменена' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Закрыть' }));
    expect(screen.queryByRole('dialog', { name: 'Загрузка книг' })).toBeNull();
    expect(cancel).toHaveBeenCalledTimes(1);
  });
});

describe('confirmed transfer cancellation', () => {
  it('does not cancel another project transfer when the default shelf project changes on refetch', async () => {
    const { api, queryClient, dialog } = await openTransfer(undefined, '');
    let releaseUpload!: () => void;
    const gate = new Promise<void>((resolve) => { releaseUpload = resolve; });
    const uploadFile = api.uploadImportQueueItem.bind(api);
    const upload = vi.spyOn(api, 'uploadImportQueueItem').mockImplementation(async (...args) => {
      await gate;
      return uploadFile(...args);
    });
    const start = vi.spyOn(api, 'startImportQueue');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Загрузить' }));
    await waitFor(() => expect(upload).toHaveBeenCalledTimes(1));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Скрыть очередь' }));
    const otherProject = await api.createProject({ title: 'Другой проект' });
    await api.createImportQueue(otherProject.id, {
      items: [{ clientItemId: 'other-project-file', position: 1, sourceFileName: 'Том 1.epub', detectedBookOrder: 1 }],
    }, { idempotencyKey: 'idem-create-other-project-queue' });
    const projects = await api.listProjects();
    vi.spyOn(api, 'listProjects').mockResolvedValue({ ...projects, data: [otherProject, ...projects.data.filter((project) => project.id !== otherProject.id)] });
    const cancel = vi.spyOn(api, 'cancelImportQueue');
    await act(async () => { await queryClient.refetchQueries({ queryKey: manuscriptQueryKeys.shelf({ projectId: null }), exact: true }); });
    const otherDialog = await screen.findByRole('dialog', { name: 'Загрузка книг' });
    await within(otherDialog).findByText('Том 1.epub');
    try {
      // This tab holds no file of the other queue, so its transfer shows as interrupted (R3.5.1).
      const cancelOther = within(otherDialog).getByRole('button', { name: 'Отменить загрузку' });
      fireEvent.click(cancelOther);
      const confirmation = screen.queryByRole('dialog', { name: 'Отменить передачу?' });
      if (confirmation) fireEvent.click(within(confirmation).getByRole('button', { name: 'Отменить передачу' }));
      expect(upload.mock.calls[0]![3].signal?.aborted).toBe(false);
      expect(cancelOther).toHaveProperty('disabled', true);
      expect(cancel).not.toHaveBeenCalled();
    } finally {
      releaseUpload();
      if (upload.mock.calls[0]![3].signal?.aborted) {
        await waitFor(() => expect(cancel).toHaveBeenCalledTimes(1));
      } else {
        await waitFor(() => expect(start).toHaveBeenCalledTimes(1));
      }
    }
    expect(cancel).not.toHaveBeenCalled();
    expect(upload).toHaveBeenCalledTimes(1);
  });

  it.each(['abort rejection', 'late upload success'] as const)(
    'settles %s before canceling and never uploads the next file or starts the queue', async (outcome) => {
      const files = [5, 6].map((volume) => new File(['book'], `Том ${volume}.epub`, { type: 'application/epub+zip' }));
      const { api, dialog } = await openTransfer(files);
      const uploadFile = api.uploadImportQueueItem.bind(api);
      const events: string[] = [];
      let settleUpload!: () => void;
      const gate = new Promise<void>((resolve) => { settleUpload = resolve; });
      const upload = vi.spyOn(api, 'uploadImportQueueItem').mockImplementation(async (...args) => {
        args[3].signal?.addEventListener('abort', () => events.push('abort'), { once: true });
        await gate;
        events.push('settled');
        if (outcome === 'abort rejection' && args[3].signal?.aborted) {
          throw new NetworkApiError(new DOMException('Transfer canceled', 'AbortError'));
        }
        return uploadFile(...args);
      });
      const cancelQueue = api.cancelImportQueue.bind(api);
      const cancel = vi.spyOn(api, 'cancelImportQueue').mockImplementation(async (...args) => {
        events.push('cancel');
        return cancelQueue(...args);
      });
      const start = vi.spyOn(api, 'startImportQueue');
      fireEvent.click(within(dialog).getByRole('button', { name: 'Загрузить' }));
      await waitFor(() => expect(upload).toHaveBeenCalledTimes(1));

      try {
        const requestCancel = within(dialog).getByRole('button', { name: 'Отменить передачу' });
        expect(requestCancel).toHaveProperty('disabled', false);
        fireEvent.click(requestCancel);
        let confirmation = await screen.findByRole('dialog', { name: 'Отменить передачу?' });
        expect(events).toEqual([]);
        expect(cancel).not.toHaveBeenCalled();
        fireEvent.click(within(confirmation).getByRole('button', { name: 'Назад' }));
        expect(events).toEqual([]);
        expect(cancel).not.toHaveBeenCalled();
        fireEvent.click(requestCancel);
        confirmation = await screen.findByRole('dialog', { name: 'Отменить передачу?' });
        fireEvent.click(within(confirmation).getByRole('button', { name: 'Отменить передачу' }));
        await waitFor(() => expect(events).toEqual(['abort']));
        expect(cancel).not.toHaveBeenCalled();
      } finally {
        settleUpload();
        if (!events.includes('abort')) await waitFor(() => expect(start).toHaveBeenCalledTimes(1));
      }

      await waitFor(() => expect(cancel).toHaveBeenCalledTimes(1));
      await within(dialog).findByRole('heading', { name: 'Очередь отменена' });
      expect(events).toEqual(['abort', 'settled', 'cancel']);
      expect(upload).toHaveBeenCalledTimes(1);
      expect(start).not.toHaveBeenCalled();
      expect(screen.queryByRole('dialog', { name: 'Отменить передачу?' })).toBeNull();
    },
  );

  it.each(['canceled', 'uploading', 'running', 'succeeded', 'failed', 'unavailable'] as const)(
    'reconciles a lost cancel response with exact GET reporting %s', async (outcome) => {
      const { api, dialog } = await openTransfer();
      const getQueue = api.getImportQueue.bind(api);
      const cancelQueue = api.cancelImportQueue.bind(api);
      let cancelAttempted = false;
      const get = vi.spyOn(api, 'getImportQueue').mockImplementation(async (...args) => {
        if (cancelAttempted && outcome === 'unavailable') throw new NetworkApiError(new TypeError('offline'));
        const queue = await getQueue(...args);
        return cancelAttempted && (outcome === 'succeeded' || outcome === 'failed' || outcome === 'running')
          ? { ...queue, status: outcome, version: queue.version + 1, items: queue.items.map((item) => ({
            ...item,
            preflightStatus: 'ready',
            processingStatus: outcome === 'running' ? 'canceling' : outcome,
          })) }
          : queue;
      });
      const upload = vi.spyOn(api, 'uploadImportQueueItem').mockImplementation(async (...args) => {
        await new Promise<void>((_resolve, reject) => {
          args[3].signal?.addEventListener('abort', () => reject(new NetworkApiError(new DOMException('Canceled', 'AbortError'))), { once: true });
        });
        return getQueue(args[0], args[1]);
      });
      const start = vi.spyOn(api, 'startImportQueue');
      const cancel = vi.spyOn(api, 'cancelImportQueue').mockImplementation(async (...args) => {
        if (outcome === 'canceled') await cancelQueue(...args);
        cancelAttempted = true;
        throw new NetworkApiError(new TypeError('response lost'));
      });
      fireEvent.click(within(dialog).getByRole('button', { name: 'Загрузить' }));
      await waitFor(() => expect(upload).toHaveBeenCalledTimes(1));
      get.mockClear();
      fireEvent.click(within(dialog).getByRole('button', { name: 'Отменить передачу' }));
      const confirmation = await screen.findByRole('dialog', { name: 'Отменить передачу?' });
      fireEvent.click(within(confirmation).getByRole('button', { name: 'Отменить передачу' }));
      await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Отменить передачу?' })).toBeNull());

      expect(get).toHaveBeenCalledWith(projectId, cancel.mock.calls[0]![1]);
      expect(upload).toHaveBeenCalledTimes(1);
      expect(start).not.toHaveBeenCalled();
      if (outcome === 'canceled') {
        expect(within(dialog).getByRole('heading', { name: 'Очередь отменена' })).toBeTruthy();
      } else if (outcome === 'succeeded') {
        expect(within(dialog).getByRole('alert', { name: 'Очередь уже завершена. Отмена не выполнена.' })).toBeTruthy();
        expect(dialog.querySelector('[data-queue-state="complete"]')).not.toBeNull();
        expect(within(dialog).queryByText('Очередь отменена')).toBeNull();
      } else if (outcome === 'failed') {
        expect(dialog.querySelector('[data-queue-state="failed"]')).not.toBeNull();
        expect(within(dialog).getByLabelText('Заменить очередь')).toBeTruthy();
        expect(within(dialog).queryByText('Очередь отменена')).toBeNull();
      } else {
        expect(within(dialog).getByText('Том 5.epub')).toBeTruthy();
        expect(dialog.querySelector(`[data-queue-state="${outcome === 'running' ? 'processing' : 'uploading'}"]`)).not.toBeNull();
        if (outcome === 'running') expect(within(dialog).getByText('Останавливаем')).toBeTruthy();
        expect(within(dialog).queryByText('Ошибка')).toBeNull();
        cancel.mockImplementation(cancelQueue);
        fireEvent.click(within(dialog).getByRole('button', { name: 'Повторить отмену' }));
        const retry = await screen.findByRole('dialog', { name: 'Отменить передачу?' });
        fireEvent.click(within(retry).getByRole('button', { name: 'Отменить передачу' }));
        await within(dialog).findByRole('heading', { name: 'Очередь отменена' });
        expect(cancel).toHaveBeenCalledTimes(2);
        expect(upload).toHaveBeenCalledTimes(1);
        expect(start).not.toHaveBeenCalled();
      }
    },
  );

  it('settles an already-issued start before canceling the accepted queue', async () => {
    const { api, dialog } = await openTransfer();
    const uploadFile = api.uploadImportQueueItem.bind(api);
    const startQueue = api.startImportQueue.bind(api);
    const cancelQueue = api.cancelImportQueue.bind(api);
    let finishUpload!: () => void;
    let finishStart!: () => void;
    const uploadGate = new Promise<void>((resolve) => { finishUpload = resolve; });
    const startGate = new Promise<void>((resolve) => { finishStart = resolve; });
    const events: string[] = [];
    const upload = vi.spyOn(api, 'uploadImportQueueItem').mockImplementation(async (...args) => {
      await uploadGate;
      return uploadFile(...args);
    });
    const start = vi.spyOn(api, 'startImportQueue').mockImplementation(async (...args) => {
      events.push('start sent');
      await startGate;
      events.push('start settled');
      return startQueue(...args);
    });
    const cancel = vi.spyOn(api, 'cancelImportQueue').mockImplementation(async (...args) => {
      events.push('cancel');
      return cancelQueue(...args);
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Загрузить' }));
    await waitFor(() => expect(upload).toHaveBeenCalledTimes(1));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Отменить передачу' }));
    const confirmation = await screen.findByRole('dialog', { name: 'Отменить передачу?' });
    finishUpload();
    try {
      await waitFor(() => expect(start).toHaveBeenCalledTimes(1));
      fireEvent.click(within(confirmation).getByRole('button', { name: 'Отменить передачу' }));
      await waitFor(() => expect(upload.mock.calls[0]![3].signal?.aborted).toBe(true));
      expect(cancel).not.toHaveBeenCalled();
    } finally {
      finishStart();
    }
    await within(dialog).findByRole('heading', { name: 'Очередь отменена' });
    expect(events).toEqual(['start sent', 'start settled', 'cancel']);
    expect(cancel).toHaveBeenCalledTimes(1);
  });
});
