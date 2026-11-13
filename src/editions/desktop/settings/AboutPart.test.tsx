// SPDX-License-Identifier: AGPL-3.0-only
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { AboutPart } from './AboutPart';
import { appOnTheComputer, noShell, renderPart, shellOfTheWindow } from './settings.test-helpers';

afterEach(noShell);

const links = [
  ['Исходный код', 'https://github.com/canonkeeper/desktop-app'],
  ['Сообщить о проблеме', 'https://github.com/canonkeeper/desktop-app/issues'],
  ['Написать нам', 'https://canonkeeper.ru/?contact=write'],
] as const;

// specs/2026-11-13-desk-settings.md ST4.1–ST4.3 (board 44·16): the version of the state, the open core and its code, where
// the app goes in the network by the list of the server, «Сообщить о проблеме» and «Написать нам» in the browser of the
// system.
describe('«О программе» of «Настройки»', () => {
  it('names the version the backend names and opens every link in the browser of the system', async () => {
    const invoke = shellOfTheWindow();
    renderPart(AboutPart, appOnTheComputer);
    expect(await screen.findByText('Версия 1.2.0')).toBeTruthy();
    expect(screen.getByText('Ядро CanonKeeper открыто')).toBeTruthy();
    for (const [name, url] of links) {
      const link = screen.getByRole('link', { name });
      expect(link.getAttribute('href')).toBe(url);
      fireEvent.click(link);
      await waitFor(() => expect(invoke).toHaveBeenLastCalledWith('open_external', { url }));
    }
    expect(invoke).toHaveBeenCalledTimes(links.length);
    // Nothing of a work not done yet: the closed module, the licences.
    expect(screen.queryByText(/закрытый модуль|Лицензии/u)).toBeNull();
  });

  it('says the app goes nowhere while the list of the server is empty, and names each host of it otherwise', async () => {
    shellOfTheWindow();
    renderPart(AboutPart, appOnTheComputer);
    const network = await screen.findByRole('group', { name: 'Куда приложение ходит в сеть' });
    expect(await within(network).findByText('Сейчас никуда: помощник не подключён')).toBeTruthy();
  });

  it('names each host of the list of the server and what for', async () => {
    shellOfTheWindow();
    renderPart(AboutPart, { ...appOnTheComputer, network: [{ host: 'api.provider.example', purpose: 'помощник через «Свой ИИ»' }] });
    const network = await screen.findByRole('group', { name: 'Куда приложение ходит в сеть' });
    expect((await within(network).findAllByRole('listitem')).map((row) => row.textContent)).toEqual(['api.provider.example — помощник через «Свой ИИ»']);
    expect(within(network).queryByText('Сейчас никуда: помощник не подключён')).toBeNull();
  });

  it('leaves the links to the browser in a browser without the shell', async () => {
    noShell();
    renderPart(AboutPart, appOnTheComputer);
    await screen.findByText('Версия 1.2.0');
    for (const [name, url] of links) {
      const link = screen.getByRole('link', { name });
      expect([link.getAttribute('href'), link.getAttribute('target'), link.getAttribute('rel')]).toEqual([url, '_blank', 'noopener noreferrer']);
    }
  });
});
