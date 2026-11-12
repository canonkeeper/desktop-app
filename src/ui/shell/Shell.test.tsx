import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { NotesContext, type NotesActions } from '../../shared/notes/notes-context';
import { AccountSessionContext, type AccountSession } from '../../shared/session/account-session';
import { TopBarStripProvider, TopBarTalkProvider } from '../navigation/TopBarStrip';
import { sagaSections } from '../saga-sections';
import { Shell, type ShellProps } from './Shell';
import { ReaderLine } from './ReaderLine';

// specs/2026-11-10-ux2-shell-core.md UC1, UC2 (S1.2–S1.4, S2.1, S2.4, S2.5): one top bar, one column of the sections in
// three groups, the drawer of a phone, the states of the shell.

/** A window of `width`×`height`: what the media queries of the shell answer. */
function windowOf(width: number, height = 900) {
  vi.stubGlobal('matchMedia', (query: string) => {
    const max = /\(max-(width|height): (\d+)px\)/u.exec(query);
    const matches = max ? (max[1] === 'width' ? width : height) <= Number(max[2]) : false;
    return { matches, media: query, addEventListener: () => undefined, removeEventListener: () => undefined };
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

const notes: NotesActions = { open: vi.fn(), openWithNavigation: vi.fn(), capture: vi.fn() };

function renderShell(props: Partial<ShellProps> = {}, session: AccountSession | null = { busy: false, onOpenContact: vi.fn() }, extra?: ReactNode) {
  const onOpen = vi.fn();
  const onOpenMenu = vi.fn();
  const view = render(
    <AccountSessionContext.Provider value={session}>
      <NotesContext.Provider value={notes}>
        <TopBarTalkProvider value={<button type="button">Поговорить</button>}>
          <TopBarStripProvider value={extra}>
            <Shell
              account={{ onLogout: vi.fn(), userName: 'Мира' }}
              label="Книги"
              onNewSaga={vi.fn()}
              onOpenSearch={vi.fn()}
              saga={{ name: 'Хроники Белого порта', demo: false, menuOpen: false, onOpenMenu }}
              sections={{ active: 'manuscript', items: sagaSections, onOpen }}
              {...props}
            >
              <h1>Книги</h1>
            </Shell>
          </TopBarStripProvider>
        </TopBarTalkProvider>
      </NotesContext.Provider>
    </AccountSessionContext.Provider>,
  );
  return { ...view, onOpen, onOpenMenu };
}

describe('the top bar of the shell', () => {
  it('stands in one order: the saga · the section — the search — «Поговорить» · «Заметка» · «Тариф» · the account', () => {
    windowOf(1440);
    renderShell({}, { busy: false, plan: { badge: <a href="/tariff">Бесплатно</a>, ending: false, line: 'Тариф «Бесплатно»' } });
    const bar = screen.getByRole('banner');
    const order = [...bar.querySelectorAll('button, a')].map((node) => node.getAttribute('aria-label') ?? node.textContent);
    expect(order).toEqual(['Сага: Хроники Белого порта', 'Поиск по книгам', 'Поговорить', 'Заметка', 'Бесплатно', 'Аккаунт']);
    // The name of the product is in the tab, not in the top bar of the author (SC1.4).
    expect(bar.textContent).not.toContain('CanonKeeper');
    expect(screen.queryByText(/Глобальный/u)).toBeNull();
  });

  // specs/2026-11-11-ux2-polish.md PL1.1, PL1.2: the top bar names the saga; the section is named by the menu and the
  // heading of the page, at every width.
  it('names the saga and not the section at every width', () => {
    for (const [width, height] of [[1440, 900], [1000, 760], [390, 844]] as const) {
      windowOf(width, height);
      const view = renderShell({ label: 'Чат', sections: { active: 'chat', items: sagaSections, onOpen: vi.fn() } });
      const bar = screen.getByRole('banner');
      expect(bar.textContent, `${width}`).toContain('Хроники Белого порта');
      expect(bar.textContent, `${width}`).not.toMatch(/Чат|\//u);
      view.unmount();
    }
  });

  it('opens the menu of the saga under its name and «Заметка» as a new note', () => {
    windowOf(1440);
    const { onOpenMenu } = renderShell();
    const saga = screen.getByRole('button', { name: 'Сага: Хроники Белого порта' });
    expect(saga.getAttribute('aria-haspopup')).toBe('menu');
    fireEvent.click(saga);
    expect(onOpenMenu).toHaveBeenCalledWith(saga);
    fireEvent.click(screen.getByRole('button', { name: 'Заметка' }));
    expect(notes.capture).toHaveBeenCalledWith({ body: '' });
  });

  it('marks the saga-example «Пример» by its name, outside the name of the button', () => {
    windowOf(1440);
    renderShell({ saga: { name: 'Город туманов', demo: true, menuOpen: false, onOpenMenu: vi.fn() } });
    expect(screen.getByRole('button', { name: 'Сага: Город туманов' })).toBeTruthy();
    expect(within(screen.getByRole('banner')).getByText('Пример')).toBeTruthy();
  });

  it('turns the search and «Заметка» into buttons with a word under the icon on 768–1099 px', () => {
    windowOf(1000);
    renderShell();
    const search = screen.getByRole('button', { name: 'Поиск по книгам' });
    expect(search.textContent).toBe('Поиск');
    expect(screen.getByRole('button', { name: 'Заметка' }).textContent).toBe('Заметка');
    expect(screen.getByRole('button', { name: 'Сага: Хроники Белого порта' })).toBeTruthy();
  });
});

describe('the column of the sections', () => {
  it('has three signed groups in the order of S2.1, no «Чтение» and «Помощь» at the bottom', () => {
    windowOf(1440);
    renderShell();
    const nav = screen.getByRole('navigation', { name: 'Разделы' });
    const groups = within(nav).getAllByRole('list');
    expect(groups.map((list) => list.getAttribute('aria-labelledby') && document.getElementById(list.getAttribute('aria-labelledby')!)?.textContent))
      .toEqual(['Сага', 'Работа', 'Моё']);
    expect(groups.map((list) => within(list).getAllByRole('button').map((button) => button.textContent))).toEqual([
      ['Книги', 'Карта', 'Что помнит помощник'],
      ['Чат', 'Письмо', 'Рисунки'],
      ['Заметки', 'Недавно удалённые'],
    ]);
    expect(within(nav).queryByRole('button', { name: 'Чтение' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Помощь' })).toBeTruthy();
    expect(document.querySelector('[data-shell]')?.getAttribute('data-column')).toBe('list');
  });

  it('marks the section of the page and opens the one pressed', () => {
    windowOf(1440);
    const { onOpen } = renderShell({ sections: { active: 'chat', items: sagaSections, onOpen: vi.fn() } });
    expect(screen.getByRole('button', { name: 'Чат' }).getAttribute('aria-current')).toBe('page');
    expect(screen.getByRole('button', { name: 'Книги' }).getAttribute('aria-current')).toBeNull();
    expect(onOpen).not.toHaveBeenCalled();
  });

  // specs/2026-11-10-ux6-drawings-section.md DS1.1: one «Рисунки» in place of «Рисование» and «Галерея», without «новое».
  it('gives the chat its mark while the author is elsewhere, and the drawings one item without «новое»', () => {
    windowOf(1440);
    const view = renderShell({ chat: { preparing: false, ready: 2 } });
    const chat = screen.getByRole('button', { name: 'Чат' });
    // specs/2026-11-11-ux4-after-answer.md AA2.2: the number of the answers ready and not opened, in words for the screen reader.
    expect(chat.getAttribute('aria-describedby') && document.getElementById(chat.getAttribute('aria-describedby')!)?.textContent).toBe('2 ответа готовы');
    expect(chat.querySelector('[data-chat-mark="ready"]')?.textContent).toBe('2');
    view.unmount();
    renderShell({ chat: { preparing: true, ready: 0 } });
    const preparing = screen.getByRole('button', { name: 'Чат' });
    expect(document.getElementById(preparing.getAttribute('aria-describedby')!)?.textContent).toBe('ответ готовится');
    expect(preparing.querySelector('[data-chat-mark="preparing"]')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Рисунки' })).toBeTruthy();
    for (const name of [/Галерея/u, /Рисование/u, /новое/u]) expect(screen.queryByRole('button', { name })).toBeNull();
  });

  // specs/2026-11-11-ux6-drawing-flow.md DF2.2: «Рисунки» has the mark of the chat's mechanism — a drawing made, a drawing ready.
  it('gives «Рисунки» its mark by the same mechanism while the author is elsewhere', () => {
    windowOf(1440);
    const view = renderShell({ drawing: { preparing: false, ready: 1 } });
    const drawings = screen.getByRole('button', { name: 'Рисунки' });
    expect(document.getElementById(drawings.getAttribute('aria-describedby')!)?.textContent).toBe('рисунок готов');
    expect(drawings.querySelector('[data-section-mark="ready"]')?.textContent).toBe('1');
    view.unmount();
    renderShell({ drawing: { preparing: true, ready: 0 } });
    const working = screen.getByRole('button', { name: 'Рисунки' });
    expect(document.getElementById(working.getAttribute('aria-describedby')!)?.textContent).toBe('рисунок рисуется');
    expect(working.querySelector('[data-section-mark="preparing"]')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Чат' }).getAttribute('aria-describedby')).toBeNull();
  });

  // specs/2026-11-11-ux9-shelf-chat.md SC3.6 (М-04): «Что помнит помощник» has the mark of the chat's mechanism while a file of
  // the materials is read and while its records wait for the check.
  it('gives «Что помнит помощник» its mark while a file is read and while its records wait', () => {
    windowOf(1440);
    const view = renderShell({ memory: { preparing: true, ready: 0 } });
    const reading = screen.getByRole('button', { name: 'Что помнит помощник' });
    expect(document.getElementById(reading.getAttribute('aria-describedby')!)?.textContent).toBe('файл материалов разбирается');
    expect(reading.querySelector('[data-section-mark="preparing"]')).toBeTruthy();
    view.unmount();
    renderShell({ memory: { preparing: false, ready: 3 } });
    const waiting = screen.getByRole('button', { name: 'Что помнит помощник' });
    expect(document.getElementById(waiting.getAttribute('aria-describedby')!)?.textContent).toBe('3 записи на проверку');
    expect(waiting.querySelector('[data-section-mark="ready"]')?.textContent).toBe('3');
  });

  it('has no mark of the chat in the chat itself', () => {
    windowOf(1440);
    renderShell({ chat: { preparing: true, ready: 1 }, sections: { active: 'chat', items: sagaSections, onOpen: vi.fn() } });
    expect(screen.getByRole('button', { name: 'Чат' }).getAttribute('aria-describedby')).toBeNull();
  });

  it('shows the sections of the edition only', () => {
    windowOf(1440);
    renderShell({ sections: { active: 'notes', items: ['manuscript', 'chat', 'notes'], onOpen: vi.fn() } });
    const nav = screen.getByRole('navigation', { name: 'Разделы' });
    expect(within(nav).getAllByRole('button').map((button) => button.textContent)).toEqual(['Книги', 'Чат', 'Заметки']);
  });

  it('stands narrow on 768–1099 px, in a low window and on a workplace: the word under the icon, the groups apart', () => {
    windowOf(1000);
    const view = renderShell();
    expect(document.querySelector('[data-shell]')?.getAttribute('data-column')).toBe('rail');
    expect(screen.getAllByRole('button').filter((button) => button.closest('nav')).map((button) => button.textContent))
      .toEqual(['Книги', 'Карта', 'Что помнит помощник', 'Чат', 'Письмо', 'Рисунки', 'Заметки', 'Недавно удалённые']);
    view.unmount();
    windowOf(1366, 650);
    const low = renderShell();
    expect(document.querySelector('[data-shell]')?.getAttribute('data-column')).toBe('rail');
    low.unmount();
    windowOf(1440);
    renderShell({ narrow: true });
    expect(document.querySelector('[data-shell]')?.getAttribute('data-column')).toBe('rail');
    // The top bar of a workplace on a wide window stays full (Decision log of the wave).
    expect(screen.getByRole('button', { name: 'Поиск по книгам' }).textContent).toBe('Поиск по книгам');
  });

  // specs/2026-11-03-narrow-windows-navigation.md N2.1: the narrow column names every section under its icon, 44 px targets.
  it('keeps the targets of the narrow column not smaller than 44 px', () => {
    windowOf(1000);
    renderShell();
    for (const item of [...document.querySelectorAll<HTMLElement>('[data-section]')]) {
      expect(getComputedStyle(item).width).toBe('72px');
      expect(Number.parseInt(getComputedStyle(item).minHeight, 10)).toBeGreaterThanOrEqual(44);
    }
  });

  it('opens «Помощь» only where the edition has the window to write to', () => {
    windowOf(1440);
    const onOpenContact = vi.fn();
    const view = renderShell({}, { busy: false, onOpenContact, contactUnread: 1 });
    const help = screen.getByRole('button', { name: 'Помощь' });
    // specs/2026-11-10-ux2-contact.md CT1.6, CT2.1: «Помощь» opens «Написать нам» and carries the number of replies.
    expect(help.textContent).toContain('1 ответ');
    expect(help.getAttribute('aria-describedby') && document.getElementById(help.getAttribute('aria-describedby')!)?.textContent)
      .toBe('Написать нам · 1 новый ответ поддержки');
    fireEvent.click(help);
    expect(onOpenContact).toHaveBeenCalledOnce();
    expect(onOpenContact).toHaveBeenCalledWith();
    view.unmount();
    renderShell({}, { busy: false, onOpenContact: vi.fn() });
    const quiet = screen.getByRole('button', { name: 'Помощь' });
    expect(document.getElementById(quiet.getAttribute('aria-describedby')!)?.textContent).toBe('Написать нам');
    cleanup();
    renderShell({}, null);
    expect(screen.queryByRole('button', { name: 'Помощь' })).toBeNull();
  });
});

describe('the shell of a phone', () => {
  it('has «Меню» · the saga · «Поиск» · «Поговорить» · «Заметка», and no column', () => {
    windowOf(390, 844);
    renderShell();
    const bar = screen.getByRole('banner');
    expect(within(bar).getAllByRole('button').map((node) => node.getAttribute('aria-label') ?? node.textContent))
      .toEqual(['Открыть меню', 'Поиск по книгам', 'Поговорить', 'Заметка']);
    expect(bar.textContent).toContain('Хроники Белого порта');
    expect(screen.queryByRole('navigation', { name: 'Разделы' })).toBeNull();
    expect(within(bar).queryByRole('button', { name: 'Аккаунт' })).toBeNull();
  });

  it('says on «Меню» what the account button said: the admin working as another user, a reply of support', () => {
    windowOf(390, 844);
    const view = renderShell({}, {
      busy: false,
      actingAs: { name: 'Анна Соколова', email: 'anna@example.org', adminName: 'Илья Петров', expiresAt: '2026-09-04T10:05:00.000Z', expiresSoon: false },
    });
    const acting = screen.getByRole('button', { name: 'Открыть меню: вы работаете как Анна Соколова' });
    expect(acting.querySelector('[aria-hidden="true"] svg')).toBeTruthy();
    view.unmount();
    renderShell({}, { busy: false, onOpenContact: vi.fn(), contactUnread: 1 });
    expect(screen.getByRole('button', { name: 'Открыть меню: есть ответ поддержки' })).toBeTruthy();
  });

  it('opens one drawer: the saga and its menu, «Новая сага», the sections in groups, «Помощь», the account', () => {
    windowOf(390, 844);
    const { onOpen, onOpenMenu } = renderShell({}, { busy: false, onOpenContact: vi.fn() });
    fireEvent.click(screen.getByRole('button', { name: 'Открыть меню' }));
    const drawer = screen.getByRole('dialog', { name: 'Меню' });
    expect(within(drawer).getByRole('button', { name: 'Новая сага' })).toBeTruthy();
    expect(within(within(drawer).getByRole('navigation', { name: 'Разделы' })).getAllByRole('list')).toHaveLength(3);
    expect(within(drawer).getByRole('button', { name: 'Помощь' })).toBeTruthy();
    expect(within(drawer).getByText('Мира')).toBeTruthy();
    expect(within(drawer).getByRole('button', { name: 'Выйти' })).toBeTruthy();
    // «Связаться с нами» is «Помощь» above: one way to write.
    expect(within(drawer).queryByRole('button', { name: /Связаться с нами/u })).toBeNull();
    // The theme is not chosen here (specs/2026-11-12-ux11-dark-core.md TH1.2): the sections keep the height of the drawer.
    expect(within(drawer).queryByRole('radiogroup', { name: 'Тема' })).toBeNull();
    fireEvent.click(within(drawer).getByRole('button', { name: 'Карта' }));
    expect(onOpen).toHaveBeenCalledWith('saga', expect.any(HTMLElement));
    expect(screen.queryByRole('dialog', { name: 'Меню' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Открыть меню' }));
    fireEvent.click(within(screen.getByRole('dialog', { name: 'Меню' })).getByRole('button', { name: /^Сага: Хроники Белого порта/u }));
    expect(onOpenMenu).toHaveBeenCalledWith();
  });

  it('puts a dot on «Меню» while a ready answer or a reply of support waits inside', () => {
    windowOf(390, 844);
    renderShell({ chat: { preparing: false, ready: 1 } });
    expect(screen.getByRole('button', { name: 'Открыть меню' }).querySelector('[data-menu-mark]')).toBeTruthy();
  });
});

describe('the states of the shell', () => {
  it('without a saga has no column, no search and no «Заметка», and names the first saga', () => {
    windowOf(1440);
    renderShell({ place: 'Первая сага', saga: null, sections: null });
    const bar = screen.getByRole('banner');
    expect(bar.textContent).toContain('Первая сага');
    expect(screen.queryByRole('navigation', { name: 'Разделы' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Поиск по книгам' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Заметка' })).toBeNull();
    expect(within(bar).getByRole('button', { name: 'Поговорить' })).toBeTruthy();
  });

  it('says to a reader of the saga who changes it, in one line under the top bar', () => {
    windowOf(1440);
    renderShell({ notice: <ReaderLine /> });
    const line = screen.getByRole('note', { name: 'Вы читаете эту сагу' });
    expect(line.textContent).toBe('читательВы читаете эту сагу. Менять книги и главы может владелец.');
    expect(line.previousElementSibling?.tagName).toBe('HEADER');
  });

  // specs/2026-11-10-ux2-patterns-refusals.md PR3.4: an editor reads one line too — who deletes and sets the saga up.
  it('says the role of an editor in one line under the top bar', () => {
    renderShell({ notice: <ReaderLine role="editor" /> });
    const line = screen.getByRole('note', { name: 'Вы редактор этой саги' });
    expect(line.textContent).toBe('редакторВы редактор этой саги — удалять и настраивать её может владелец.');
  });

  it('keeps the strip of a call and the line of the address right under the top bar', () => {
    windowOf(1440);
    renderShell({}, { busy: false }, <p>Полоса</p>);
    expect(screen.getByText('Полоса').previousElementSibling?.tagName).toBe('HEADER');
  });

  it('marks its content once for the focus of the root (SH2) and has one main', () => {
    windowOf(1440);
    renderShell();
    expect(document.querySelectorAll('[data-page-content]')).toHaveLength(1);
    expect(screen.getAllByRole('main')).toHaveLength(1);
    expect(screen.getByRole('main', { name: 'Книги' }).querySelector('h1')).toBeTruthy();
  });
});

// specs/2026-11-10-ux7-reader.md RD1.6: «Только текст» of the reader — the top bar, the line under it and the sections step aside.
describe('the shell of «Только текст»', () => {
  it('steps aside, comes back at the top edge and leaves again on the text', () => {
    windowOf(1440);
    const { container } = renderShell({ bare: true, narrow: true, notice: <ReaderLine role="viewer" /> });
    const shell = container.querySelector<HTMLElement>('[data-shell]')!;
    expect(shell.hasAttribute('data-bare')).toBe(true);
    expect(screen.queryByRole('note', { name: 'Вы читаете эту сагу' })).toBeNull();
    // The top bar and the sections are still there for the keyboard: Tab brings them back by their focus.
    expect(within(shell).getByRole('navigation', { name: 'Разделы' })).toBeTruthy();
    const move = (clientX: number, clientY: number) => act(() => { shell.dispatchEvent(new MouseEvent('pointermove', { bubbles: true, clientX, clientY })); });
    // A mouse moves by pointer events of its own kind; a touch is a press.
    Object.defineProperty(MouseEvent.prototype, 'pointerType', { configurable: true, get: () => 'mouse' });
    move(600, 4);
    expect(shell.hasAttribute('data-revealed')).toBe(true);
    move(40, 300);
    expect(shell.hasAttribute('data-revealed')).toBe(true);
    move(600, 400);
    expect(shell.hasAttribute('data-revealed')).toBe(false);
    delete (MouseEvent.prototype as { pointerType?: string }).pointerType;
  });

  it('is the usual shell otherwise', () => {
    windowOf(1440);
    const { container } = renderShell({ notice: <ReaderLine role="viewer" /> });
    expect(container.querySelector('[data-shell]')!.hasAttribute('data-bare')).toBe(false);
    expect(screen.getByRole('note', { name: 'Вы читаете эту сагу' })).toBeTruthy();
  });
});

// specs/2026-11-10-ux2-patterns-states.md PS1.2, PS4.1: the width of the content and the line back are the shell's.
describe('the content of the shell', () => {
  it('stands in one of the two widths the shell gives it, the lists by default', () => {
    windowOf(1440);
    const view = renderShell();
    const main = screen.getByRole('main');
    expect(main.querySelector('[data-content-width]')!.getAttribute('data-content-width')).toBe('wide');
    view.unmount();
    renderShell({ width: 'reading' });
    const page = screen.getByRole('main').querySelector('[data-content-width="reading"]')!;
    expect(within(page as HTMLElement).getByRole('heading', { name: 'Книги' })).toBeTruthy();
  });

  it('puts the line back over the content', () => {
    windowOf(1440);
    renderShell({ back: <nav aria-label="Возврат">← К чату</nav> });
    const page = screen.getByRole('main').querySelector('[data-content-width]')!;
    expect(page.firstElementChild!.getAttribute('aria-label')).toBe('Возврат');
  });
});
