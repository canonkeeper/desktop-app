import { act, fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { markGallerySeen } from '../shared/navigation/gallery-seen';
import { ProjectSidebar } from './ProjectSidebar';
import { SagaSectionsProvider } from './saga-sections';
import { Select } from './controls/Select';

describe('ProjectSidebar', () => {
  it.each(['sidebar', 'drawer'] as const)('uses one supplied project selector in %s', (variant) => {
    const onSelect = vi.fn();
    render(<ProjectSidebar activeSection="chat" variant={variant} onOpenChat={vi.fn()}
      onOpenManuscript={vi.fn()} projectTitle="Северный архив" projectMeta="2 книги"
      projectSelector={<Select label="Проект" value="north" onValueChange={onSelect}
        options={[{ value: 'north', label: 'Северный архив' }, { value: 'port', label: 'Белый порт' }]} />} />);
    expect(screen.queryByRole('button', { name: 'Северный архив' })).toBeNull();
    const selector = screen.getByRole('button', { name: 'Проект Северный архив' });
    fireEvent.click(selector);
    expect(screen.getAllByRole('listbox')).toHaveLength(1);
    fireEvent.click(screen.getByRole('option', { name: 'Белый порт' }));
    expect(onSelect).toHaveBeenCalledWith('port');
    expect(screen.queryByRole('listbox')).toBeNull();
  });
  // specs/2026-11-07-desktop-app.md DA2.8, DA16.4: the menu has the sections of the edition, whatever a page can open.
  it('shows only the sections the edition names', () => {
    render(<SagaSectionsProvider value={['manuscript', 'chat', 'notes']}>
      <ProjectSidebar activeSection="chat" onOpenChat={vi.fn()} onOpenManuscript={vi.fn()} onOpenNotes={vi.fn()} onOpenSaga={vi.fn()}
        onOpenWriting={vi.fn()} onOpenDrawing={vi.fn()} onOpenGallery={vi.fn()} projectMeta="" projectTitle="Сага" />
    </SagaSectionsProvider>);
    const menu = screen.getByRole('navigation', { name: 'Разделы саги' });
    expect([...menu.querySelectorAll('button')].map((button) => button.getAttribute('aria-label'))).toEqual(['Мои книги', 'Чат', 'Заметки']);
  });

  it('marks Notes active and keeps its create action separate from navigation', () => {
    const onOpenNotes = vi.fn();
    const onCreateNote = vi.fn();
    render(<ProjectSidebar activeSection="notes" onOpenChat={vi.fn()} onOpenManuscript={vi.fn()}
      onOpenNotes={onOpenNotes} onCreateNote={onCreateNote} projectMeta="Все заметки" projectTitle="Сага" />);
    const notes = screen.getByRole('button', { name: 'Заметки' });
    const create = screen.getByRole('button', { name: 'Создать заметку' });
    expect(notes.getAttribute('aria-current')).toBe('page');
    expect(notes.contains(create)).toBe(false);
    fireEvent.click(create);
    expect(onCreateNote).toHaveBeenCalledOnce();
    expect(onOpenNotes).not.toHaveBeenCalled();
    fireEvent.click(notes);
    expect(onOpenNotes).toHaveBeenCalledOnce();
  });

  it.each([false, true])('opens the saga map after Chat and marks it active in compact=%s', (collapsed) => {
    const onOpenSaga = vi.fn();
    render(<ProjectSidebar activeSection="saga" collapsed={collapsed} onOpenChat={vi.fn()} onOpenManuscript={vi.fn()}
      onOpenNotes={vi.fn()} onOpenSaga={onOpenSaga} projectMeta="2 книги" projectTitle="Сага" />);
    const saga = screen.getByRole('button', { name: 'Карта саги' });
    expect(saga.getAttribute('aria-current')).toBe('page');
    const labels = screen.getAllByRole('button').map((button) => button.getAttribute('aria-label') ?? button.textContent);
    expect(labels.indexOf('Карта саги')).toBe(labels.indexOf('Чат') + 1);
    fireEvent.click(saga);
    expect(onOpenSaga).toHaveBeenCalledOnce();
  });

  // specs/2026-10-29-saga-illustrations.md R11.9: «Галерея» after «Карта саги», «новое» until it is opened.
  describe('the gallery section', () => {
    beforeEach(() => window.localStorage.clear());

    it.each([false, true])('opens the gallery after the saga map and marks it new until it is opened in compact=%s', (collapsed) => {
      const onOpenGallery = vi.fn();
      render(<ProjectSidebar activeSection="chat" collapsed={collapsed} onOpenChat={vi.fn()} onOpenManuscript={vi.fn()}
        onOpenGallery={onOpenGallery} onOpenSaga={vi.fn()} projectMeta="2 книги" projectTitle="Сага" />);
      const gallery = screen.getByRole('button', { name: 'Галерея новое' });
      expect(gallery.querySelector('svg')?.classList.contains('lucide-brush')).toBe(true);
      const labels = screen.getAllByRole('button').map((button) => button.getAttribute('aria-label') ?? button.textContent);
      expect(labels.indexOf('Галерея новое')).toBe(labels.indexOf('Карта саги') + 1);
      fireEvent.click(gallery);
      expect(onOpenGallery).toHaveBeenCalledOnce();
      act(() => markGallerySeen());
      expect(screen.getByRole('button', { name: 'Галерея' }).textContent).not.toContain('новое');
    });

    it('marks the gallery active on its page and has no item without a way to it', () => {
      const view = render(<ProjectSidebar activeSection="gallery" onOpenChat={vi.fn()} onOpenManuscript={vi.fn()}
        onOpenGallery={vi.fn()} projectMeta="" projectTitle="Сага" />);
      expect(screen.getByRole('button', { name: /^Галерея/u }).getAttribute('aria-current')).toBe('page');
      view.rerender(<ProjectSidebar activeSection="chat" onOpenChat={vi.fn()} onOpenManuscript={vi.fn()} projectMeta="" projectTitle="Сага" />);
      expect(screen.queryByRole('button', { name: /^Галерея/u })).toBeNull();
    });

    it('keeps the mark when the browser keeps nothing', () => {
      vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked'); });
      vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked'); });
      render(<ProjectSidebar activeSection="chat" onOpenChat={vi.fn()} onOpenManuscript={vi.fn()}
        onOpenGallery={vi.fn()} projectMeta="" projectTitle="Сага" />);
      act(() => markGallerySeen());
      expect(screen.getByRole('button', { name: 'Галерея новое' })).toBeTruthy();
      vi.restoreAllMocks();
    });
  });

  it('shows a touch-sized create action in the drawer and keeps it out of the rail', () => {
    const props = { activeSection: 'notes' as const, onOpenChat: vi.fn(), onOpenManuscript: vi.fn(),
      onOpenNotes: vi.fn(), onCreateNote: vi.fn(), projectTitle: 'Сага', projectMeta: '' };
    const view = render(<ProjectSidebar {...props} variant="drawer" />);
    const create = screen.getByRole('button', { name: 'Создать заметку' });
    expect(create.dataset.size).toBe('touch');
    expect(getComputedStyle(create).opacity).toBe('1');
    view.rerender(<ProjectSidebar {...props} collapsed />);
    expect(screen.queryByRole('button', { name: 'Создать заметку' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Заметки' })).toBeTruthy();
  });

  it.each([false, true])('shares reading and notes shortcuts in compact=%s', (collapsed) => {
    const onOpenReader = vi.fn();
    const onOpenNotes = vi.fn();
    render(<ProjectSidebar activeSection="reader" collapsed={collapsed} onOpenChat={vi.fn()}
      onOpenManuscript={vi.fn()} onOpenReader={onOpenReader}
      onOpenNotes={onOpenNotes} projectMeta="2 книги" projectTitle="Сага" />);
    const reader = screen.getByRole('button', { name: 'Чтение' });
    expect(reader.getAttribute('aria-current')).toBe('page');
    expect(screen.getByRole('button', { name: 'Мои книги' }).getAttribute('aria-current')).toBeNull();
    fireEvent.click(reader);
    fireEvent.click(screen.getByRole('button', { name: 'Заметки' }));
    expect(onOpenReader).toHaveBeenCalledOnce();
    expect(screen.queryByRole('button', { name: 'Редактор' })).toBeNull();
    expect(onOpenNotes).toHaveBeenCalledOnce();
  });

  // specs/2026-11-03-narrow-windows-navigation.md N2.1, N2.3 («как мне попасть на другие вкладки?»).
  it('names every section in words under its icon in the narrow column', () => {
    window.localStorage.clear();
    render(<ProjectSidebar activeSection="chat" collapsed onOpenChat={vi.fn()} onOpenManuscript={vi.fn()}
      onOpenReader={vi.fn()} onOpenSaga={vi.fn()} onOpenWriting={vi.fn()} onOpenDrawing={vi.fn()}
      onOpenGallery={vi.fn()} onOpenNotes={vi.fn()} projectMeta="" projectTitle="Сага" />);
    const names = ['Мои книги', 'Чтение', 'Чат', 'Карта саги', 'Письмо', 'Рисование', 'Галерея', 'Заметки'];
    expect(screen.getAllByRole('button').map((button) => button.textContent)).toEqual(names);
    expect(screen.getByRole('button', { name: 'Галерея новое' })).toBeTruthy();
  });

  it('сворачивается в рейл, сохраняя доступные названия и навигацию', () => {
    const onOpenChat = vi.fn();
    const onOpenManuscript = vi.fn();
    render(<ProjectSidebar activeSection="manuscript" collapsed onOpenChat={onOpenChat}
      onOpenManuscript={onOpenManuscript} projectMeta="12 книг" projectTitle="Сага" />);
    expect(getComputedStyle(screen.getByRole('complementary', { name: 'Навигация саги' })).width).toBe('88px');
    expect(screen.queryByText('Разделы')).toBeNull();
    expect(screen.queryByText('12 книг')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Чат' }));
    fireEvent.click(screen.getByRole('button', { name: 'Мои книги' }));
    expect(onOpenChat).toHaveBeenCalledOnce();
    expect(onOpenManuscript).toHaveBeenCalledOnce();
  });

  it('открывает канонические разделы проекта и отмечает текущий', () => {
    const onOpenChat = vi.fn();
    const onOpenManuscript = vi.fn();
    render(
      <ProjectSidebar
        activeSection="chat"
        onOpenChat={onOpenChat}
        onOpenManuscript={onOpenManuscript}
        projectMeta="2 книги"
        projectTitle="Белый порт"
      />,
    );

    const chatButton = screen.getByRole('button', { name: 'Чат' });
    const booksButton = screen.getByRole('button', { name: 'Мои книги' });
    const project = screen.getByRole('button', { name: 'Белый порт' });
    const sidebarStyle = getComputedStyle(screen.getByRole('complementary', { name: 'Навигация саги' }));
    const projectStyle = getComputedStyle(project);
    expect(projectStyle.fontSize).toBe('14px');
    expect(projectStyle.minHeight).toBe('40px');
    // Pencil's 14px content inset includes the existing 1px border.
    expect(projectStyle.paddingLeft).toBe('13px');
    expect(sidebarStyle.width).toBe('276px');
    expect(sidebarStyle.padding).toBe('18px');
    expect(sidebarStyle.borderRightWidth).toBe('0px');
    expect(sidebarStyle.boxShadow).toBe('inset -1px 0 0 var(--ck-border)');
    expect(sidebarStyle.gap).toBe('14px');
    expect(getComputedStyle(screen.getByText('Разделы')).marginBottom).toBe('6px');
    expect(getComputedStyle(booksButton.parentElement!).gap).toBe('6px');
    for (const button of [booksButton, chatButton]) {
      expect(getComputedStyle(button).fontSize).toBe('13px');
      expect(getComputedStyle(button).gap).toBe('8px');
      expect(button.querySelector('svg')?.getAttribute('width')).toBe('15');
    }
    expect(booksButton.querySelector('svg')?.classList.contains('lucide-library')).toBe(true);
    expect(project.querySelector('svg')?.getAttribute('width')).toBe('16');
    expect(project.querySelector('svg')?.getAttribute('stroke')).toBe('var(--ck-muted-foreground)');
    expect(projectStyle.color).toBe('var(--ck-foreground)');
    expect(project).toHaveProperty('disabled', true);
    expect(booksButton.getAttribute('aria-current')).toBeNull();
    expect(chatButton.getAttribute('aria-current')).toBe('page');
    fireEvent.click(booksButton);
    fireEvent.click(chatButton);

    expect(onOpenManuscript).toHaveBeenCalledOnce();
    expect(onOpenChat).toHaveBeenCalledOnce();
  });

  it('keeps project and navigation actions touch sized in the drawer', () => {
    const onOpenProjectMenu = vi.fn();
    render(
      <ProjectSidebar
        activeSection="manuscript"
        onOpenChat={() => undefined}
        onOpenManuscript={() => undefined}
        onOpenProjectMenu={onOpenProjectMenu}
        projectMeta="2 книги"
        projectTitle="Белый порт"
        variant="drawer"
      />,
    );

    const project = screen.getByRole('button', { name: /Белый порт/u });
    expect(getComputedStyle(project).minHeight).toBe('44px');
    expect(getComputedStyle(screen.getByRole('button', { name: 'Мои книги' })).minHeight).toBe('44px');
    expect(screen.getByRole('button', { name: 'Чат' })).toBeTruthy();
    fireEvent.click(project);
    expect(onOpenProjectMenu).toHaveBeenCalledOnce();
  });
});
