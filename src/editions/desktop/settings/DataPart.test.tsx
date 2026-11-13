// SPDX-License-Identifier: AGPL-3.0-only
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { DataPart } from './DataPart';
import { appOnTheComputer, noShell, renderPart, shellOfTheWindow } from './settings.test-helpers';

afterEach(noShell);

// specs/2026-11-13-desk-settings.md ST2.4 (board 44·11): where the data lie with «Открыть папку», the place they take and
// the copies before an update by their day and version.
describe('«Данные» of «Настройки»', () => {
  it('say where the data lie, how much they take and which copies wait before an update', async () => {
    const invoke = shellOfTheWindow();
    renderPart(DataPart, appOnTheComputer);
    const where = await screen.findByRole('group', { name: 'Где лежат данные' });
    expect(within(where).getByText('C:\\Users\\author\\AppData\\Local\\ru.canonkeeper.desktop\\data')).toBeTruthy();
    expect(screen.getByText('Занято на диске: 1,2 ГБ')).toBeTruthy();
    const copies = screen.getByRole('list', { name: 'Копии перед обновлением' });
    expect(within(copies).getAllByRole('listitem').map((row) => row.textContent)).toEqual(['12 ноября 2026 · версия 1.1.4', '2 ноября 2026']);
    fireEvent.click(within(where).getByRole('button', { name: 'Открыть папку' }));
    await waitFor(() => expect(invoke).toHaveBeenCalledWith('reveal_data_dir', undefined));
  });

  it('have no block of copies before the first update', async () => {
    shellOfTheWindow();
    renderPart(DataPart, { ...appOnTheComputer, backups: [] });
    await screen.findByText('Занято на диске: 1,2 ГБ');
    expect(screen.queryByText('Копии перед обновлением')).toBeNull();
  });

  it('say «Считаю…» while the place is counted and ask again until it is', async () => {
    shellOfTheWindow();
    const asked = renderPart(DataPart,
      { ...appOnTheComputer, usage: { bytes: null, counting: true } },
      { ...appOnTheComputer, usage: { bytes: 1_288_490_188, counting: true } },
      appOnTheComputer);
    expect(await screen.findByText('Занято на диске: Считаю…')).toBeTruthy();
    expect(await screen.findByText('Занято на диске: 1,2 ГБ · Считаю…', undefined, { timeout: 5_000 })).toBeTruthy();
    expect(await screen.findByText('Занято на диске: 1,2 ГБ', undefined, { timeout: 5_000 })).toBeTruthy();
    expect(asked).toHaveBeenCalledTimes(3);
  }, 15_000);

  it('have no «Открыть папку» in a browser without the shell', async () => {
    noShell();
    renderPart(DataPart, appOnTheComputer);
    await screen.findByText('Занято на диске: 1,2 ГБ');
    expect(screen.queryByRole('button', { name: 'Открыть папку' })).toBeNull();
  });

  it('say the folder did not open when the shell could not open it', async () => {
    const invoke = shellOfTheWindow();
    invoke.mockRejectedValueOnce('not opened');
    renderPart(DataPart, appOnTheComputer);
    fireEvent.click(await screen.findByRole('button', { name: 'Открыть папку' }));
    expect(await screen.findByText('Не удалось открыть папку')).toBeTruthy();
  });
});
