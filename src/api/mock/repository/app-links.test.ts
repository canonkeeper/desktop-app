import { describe, expect, it } from 'vitest';
import { createAppLinkOperations } from './app-links';
import { createMockRepositoryContext } from './context';

const request = { challenge: 'c'.repeat(43), state: 'state-of-the-app', port: 24567, platform: 'windows' as const };

// specs/2026-11-13-desk-link.md LK1.4, LK1.5, LK4.1, LK4.2: the mock of the page /link and of «Приложения на компьютере».
describe('the mock of the link of the app', () => {
  it('answers the address on 127.0.0.1 of the port, links the app, refuses a sixth and unlinks only an app of the account', async () => {
    const context = createMockRepositoryContext();
    const api = createAppLinkOperations(context);
    const started = await api.startAppLink(request);
    const address = new URL(started.callbackUrl);
    expect([address.protocol, address.host, address.pathname]).toEqual(['http:', '127.0.0.1:24567', '/desktop/link/callback']);
    expect(address.searchParams.get('state')).toBe('state-of-the-app');
    await expect(api.startAppLink({ ...request, port: 443 })).rejects.toMatchObject({ status: 400 });

    const { apps } = await api.listLinkedApps();
    expect(apps).toEqual([expect.objectContaining({ name: 'Компьютер с Windows', platform: 'windows', seenAt: null })]);
    for (let index = 1; index < 5; index += 1) await api.startAppLink(request);
    await expect(api.startAppLink(request)).rejects.toMatchObject({ status: 409 });

    context.state.linkedApps = [...context.state.linkedApps, { ...context.state.linkedApps[0]!, userId: 'another-account', id: 'another-app' }];
    await expect(api.revokeLinkedApp('another-app')).rejects.toMatchObject({ status: 404 });
    await api.revokeLinkedApp(apps[0]!.id);
    expect((await api.listLinkedApps()).apps.map((app) => app.id)).not.toContain(apps[0]!.id);
    await expect(api.revokeLinkedApp(apps[0]!.id)).rejects.toMatchObject({ status: 404 });
    await api.startAppLink(request);
    expect(new Set((await api.listLinkedApps()).apps.map((app) => app.id)).size).toBe(5);
  });

  // LK1.4: as the server, the mock links no app of an address not confirmed.
  it('refuses the link of an account whose address is not confirmed', async () => {
    const context = createMockRepositoryContext();
    const api = createAppLinkOperations(context);
    context.state.session = { ...context.state.session!, user: { ...context.state.session!.user, emailVerified: false } };
    await expect(api.startAppLink(request)).rejects.toMatchObject({ status: 403, code: 'auth.email_unverified' });
    expect((await api.listLinkedApps()).apps).toEqual([]);
  });
});
