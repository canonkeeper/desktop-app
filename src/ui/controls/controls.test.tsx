import { fireEvent, render, screen } from '@testing-library/react';
import { createRef } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { Button } from './Button';
import { IconButton } from './IconButton';

describe('button controls', () => {
  it.each(['sm', 'md', 'touch'] as const)('keeps a 44px coarse target for %s text buttons', (size) => {
    render(<Button size={size}>Продолжить</Button>);
    const button = screen.getByRole('button', { name: 'Продолжить' });
    const coarseRules = [...document.styleSheets]
      .flatMap((sheet) => [...sheet.cssRules])
      .filter((rule): rule is CSSMediaRule => rule instanceof CSSMediaRule && rule.conditionText === '(pointer: coarse)')
      .flatMap((rule) => [...rule.cssRules])
      .filter((rule): rule is CSSStyleRule => rule instanceof CSSStyleRule && button.matches(rule.selectorText));
    expect(coarseRules.some((rule) => rule.style.getPropertyValue('min-height') === '44px')).toBe(true);
  });

  it('keeps adjacent text fragments in one intrinsic label beside the icon', () => {
    render(<Button><svg aria-hidden="true" />{'Глава '}{3}{': готова'}</Button>);
    const button = screen.getByRole('button', { name: 'Глава 3: готова' });
    expect(button.children).toHaveLength(2);
    expect(button.firstElementChild?.tagName.toLowerCase()).toBe('svg');
    expect(button.lastElementChild?.textContent).toBe('Глава 3: готова');
  });

  it('exposes the native Button ref for restoring trigger focus', () => {
    const triggerRef = createRef<HTMLButtonElement>();
    render(<Button ref={triggerRef}>Глобальный поиск</Button>);

    const button = screen.getByRole('button', { name: 'Глобальный поиск' });
    expect(triggerRef.current).toBe(button);
    triggerRef.current?.focus();
    expect(document.activeElement).toBe(button);
  });

  it.each(['primary', 'secondary', 'outline', 'ghost', 'danger', 'ink'] as const)('preserves %s Button sizes and blocks interaction while loading', (variant) => {
    const onClick = vi.fn();
    const view = render(<Button onClick={onClick} variant={variant}>Сохранить</Button>);
    const button = screen.getByRole('button', { name: 'Сохранить' });
    expect(getComputedStyle(button).minHeight).toBe('37px');

    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);

    view.rerender(<Button size="sm" variant={variant}>Сохранить</Button>);
    expect(getComputedStyle(button).minHeight).toBe('32px');
    view.rerender(<Button size="touch" variant={variant}>Сохранить</Button>);
    expect(getComputedStyle(button).minHeight).toBe('44px');

    view.rerender(<Button loading onClick={onClick} variant={variant}>Сохранить</Button>);
    const loadingButton = screen.getByRole('button', { name: 'Сохранить' });
    expect(loadingButton.getAttribute('aria-busy')).toBe('true');
    expect((loadingButton as HTMLButtonElement).disabled).toBe(true);
  });

  it('requires an accessible name for IconButton and forwards interaction', () => {
    const onClick = vi.fn();
    const view = render(<IconButton aria-label="Закрыть" onClick={onClick}>×</IconButton>);

    const button = screen.getByRole('button', { name: 'Закрыть' });
    button.focus();
    expect(document.activeElement).toBe(button);
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);

    view.rerender(<IconButton aria-label="Закрыть" disabled onClick={onClick}>×</IconButton>);
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('keeps native submit semantics and exposes the Pencil variants and sizes', () => {
    const onSubmit = vi.fn((event: React.FormEvent) => event.preventDefault());
    render(
      <form onSubmit={onSubmit}>
        <Button size="touch" type="submit" variant="outline">Продолжить</Button>
      </form>,
    );

    const button = screen.getByRole('button', { name: 'Продолжить' }) as HTMLButtonElement;
    expect(button.type).toBe('submit');
    expect(getComputedStyle(button).minHeight).toBe('44px');
    fireEvent.click(button);
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it.each([
    ['sm', '32px'],
    ['md', '36px'],
    ['touch', '44px'],
  ] as const)('preserves %s IconButton size with a 44px coarse minimum', (size, fineSize) => {
    render(<IconButton aria-label="Открыть меню" size={size}>…</IconButton>);

    const button = screen.getByRole('button', { name: 'Открыть меню' });
    expect(getComputedStyle(button).width).toBe(fineSize);
    expect(getComputedStyle(button).height).toBe(fineSize);
    const coarseRules = [...document.styleSheets]
      .flatMap((sheet) => [...sheet.cssRules])
      .filter((rule): rule is CSSMediaRule => rule instanceof CSSMediaRule && rule.conditionText === '(pointer: coarse)')
      .flatMap((rule) => [...rule.cssRules])
      .filter((rule): rule is CSSStyleRule => rule instanceof CSSStyleRule && button.matches(rule.selectorText));
    expect(coarseRules.some((rule) => rule.style.getPropertyValue('min-width') === '44px'
      && rule.style.getPropertyValue('min-height') === '44px')).toBe(true);
  });
});
