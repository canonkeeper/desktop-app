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

  // specs/2026-11-19-desk-trust.md TW5: the license covers the code, not the books — under the open core; what stays if the site
  // closes — a block after the network.
  it('says the license covers the code and not the books, and what stays if the site closes', async () => {
    shellOfTheWindow();
    renderPart(AboutPart, appOnTheComputer);
    const core = (await screen.findByText('Ядро CanonKeeper открыто')).parentElement!;
    expect(core.nextElementSibling?.textContent).toBe('Лицензия касается кода программы, а не ваших книг: права на тексты остаются у вас');
    const network = screen.getByRole('group', { name: 'Куда приложение ходит в сеть' });
    const site = screen.getByRole('group', { name: 'Если сайт CanonKeeper закроется' });
    expect(network.nextElementSibling).toBe(site);
    expect(within(site).getByText('Книги, чтение, поиск, заметки и правка глав останутся: они работают на этом компьютере без сайта. Помощник через вашего провайдера тоже продолжит работать. Перестанут работать помощник по аккаунту и перенос саг с сайта; обновления будут приходить с GitHub, пока CanonKeeper там')).toBeTruthy();
  });

  // specs/2026-11-14-desk-journal.md JR4.3: the empty list says when addresses appear, and no line of a proxy stands without one.
  it('says the app goes nowhere while the list of the server is empty, and when addresses appear', async () => {
    shellOfTheWindow();
    renderPart(AboutPart, appOnTheComputer);
    const network = await screen.findByRole('group', { name: 'Куда приложение ходит в сеть' });
    expect(await within(network).findByText('Сейчас никуда')).toBeTruthy();
    expect(within(network).getByText('Адреса появятся здесь, когда вы подключите помощника, свяжете приложение с аккаунтом или включите проверку обновлений.')).toBeTruthy();
    expect(within(network).queryByText(/прокси/u)).toBeNull();
  });

  it('names the proxy of the environment under the list by its host', async () => {
    shellOfTheWindow();
    renderPart(AboutPart, {
      ...appOnTheComputer, proxy: 'proxy.example.com',
      network: [{ host: '127.0.0.1:11434', purpose: 'программа на этом компьютере, помощник через «Свой ИИ»' }],
    });
    const network = await screen.findByRole('group', { name: 'Куда приложение ходит в сеть' });
    expect((await within(network).findAllByRole('listitem')).map((row) => row.textContent))
      .toEqual(['127.0.0.1:11434 — программа на этом компьютере, помощник через «Свой ИИ»']);
    expect(within(network).getByText('Запросы к помощнику и серверу идут через прокси из настроек окружения: proxy.example.com')).toBeTruthy();
  });

  it('names each host of the list of the server and what for', async () => {
    shellOfTheWindow();
    renderPart(AboutPart, { ...appOnTheComputer, network: [{ host: 'api.provider.example', purpose: 'помощник через «Свой ИИ»' }] });
    const network = await screen.findByRole('group', { name: 'Куда приложение ходит в сеть' });
    expect((await within(network).findAllByRole('listitem')).map((row) => row.textContent)).toEqual(['api.provider.example — помощник через «Свой ИИ»']);
    expect(within(network).queryByText('Сейчас никуда')).toBeNull();
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

// specs/2026-11-15-desk-mcp.md MC3.5: while the programs of the author may read the sagas, a line of its own stands under the
// list of the addresses and before the proxy — never in the list.
describe('«О программе» while the programs read', () => {
  it('says the reading of the programs only while it is on', async () => {
    shellOfTheWindow();
    renderPart(AboutPart, { ...appOnTheComputer, programsRead: true, proxy: 'proxy.example.test:3128' });
    const network = await screen.findByRole('group', { name: 'Куда приложение ходит в сеть' });
    const line = await within(network).findByText('Чтение саг вашим Claude или Codex включено — куда они отправляют прочитанное, решают они');
    const proxy = within(network).getByText(/через прокси/u);
    expect(line.compareDocumentPosition(proxy) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(within(network).getByText('Сейчас никуда').compareDocumentPosition(line) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('has no line of the programs while the reading is off', async () => {
    shellOfTheWindow();
    renderPart(AboutPart, appOnTheComputer);
    const network = await screen.findByRole('group', { name: 'Куда приложение ходит в сеть' });
    await within(network).findByText('Сейчас никуда');
    expect(within(network).queryByText(/Claude или Codex/u)).toBeNull();
  });
});
