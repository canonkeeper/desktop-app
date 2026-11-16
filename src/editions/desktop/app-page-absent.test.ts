// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it, vi } from 'vitest';
import { routeOfLocation } from '../../routing/router';
import { createAppRoute } from '../../shared/navigation/app-route';

vi.mock('@edition', async () => await import('.'));

// specs/2026-11-16-desk-app-page.md AP2.1: the page «CanonKeeper на компьютере» is a page of the site alone. In the window
// of the app its address is the route of no page, as any address the app does not know — the tab and the shell read it so.
describe('the address of the page of the app of the site in the app', () => {
  it('is the route of no page', () => {
    expect(routeOfLocation({ pathname: '/desktop', searchStr: '', state: {} })).toEqual({ ...createAppRoute('/no-such-page'), path: '/desktop' });
    expect(routeOfLocation({ pathname: '/settings', searchStr: '?part=look', state: {} }).surface).toBe('settings');
  });
});
