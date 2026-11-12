import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { sectionTitles } from '../../shared/navigation/page-title';
import { Button } from '../controls/Button';
import { sectionTable } from '../saga-sections';
import { PageHead, pageWord, WorkplaceHeading } from './PageHead';

// specs/2026-11-10-ux2-patterns-states.md PS1.1: one top of a page — the word of the menu, one line, one action.
describe('PageHead', () => {
  it('names the page by the word of its menu item, a heading that takes the focus after a change of the page', () => {
    render(<PageHead action={<Button variant="primary">Новая заметка</Button>} line="Ваши мысли к книгам" surface="notes" />);
    const heading = screen.getByRole('heading', { level: 1, name: 'Заметки' });
    expect(heading.hasAttribute('data-page-heading')).toBe(true);
    expect(heading.getAttribute('tabindex')).toBe('-1');
    expect(screen.getByText('Ваши мысли к книгам').tagName).toBe('P');
    expect(screen.getByRole('button', { name: 'Новая заметка' })).toBeTruthy();
  });

  it('keeps only the heading without a line and an action', () => {
    const view = render(<PageHead surface="drawing" />);
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Рисунки');
    expect(view.container.querySelectorAll('p')).toHaveLength(0);
    expect(view.container.querySelector('[data-page-head]')!.children).toHaveLength(1);
    expect(view.container.querySelector('header')).toBeNull();
  });

  // specs/2026-11-11-ux2-polish.md PL2: a workplace names itself in the row of its panel — the same word, the same focus.
  it('gives a workplace its heading for the row of its panel, without a block of its own', () => {
    const view = render(<div><WorkplaceHeading data-chat-heading="" id="chat-page-title" surface="chat" /><button type="button">Чаты</button></div>);
    const heading = screen.getByRole('heading', { level: 1, name: 'Чат' });
    expect(heading.hasAttribute('data-page-heading')).toBe(true);
    expect(heading.hasAttribute('data-chat-heading')).toBe(true);
    expect(heading.getAttribute('tabindex')).toBe('-1');
    expect(heading.id).toBe('chat-page-title');
    expect(view.container.querySelector('[data-page-head]')).toBeNull();
  });

  // The table «раздел → слово меню → заголовок страницы»: one word for the menu, the tab and the heading.
  it('gives every section of the menu its word, and the pages without a section their name of the tab', () => {
    for (const entry of sectionTable) {
      if (!entry.path) continue;
      const surface = entry.path === '/manuscript/books' ? 'manuscript' : entry.path.slice(1);
      expect([surface, pageWord(surface as never)]).toEqual([surface, entry.label]);
    }
    expect(['tariff', 'account', 'promo', 'unknown'].map((surface) => pageWord(surface as never)))
      .toEqual([sectionTitles.tariff, sectionTitles.account, sectionTitles.promo, sectionTitles.unknown]);
    expect(pageWord('tariff')).toBe('Тариф');
    expect(pageWord('account')).toBe('Аккаунт');
  });
});
