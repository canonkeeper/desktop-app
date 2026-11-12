import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { projectId } from '../../../api/mock/repository/fixtures';
import type { AccountQuota } from '../../../entities/account-quota/api';
import { QuotaNoteProvider, type QuotaNote } from '../../../entities/account-quota/quota-note';
import { useSagaChoice } from '../../../ui/navigation/SagaMenu';
import { SpaceLimitLine } from '../../../entities/account-quota/ui/SpaceLimitLine';
import { ShelfQuotaHints } from './ShelfQuotaHints';

/** The port of the site: the tail of the quiet line names the tier and the next one (the real one is TierLineTail). */
const note: QuotaNote = (code, place) => ({
  offer: null,
  tariff: null,
  line: code === 'quota.projects' ? ' · больше — на «Художнике»' : <> на тарифе «Бесплатно» · {place ? <a href="/tariff">больше — на «Художнике»</a> : 'больше — на «Художнике»'}</>,
});

function renderHints(patch: (quota: AccountQuota) => AccountQuota) {
  const api = createMockApiClient();
  const read = api.getAccountQuota.bind(api);
  vi.spyOn(api, 'getAccountQuota').mockImplementation(async () => patch(await read()));
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <QuotaNoteProvider value={note}><ShelfQuotaHints api={api} projectId={projectId} /></QuotaNoteProvider>
    </QueryClientProvider>,
  );
  return api;
}

// specs/2026-11-10-ux2-patterns-refusals.md PR2.7: a limit of space is a quiet line before the press, not a block.
describe('the limits of the shelf before the press', () => {
  // specs/2026-11-11-tier-limits.md TL4.2: the books count in the saga on screen, not in all the sagas.
  it('says the books of this saga at their limit in one quiet line with the way to «Тариф», no block', async () => {
    renderHints((quota) => ({
      ...quota, ownedProjects: quota.ownedProjects.map((project) => (project.projectId === projectId
        ? { ...project, sources: { ...project.sources, books: { used: 1, limit: 1 } } } : project)),
    }));
    const line = await screen.findByText(/^Книг 1 из 1/u);
    expect(line.textContent).toBe('Книг 1 из 1 в этой саге на тарифе «Бесплатно» · больше — на «Художнике»');
    expect(screen.getByRole('link', { name: 'больше — на «Художнике»' }).getAttribute('href')).toBe('/tariff');
    expect(screen.queryByRole('note', { name: /закончилось/u })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Тариф' })).toBeNull();
  });

  it('says nothing while there is room', async () => {
    const api = renderHints((quota) => ({ ...quota, account: { ...quota.account, books: { used: 1, limit: 1 } } }));
    await waitFor(() => expect(api.getAccountQuota).toHaveBeenCalled());
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(screen.queryByText(/^Книг/u)).toBeNull();
    expect(screen.queryByRole('note')).toBeNull();
  });

  it('keeps the block before the press when the uploads of the day are over: nothing can be uploaded today', async () => {
    renderHints((quota) => ({ ...quota, imports: { used: 30, limit: 30 } }));
    expect(await screen.findByRole('note', { name: 'Загрузки книг на сегодня закончились — 30 из 30' })).toBeTruthy();
  });

  it('says the sagas at their limit under «Новая сага» of the menu of the saga', () => {
    const quota = { projects: { used: 1, limit: 1 } } as AccountQuota;
    function Menu() {
      const choice = useSagaChoice({
        choosing: false, current: { id: 'p1', title: 'Отмель' }, failed: false, onChoose: vi.fn(), onCreate: vi.fn(), sagas: [{ id: 'p1', title: 'Отмель' }],
        createNote: <SpaceLimitLine as="span" code="quota.projects" quota={quota} />,
      });
      return <>{choice.saga ? <button onClick={(event) => choice.saga!.onOpenMenu(event.currentTarget)} type="button">Сага</button> : null}{choice.menu}</>;
    }
    render(<QuotaNoteProvider value={note}><Menu /></QuotaNoteProvider>);
    fireEvent.click(screen.getByRole('button', { name: 'Сага' }));
    expect(screen.getByRole('menuitem', { name: /^Новая сага/u }).textContent).toBe('Новая сагаСаг 1 из 1 · больше — на «Художнике»');
  });
});
