// SPDX-License-Identifier: AGPL-3.0-only
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiStatusError } from '../../../shared/api';
import { DataPart } from './DataPart';
import { appOnTheComputer, noShell, renderPart, renderPartWith, shellOfTheWindow } from './settings.test-helpers';

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

// specs/2026-11-13-desk-erase.md ER1.1 (board 44·11, 44·12, `Pattern/Desktop/Erase Dialog`): the last row of «Данные»
// opens the last question; «Стереть всё» stays closed until the word is in the field and says why; after it the window
// gives no way out — the app closes itself.
describe('«Стереть все данные…» of «Данные»', () => {
  async function openTheQuestion(eraseDesktopData = vi.fn<() => Promise<void>>(async () => undefined)) {
    noShell();
    renderPartWith(DataPart, { eraseDesktopData }, appOnTheComputer);
    fireEvent.click(await screen.findByRole('button', { name: 'Стереть все данные…' }));
    const dialog = await screen.findByRole('dialog', { name: 'Стереть все данные CanonKeeper на этом компьютере?' });
    return { dialog, eraseDesktopData };
  }

  it('is the last row of the part', async () => {
    noShell();
    renderPartWith(DataPart, {}, appOnTheComputer);
    const erase = await screen.findByRole('button', { name: 'Стереть все данные…' });
    const rows = [...document.body.querySelectorAll('button')];
    expect(rows.at(-1)).toBe(erase);
    expect(erase.getAttribute('data-variant')).toBe('danger');
  });

  it('asks in the words of the board, with the word to write and «Стереть всё» closed until it is there', async () => {
    const { dialog, eraseDesktopData } = await openTheQuestion();
    expect(within(dialog).getByRole('heading', { name: 'Стереть все данные CanonKeeper на этом компьютере?' })).toBeTruthy();
    expect(within(dialog).getByText(
      'Удалятся все саги, книги, главы в работе, чаты и настройки. Вернуть их будет нельзя. Файлы саг, которые вы сохранили, и папки Obsidian останутся.',
    )).toBeTruthy();
    const word = within(dialog).getByRole('textbox', { name: 'Напишите «стереть»' });
    expect((word as HTMLInputElement).value).toBe('');
    const hint = '«Стереть всё» станет доступна, когда в поле будет это слово.';
    expect(within(dialog).getByText(hint)).toBeTruthy();
    expect(within(dialog).getAllByRole('button').map((button) => button.getAttribute('aria-label') ?? button.textContent)).toEqual(['Закрыть', 'Отмена', 'Стереть всё']);
    const erase = within(dialog).getByRole('button', { name: 'Стереть всё' });
    expect(erase.getAttribute('aria-disabled')).toBe('true');
    fireEvent.change(word, { target: { value: 'стерет' } });
    fireEvent.click(erase);
    expect(document.querySelector('[data-reason]')?.textContent).toBe(hint);
    expect(eraseDesktopData).not.toHaveBeenCalled();
  });

  it('takes the word in another case and with spaces, then says «Стираю…» and lets the author out no more', async () => {
    let answer: () => void = () => undefined;
    const { dialog, eraseDesktopData } = await openTheQuestion(vi.fn(() => new Promise<void>((resolve) => { answer = resolve; })));
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Напишите «стереть»' }), { target: { value: '  СТЕРЕТЬ ' } });
    expect(within(dialog).queryByText('«Стереть всё» станет доступна, когда в поле будет это слово.')).toBeNull();
    const erase = within(dialog).getByRole('button', { name: 'Стереть всё' });
    expect(erase.getAttribute('aria-disabled')).toBeNull();
    expect(erase.getAttribute('data-variant')).toBe('danger');
    fireEvent.click(erase);
    const erasing = await within(dialog).findByRole('button', { name: 'Стираю…' });
    fireEvent.click(erasing);
    expect(eraseDesktopData).toHaveBeenCalledOnce();
    expect(eraseDesktopData).toHaveBeenCalledWith({ confirm: 'СТЕРЕТЬ' });
    answer();
    await waitFor(() => expect(eraseDesktopData).toHaveBeenCalledOnce());
    // The shell got the request: the window stays as it is until the app closes.
    expect(await within(dialog).findByRole('button', { name: 'Стираю…' })).toBeTruthy();
    expect(within(dialog).getByRole('button', { name: 'Отмена' })).toHaveProperty('disabled', true);
    expect(within(dialog).getByRole('button', { name: 'Закрыть' })).toHaveProperty('disabled', true);
    expect(within(dialog).getByRole('textbox', { name: 'Напишите «стереть»' })).toHaveProperty('disabled', true);
    fireEvent.keyDown(dialog, { key: 'Escape' });
    expect(screen.getByRole('dialog', { name: 'Стереть все данные CanonKeeper на этом компьютере?' })).toBeTruthy();
  });

  it('says the erasure did not begin when the app did not take the request, and lets the author try again or go', async () => {
    const refused = new ApiStatusError(500, {
      type: 'about:blank', title: 'Internal Server Error', status: 500, detail: 'unexpected error', code: 'server.error', requestId: 'request-of-the-test',
    }, new Response());
    const { dialog, eraseDesktopData } = await openTheQuestion(vi.fn(async () => { throw refused; }));
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Напишите «стереть»' }), { target: { value: 'стереть' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Стереть всё' }));
    expect(await within(dialog).findByRole('alert')).toBeTruthy();
    expect(within(dialog).getByRole('alert').textContent).toBe('Стирание не началось. Попробуйте ещё раз.');
    expect(within(dialog).getByRole('button', { name: 'Стереть всё' }).getAttribute('aria-disabled')).toBeNull();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Отмена' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(eraseDesktopData).toHaveBeenCalledOnce();
  });
});

// specs/2026-11-15-desk-mcp.md MC3.4: the app never writes the settings of the programs of the author — the last question says
// how to take away what stays in them, once the reading was ever on, and nothing otherwise.
describe('«Стереть все данные…» after the programs read', () => {
  const programs = (ever: boolean) => vi.fn(async () => ({
    enabled: false, ever, open: false, failed: false, system: 'linux' as const, extensionFile: null, programs: [], lastRead: null,
    commands: {
      claudeAdd: 'claude mcp add', claudeRemove: 'claude mcp remove --scope user canonkeeper', codexAdd: 'codex mcp add',
      codexRemove: 'codex mcp remove canonkeeper', desktopEntry: '"canonkeeper": {}', desktopFile: '{}',
    },
  }));

  it('names the commands and the file of Claude Desktop once the reading was on', async () => {
    noShell();
    renderPartWith(DataPart, { getProgramAccess: programs(true) }, appOnTheComputer);
    fireEvent.click(await screen.findByRole('button', { name: 'Стереть все данные…' }));
    const dialog = await screen.findByRole('dialog', { name: 'Стереть все данные CanonKeeper на этом компьютере?' });
    expect(await within(dialog).findByText('В Claude Code и Codex останется запись canonkeeper — уберите её командами:')).toBeTruthy();
    expect(within(dialog).getByText('claude mcp remove --scope user canonkeeper')).toBeTruthy();
    expect(within(dialog).getByText('codex mcp remove canonkeeper')).toBeTruthy();
    expect(within(dialog).getByText('В Claude Desktop удалите запись "canonkeeper" из файла ~/.config/Claude/claude_desktop_config.json')).toBeTruthy();
  });

  // The commands to take the entry away hold no path: they stand without the program of the bridge (no start.bridge, no
  // commands of the state) too — never a line «уберите её командами:» with nothing under it.
  it('names the commands to take away without the commands of the state', async () => {
    noShell();
    const without = vi.fn(async () => ({ ...(await programs(true)()), commands: null }));
    renderPartWith(DataPart, { getProgramAccess: without }, appOnTheComputer);
    fireEvent.click(await screen.findByRole('button', { name: 'Стереть все данные…' }));
    const dialog = await screen.findByRole('dialog', { name: 'Стереть все данные CanonKeeper на этом компьютере?' });
    expect(await within(dialog).findByText('В Claude Code и Codex останется запись canonkeeper — уберите её командами:')).toBeTruthy();
    expect(within(dialog).getByText('claude mcp remove --scope user canonkeeper')).toBeTruthy();
    expect(within(dialog).getByText('codex mcp remove canonkeeper')).toBeTruthy();
  });

  it('says nothing of the programs when the reading was never on', async () => {
    noShell();
    const getProgramAccess = programs(false);
    renderPartWith(DataPart, { getProgramAccess }, appOnTheComputer);
    fireEvent.click(await screen.findByRole('button', { name: 'Стереть все данные…' }));
    const dialog = await screen.findByRole('dialog', { name: 'Стереть все данные CanonKeeper на этом компьютере?' });
    await waitFor(() => expect(getProgramAccess).toHaveBeenCalled());
    expect(within(dialog).queryByText(/Claude Code и Codex/u)).toBeNull();
  });
});
