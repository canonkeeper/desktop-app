import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { NameField } from './NameField';

type Api = ReturnType<typeof createMockApiClient>;

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

function Harness({ api }: { api: Api }) {
  const [name, setName] = useState('');
  return <NameField api={api} onChange={setName} projectId={projectId} value={name} />;
}

function renderName(api: Api) {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Harness api={api} />
    </QueryClientProvider>,
  );
  return screen.getByRole('combobox', { name: /^Имя/u });
}

async function settle() {
  await act(async () => { await vi.advanceTimersByTimeAsync(300); });
}

const unknownMark = 'В книге такого имени нет — запись сработает, когда оно появится в тексте или в ваших материалах.';

// specs/2026-11-10-ux8-memory-records.md MR1.3: the names of the saga as the map finds them, asked once the author pauses.
describe('NameField (MR1.3)', () => {
  it('suggests the names of the saga once the author pauses and takes one by the arrows and Enter', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const api = createMockApiClient();
    const search = vi.spyOn(api, 'searchSagaMap');
    const input = renderName(api);
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: 'М' } });
    await act(async () => { await vi.advanceTimersByTimeAsync(100); });
    fireEvent.change(input, { target: { value: 'Ма' } });
    await settle();
    // Not oftener than every 300 ms: «М» was never asked.
    expect(search.mock.calls.map(([, options]) => options.q).filter(Boolean)).toEqual(['Ма']);
    expect(search.mock.calls.every(([, options]) => options.limit === 8)).toBe(true);
    const list = await screen.findByRole('listbox', { name: 'Имена саги' });
    const options = within(list).getAllByRole('option');
    const mara = options.findIndex((option) => option.textContent?.startsWith('Мара'));
    expect(mara).toBeGreaterThanOrEqual(0);
    for (let step = 0; step <= mara; step += 1) fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(input.getAttribute('aria-activedescendant')).toBe(options[mara]!.id);
    fireEvent.keyDown(input, { key: 'Enter' });
    expect((input as HTMLInputElement).value).toBe('Мара');
    expect(screen.queryByRole('listbox')).toBeNull();
    await settle();
    expect(screen.queryByText(unknownMark)).toBeNull();
  });

  it('marks a name the book does not know, and lets it stay', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const input = renderName(createMockApiClient());
    fireEvent.change(input, { target: { value: 'Дракон Ульм' } });
    await settle();
    expect(await screen.findByText(unknownMark)).toBeTruthy();
    expect((input as HTMLInputElement).value).toBe('Дракон Ульм');
  });

  it('a saga without a map suggests and marks nothing', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const api = createMockApiClient();
    vi.spyOn(api, 'searchSagaMap').mockResolvedValue({ entries: [] });
    const input = renderName(api);
    fireEvent.change(input, { target: { value: 'Дракон Ульм' } });
    await settle();
    await act(async () => { await vi.advanceTimersByTimeAsync(50); });
    expect(screen.queryByText(unknownMark)).toBeNull();
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(screen.getByText(/^Необязательно\./u)).toBeTruthy();
  });
});
