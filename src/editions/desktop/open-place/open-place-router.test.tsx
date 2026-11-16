// SPDX-License-Identifier: AGPL-3.0-only
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useBlocker } from '@tanstack/react-router';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { draftChapterId } from '../../../api/mock/repository/fixtures';
import { CanonKeeperRouterProvider, createCanonKeeperRouter } from '../../../routing/router';
import { paragraphsInOrder } from '../../../shared/text/place';
import { ToastsProvider } from '../../../ui/feedback/Toast';
import { edition } from '..';
import { LinkVisits } from './link-visits';

// The router of the edition of the app, as `npm run build:desktop` makes it. The pages stand for the app inside the one
// follower of the links of the edition: «Настройки» with unsaved words, which ask «Не сохранять?» before any way out, as
// «Помощник» and a note do (useBlocker); the page of a link; any other page by its address.
vi.mock('@edition', async () => await import('..'));
vi.mock('../../../App', async () => {
  const { OpenPlace } = await import('./OpenPlace');
  const { LinkVisits: Follower } = await import('./link-visits');
  function Unsaved() {
    const leaving = useBlocker({ shouldBlockFn: () => true, withResolver: true });
    return (
      <div>
        <p data-testid="page">{`/settings unsaved ${leaving.status}`}</p>
        {leaving.status === 'blocked' ? <>
          <button onClick={leaving.reset} type="button">Остаться</button>
          <button onClick={leaving.proceed} type="button">Не сохранять</button>
        </> : null}
      </div>
    );
  }
  return {
    App: ({ api, navigate, route }: Parameters<typeof OpenPlace>[0]) => (
      <Follower navigate={navigate}>
        {route.path === '/manuscript/open'
          ? <OpenPlace api={api} navigate={navigate} route={route} />
          : route.path === '/settings' ? <Unsaved /> : <p data-testid="page">{`${route.path}?${route.query.toString()}`}</p>}
      </Follower>
    ),
  };
});

beforeAll(() => {
  vi.stubGlobal('scrollTo', vi.fn());
});

let destroy: (() => void) | undefined;
afterEach(() => {
  destroy?.();
  window.history.replaceState(null, '', '/');
  vi.restoreAllMocks();
});

/**
 * The script the shell runs in the window (desktop/src-tauri/crates/core/src/visit.js), as the core gives it its argument, on
 * the page of the backend at 127.0.0.1; it answers whether the page took the place.
 */
const visitScript = readFileSync(path.join(process.cwd(), 'desktop/src-tauri/crates/core/src/visit.js'), 'utf8');
function visit(to: string): unknown {
  const run = new Function('location', 'CANONKEEPER_VISIT_TO', 'script', 'return eval(script)') as (...args: unknown[]) => unknown;
  let answer: unknown;
  act(() => {
    answer = run({ protocol: 'http:', hostname: '127.0.0.1' }, to, visitScript);
  });
  return answer;
}

/** The saga and the chapter of a link have the ids of the server; the mock answers them with its chapter of the saga. */
const saga = '0b7c1a52-5d1e-4f43-9f6b-2a8c1e0b9d44';
const chapterOfTheLink = '7c3f0a52-5d1e-4f43-9f6b-2a8c1e0b9d45';

async function mount(at: string) {
  window.history.replaceState(null, '', at);
  const api = createMockApiClient();
  const chapter = await api.getChapter(draftChapterId, 'published');
  const book = await api.getBook(chapter.bookId);
  vi.spyOn(api, 'getChapter').mockImplementation(async (id) => {
    if (id === chapterOfTheLink) return chapter;
    throw new Error(`no chapter ${id}`);
  });
  vi.spyOn(api, 'getBook').mockResolvedValue({ ...book, projectId: saga });
  const getCurrentUser = vi.spyOn(api, 'getCurrentUser');
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createCanonKeeperRouter({ api, queryClient });
  destroy = () => router.history.destroy();
  render(<QueryClientProvider client={queryClient}><ToastsProvider><CanonKeeperRouterProvider router={router} /></ToastsProvider></QueryClientProvider>);
  const reader = new URLSearchParams({
    projectId: saga, bookId: chapter.bookId, chapterId: chapter.id, mode: 'read', paragraphId: paragraphsInOrder(chapter.paragraphs)[1]!.id,
  });
  return { getCurrentUser, reader: `/manuscript/books?${reader.toString()}` };
}

const place = `/manuscript/open?saga=${saga}&chapter=${chapterOfTheLink}&p=2`;
const page = () => screen.getByTestId('page').textContent;

// specs/2026-11-16-desk-app-links.md AL1.4: the shell gives the loaded page the place of a link, and the edition of the app
// follows it by the navigation of its router — as a link pressed inside the app: the page of the link, then the reader, in the
// same page, and a page with unsaved words asks first.
describe('a link given to the loaded page of the app', () => {
  it('takes the router through the page of the link to the reader, in the same page', async () => {
    const { getCurrentUser, reader } = await mount('/chat');
    expect((await screen.findByTestId('page')).textContent).toBe('/chat?');
    const reads = getCurrentUser.mock.calls.length;
    expect(visit(place)).toBe(true);
    await waitFor(() => expect(page()).toBe(reader), { timeout: 5000 });
    expect(`${window.location.pathname}${window.location.search}`).toBe(reader);
    expect(getCurrentUser.mock.calls.length).toBe(reads);
    // «Назад» comes back to the page the author was on: the link was a step of the app, the page of the link gave its place.
    act(() => window.history.back());
    await waitFor(() => expect(page()).toBe('/chat?'));
  });

  it('asks «Не сохранять?» over unsaved words: «Остаться» keeps the page and its address, «Не сохранять» goes on', async () => {
    const { reader } = await mount('/settings?part=helper');
    expect((await screen.findByTestId('page')).textContent).toBe('/settings unsaved idle');
    expect(visit(place)).toBe(true);
    await waitFor(() => expect(page()).toBe('/settings unsaved blocked'));
    expect(`${window.location.pathname}${window.location.search}`).toBe('/settings?part=helper');
    fireEvent.click(screen.getByRole('button', { name: 'Остаться' }));
    await waitFor(() => expect(page()).toBe('/settings unsaved idle'));
    await new Promise((resolve) => setTimeout(resolve, 100));
    expect(page()).toBe('/settings unsaved idle');
    expect(`${window.location.pathname}${window.location.search}`).toBe('/settings?part=helper');

    expect(visit(place)).toBe(true);
    await waitFor(() => expect(page()).toBe('/settings unsaved blocked'));
    fireEvent.click(screen.getByRole('button', { name: 'Не сохранять' }));
    await waitFor(() => expect(page()).toBe(reader), { timeout: 5000 });
  });

  it('keeps a link the page took before its pages were there, and follows it once they are', async () => {
    expect(visit(place)).toBe(true);
    const { reader } = await mount('/chat');
    await waitFor(() => expect(page()).toBe(reader), { timeout: 5000 });
  });

  it('takes only a place of the form of the shell', () => {
    for (const to of ['/chat', '/manuscript/books?projectId=p', `${place}&x=1`, 'https://evil.example/']) {
      const event = new CustomEvent('canonkeeper:link', { detail: to, cancelable: true });
      expect(window.dispatchEvent(event), to).toBe(true);
    }
  });

  it('is the follower of the pages of the edition of the app', () => {
    const account = edition.useAccount({ api: createMockApiClient(), navigate: vi.fn(), route: {} } as unknown as Parameters<typeof edition.useAccount>[0]);
    const followers: unknown[] = [];
    const walk = (node: unknown) => {
      if (!node || typeof node !== 'object' || !('props' in node)) return;
      const element = node as { type: unknown; props: { children?: unknown } };
      if (element.type === LinkVisits) followers.push(element);
      const children = element.props.children;
      for (const child of Array.isArray(children) ? children : [children]) walk(child);
    };
    walk(account.wrapPages!(<p>страницы</p>));
    expect(followers).toHaveLength(1);
  });
});
