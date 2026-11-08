import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Alert } from './Alert';
import { Badge } from './Badge';
import { Card } from './Card';
import { Chip } from './Chip';
import { EmptyState } from './EmptyState';
import { Progress } from './Progress';
import { Skeleton } from './Skeleton';

describe('feedback primitives', () => {
  it('keeps Skeleton decorative and allows caller sizing and DOM props', () => {
    const view = render(<Skeleton className="loading-row" data-placeholder="row" />);
    const skeleton = view.container.firstElementChild as HTMLElement;
    const style = getComputedStyle(skeleton);

    expect(skeleton.getAttribute('aria-hidden')).toBe('true');
    expect(skeleton.getAttribute('role')).toBeNull();
    expect(skeleton.classList.contains('loading-row')).toBe(true);
    expect(skeleton.dataset.placeholder).toBe('row');
    expect(style.width).toBe('240px');
    expect(style.height).toBe('16px');
    expect(style.background).toBe('var(--ck-secondary)');
    expect(style.borderRadius).toBe('var(--ck-radius-sm)');

    view.rerender(<Skeleton style={{ width: '100%', height: 32 }} />);
    expect(getComputedStyle(skeleton).width).toBe('100%');
    expect(getComputedStyle(skeleton).height).toBe('32px');
  });

  it('renders Badge as compact text without adding an announcement role', () => {
    render(<Badge tone="success">Готово</Badge>);
    expect(screen.getByText('Готово').getAttribute('role')).toBeNull();
  });

  it('announces Alert content', () => {
    render(<Alert tone="error">Сервис недоступен</Alert>);
    expect(screen.getByRole('alert').textContent).toBe('Сервис недоступен');
  });

  it('keeps Progress honestly indeterminate until value appears', () => {
    const view = render(<Progress aria-label="Обработка книги" />);
    const progress = screen.getByRole('progressbar', { name: 'Обработка книги' });
    expect(progress.hasAttribute('value')).toBe(false);
    expect(progress.textContent).not.toContain('0%');

    view.rerender(<Progress aria-label="Обработка книги" value={0.5} />);
    expect(progress.getAttribute('value')).toBe('0.5');
    expect(getComputedStyle(progress).background).toBe('var(--ck-accent)');
    const webkitTrack = [...document.styleSheets]
      .flatMap((sheet) => [...sheet.cssRules])
      .find((rule): rule is CSSStyleRule => rule instanceof CSSStyleRule
        && rule.selectorText === `.${progress.className}::-webkit-progress-bar`);
    expect(webkitTrack?.style.getPropertyValue('background')).toBe('var(--ck-accent)');
  });

  it('composes Card, Chip and EmptyState without hiding their text or actions', () => {
    render(
      <>
        <Card actions={<button type="button">Сохранить</button>} description="Краткое описание" title="Карточка">
          Содержимое
        </Card>
        <Chip tone="citation">Кн. II · гл. 12</Chip>
        <EmptyState action={<button type="button">Новый чат</button>} description="Создайте первый диалог" title="Пока нет чатов" />
      </>,
    );

    expect(screen.getByText('Карточка')).toBeTruthy();
    expect(screen.getByText('Содержимое')).toBeTruthy();
    expect(screen.getByText('Кн. II · гл. 12').textContent).toContain('Кн. II');
    expect(screen.getByRole('button', { name: 'Сохранить' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Новый чат' })).toBeTruthy();
  });
});
