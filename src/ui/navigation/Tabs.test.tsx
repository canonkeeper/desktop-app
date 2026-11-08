import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Tab, Tabs } from './Tabs';

describe('Tabs', () => {
  it('связывает нативные кнопки с tablist и передаёт выбор вкладки', () => {
    const onSelect = vi.fn();

    render(
      <Tabs label="Разделы книги">
        <Tab onClick={vi.fn()} selected>
          Содержание
        </Tab>
        <Tab onClick={onSelect} selected={false}>
          Заметки
        </Tab>
      </Tabs>,
    );

    expect(screen.getByRole('tablist', { name: 'Разделы книги' })).toBeTruthy();
    expect(screen.getByRole('tab', { name: 'Содержание' }).getAttribute('aria-selected')).toBe('true');
    const notesTab = screen.getByRole('tab', { name: 'Заметки' });
    expect(notesTab.tagName).toBe('BUTTON');
    fireEvent.click(notesTab);
    expect(onSelect).toHaveBeenCalledTimes(1);
  });

  it('keeps one tab in the keyboard order and activates the next enabled tab', () => {
    const onSelect = vi.fn();
    render(
      <Tabs label="Панели">
        <Tab onClick={vi.fn()} selected>Структура</Tab>
        <Tab disabled onClick={vi.fn()} selected={false}>Недоступно</Tab>
        <Tab onClick={onSelect} selected={false}>Заметки</Tab>
      </Tabs>,
    );

    const tabs = screen.getAllByRole('tab');
    expect(tabs.map((tab) => tab.tabIndex)).toEqual([0, -1, -1]);

    tabs[0]!.focus();
    fireEvent.keyDown(tabs[0]!, { key: 'ArrowRight' });
    expect(document.activeElement).toBe(tabs[2]);
    expect(onSelect).toHaveBeenCalledTimes(1);
  });
});
