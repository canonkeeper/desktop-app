import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Checkbox } from './Checkbox';

describe('Checkbox', () => {
  it('uses the Pencil checkbox geometry at the shared control owner', () => {
    render(<Checkbox defaultChecked label="Запомнить меня" onChange={vi.fn()} />);

    const input = screen.getByRole('checkbox');
    const box = input.nextElementSibling as HTMLElement | null;

    expect(box).toBeTruthy();
    expect(getComputedStyle(box!).width).toBe('18px');
    expect(getComputedStyle(box!).height).toBe('18px');
    expect(getComputedStyle(box!).borderRadius).toBe('6px');
    expect(getComputedStyle(box!).borderWidth).toBe('0px');
    expect(box?.getAttribute('aria-hidden')).toBe('true');

    const check = box?.querySelector('svg');
    expect(check?.classList.contains('lucide-check')).toBe(true);
    expect(check?.getAttribute('width')).toBe('12');
    expect(check?.getAttribute('height')).toBe('12');
    expect(check?.getAttribute('stroke')).toBe('var(--ck-primary-foreground)');
    expect(getComputedStyle(check!).visibility).toBe('visible');
  });

  it('keeps the mark in sync with the native input and preserves focus and disabled behavior', () => {
    const onChange = vi.fn();
    const view = render(<Checkbox label="Запомнить меня" onChange={onChange} />);
    const input = screen.getByRole('checkbox', { name: 'Запомнить меня' }) as HTMLInputElement;
    const check = input.nextElementSibling?.querySelector('svg');

    expect(check).toBeTruthy();
    expect(getComputedStyle(check!).visibility).toBe('hidden');
    input.focus();
    expect(document.activeElement).toBe(input);

    fireEvent.click(screen.getByText('Запомнить меня'));
    expect(input.checked).toBe(true);
    expect(getComputedStyle(check!).visibility).toBe('visible');

    view.rerender(<Checkbox disabled label="Запомнить меня" onChange={onChange} />);
    expect(input.disabled).toBe(true);
    input.click();
    expect(input.checked).toBe(true);
    expect(onChange).toHaveBeenCalledOnce();
  });
});
