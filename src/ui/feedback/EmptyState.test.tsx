import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { EmptyFilter, EmptyState } from './EmptyState';

// specs/2026-11-10-ux2-patterns-states.md PS2.1, PS2.3: three parts — what will appear · why · one first step.
describe('EmptyState', () => {
  it('says what will appear here, why, and gives one button of the first step', () => {
    const view = render(<EmptyState action={<button type="button">Новая глава</button>} icon={<svg />}
      title="Здесь будут главы в работе" why="Глава в работе — то, что вы пишете с помощником, пока не добавите её в книгу." />);
    const block = view.container.firstElementChild!;
    expect([...block.children].map((child) => child.textContent)).toEqual([
      '', 'Здесь будут главы в работе', 'Глава в работе — то, что вы пишете с помощником, пока не добавите её в книгу.', 'Новая глава',
    ]);
    expect(block.querySelector('[aria-hidden="true"]')).toBeTruthy();
    expect(screen.getAllByRole('button')).toHaveLength(1);
  });

  it('says what is needed in place of a button that would do nothing', () => {
    render(<EmptyState need={<>Сначала загрузите книгу — <a href="/manuscript/books">к книгам</a></>} title="Здесь будут рисунки саги" />);
    expect(screen.queryByRole('button')).toBeNull();
    expect(screen.getByText(/Сначала загрузите книгу/u).tagName).toBe('P');
    expect(screen.getByRole('link', { name: 'к книгам' })).toBeTruthy();
  });

  it('keeps the button of the step over the line of what is needed', () => {
    render(<EmptyState action={<button type="button">Загрузить книгу</button>} need="Сначала загрузите книгу" title="Здесь будет карта" />);
    expect(screen.queryByText('Сначала загрузите книгу')).toBeNull();
  });

  it('tells nothing by a filter from an empty section and gives the filter back', () => {
    const onReset = vi.fn();
    render(<EmptyFilter onReset={onReset} />);
    expect(screen.getByText('По этому отбору ничего нет')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Сбросить отбор' }));
    expect(onReset).toHaveBeenCalledOnce();
  });
});
