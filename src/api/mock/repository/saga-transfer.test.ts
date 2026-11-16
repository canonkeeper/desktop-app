// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import { createMockApiClient } from '../index';

/** The computer of the app with its local author signed in, linked or not (specs/2026-11-16-desk-site-saga.md TR2). */
async function theApp(linked: boolean) {
  const api = createMockApiClient();
  api.startDesktop();
  if (linked) api.setScenario({ ...api.getScenario(), accountLinked: true });
  await api.openDesktopSession({ code: 'code-of-the-shell' });
  return api;
}

describe('the transfer of a saga from the site in the mock', () => {
  it('starts only for a linked app, waits with the page of the site and stops as the server does', async () => {
    const unlinked = await theApp(false);
    await expect(unlinked.startSiteSagaTransfer()).rejects.toMatchObject({ status: 409, code: 'account.not_linked' });
    expect(await unlinked.getSiteSagaTransfer()).toMatchObject({ phase: 'none', id: null });

    const api = await theApp(true);
    const waiting = await api.startSiteSagaTransfer();
    expect(waiting).toMatchObject({ phase: 'waiting', email: 'a…@example.test', name: null, bytes: null, code: null });
    expect(waiting.address).toMatch(/^https:\/\/canonkeeper\.example\.test\/transfer\?port=24567&request=.+&state=/u);
    expect(await api.cancelSiteSagaTransfer({ id: waiting.id! })).toMatchObject({ phase: 'failed', code: 'canceled', id: waiting.id });
    expect(await api.cancelSiteSagaTransfer({ id: waiting.id! })).toMatchObject({ phase: 'none', id: null });
  });

  // TR2.2: a cancel names its transfer, as the server's: the outcome put away after a new start leaves the new one waiting.
  it('stops only the transfer it names', async () => {
    const api = await theApp(true);
    const first = await api.startSiteSagaTransfer();
    expect(await api.cancelSiteSagaTransfer({ id: '00000000-0000-4000-9000-000000000999' })).toMatchObject({ id: first.id, phase: 'waiting' });
    expect(await api.cancelSiteSagaTransfer({ id: first.id! })).toMatchObject({ id: first.id, phase: 'failed', code: 'canceled' });
    const second = await api.startSiteSagaTransfer();
    expect(await api.cancelSiteSagaTransfer({ id: first.id! })).toMatchObject({ id: second.id, phase: 'waiting' });
    expect(await api.getSiteSagaTransfer()).toMatchObject({ id: second.id, phase: 'waiting' });
    await expect(api.cancelSiteSagaTransfer({} as { id: string })).rejects.toMatchObject({ status: 400, code: 'validation.failed' });
  });

  it('goes one phase a read by the scenario up to the saga open, each transfer a new one', async () => {
    const api = await theApp(true);
    const first = await api.startSiteSagaTransfer();
    api.setScenario({ ...api.getScenario(), siteSagaTransfer: 'done' });
    const phases = [];
    for (let read = 0; read < 4; read += 1) {
      const now = await api.getSiteSagaTransfer();
      phases.push([now.phase, now.bytes, now.name]);
    }
    expect(phases).toEqual([
      ['downloading', 0, 'Хроники Белого порта.cksaga'], ['downloading', 12 * 1024 * 1024, 'Хроники Белого порта.cksaga'],
      ['opening', 12 * 1024 * 1024, 'Хроники Белого порта.cksaga'], ['done', null, null],
    ]);
    const done = await api.getSiteSagaTransfer();
    expect(done.opened).toMatchObject({ name: 'Хроники Белого порта', renamed: false, report: { books: 1, chapters: 2 } });
    expect((await api.listProjects()).data.map((saga) => saga.id)).toContain(done.projectId);
    expect(await api.cancelSiteSagaTransfer({ id: done.id! })).toMatchObject({ phase: 'none' });
    api.setScenario({ ...api.getScenario(), siteSagaTransfer: 'expired' });
    const second = await api.startSiteSagaTransfer();
    expect(second.id).not.toBe(first.id);
    expect(await api.getSiteSagaTransfer()).toMatchObject({ id: second.id, phase: 'failed', code: 'expired' });
  });

  it('the page /transfer finds a request of another account or one gone by the scenario', async () => {
    const api = createMockApiClient();
    api.setScenario({ ...api.getScenario(), sagaTransferRequest: 'other_account' });
    await expect(api.getSagaTransferRequest('r')).rejects.toMatchObject({ status: 409, code: 'saga_transfer.other_account' });
    await expect(api.startSagaTransfer({ request: 'r', projectId: 'project-white-port', state: 'app-state', port: 24567 }))
      .rejects.toMatchObject({ status: 409, code: 'saga_transfer.other_account' });
    api.setScenario({ ...api.getScenario(), sagaTransferRequest: 'expired' });
    await expect(api.getSagaTransferRequest('r')).rejects.toMatchObject({ status: 410, code: 'saga_transfer.expired' });
  });

  it('gives the page /transfer the return of the app for a saga the author owns', async () => {
    const api = createMockApiClient();
    expect(await api.getSagaTransferRequest('00000000-0000-4000-8000-000000000301')).toHaveProperty('expiresAt');
    const started = await api.startSagaTransfer({ request: '00000000-0000-4000-8000-000000000301', projectId: 'project-white-port', state: 'app-state', port: 24567 });
    expect(started.callbackUrl).toMatch(/^http:\/\/127\.0\.0\.1:24567\/desktop\/transfer\/callback\?code=.+&state=app-state$/u);
    await expect(api.startSagaTransfer({ request: 'r', projectId: 'project-unknown', state: 'app-state', port: 24567 }))
      .rejects.toMatchObject({ status: 404 });
    await expect(api.startSagaTransfer({ request: 'r', projectId: 'project-white-port', state: 'app-state', port: 443 }))
      .rejects.toMatchObject({ status: 400, code: 'request.validation' });
  });
});
