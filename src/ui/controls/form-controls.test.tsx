import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Overlay } from '../overlays/Overlay';
import { Checkbox } from './Checkbox';
import { Dropzone } from './Dropzone';
import { Field } from './Field';
import { SearchField } from './SearchField';
import { Select } from './Select';
import { Textarea } from './Textarea';

describe('form controls', () => {
  afterEach(() => vi.restoreAllMocks());
  it('associates Field label and native validation attributes with the input', () => {
    render(<Field error="Email обязателен" hint="Используйте рабочий адрес" label="Email" required />);

    const input = screen.getByLabelText('Email');
    expect(getComputedStyle(screen.getByText('Email', { selector: 'label' })).lineHeight).toBe('normal');
    expect(input.getAttribute('aria-invalid')).toBe('true');
    expect((input as HTMLInputElement).required).toBe(true);
    const describedBy = input.getAttribute('aria-describedby')?.split(' ') ?? [];
    expect(describedBy).toHaveLength(2);
    expect(describedBy.map((id) => document.getElementById(id)?.textContent)).toEqual([
      'Используйте рабочий адрес',
      'Email обязателен',
    ]);
  });

  it('uses one roving Select index and skips disabled options', () => {
    const onValueChange = vi.fn();
    render(
      <Select
        label="Версия текста"
        name="sourceView"
        onValueChange={onValueChange}
        options={[
          { label: 'Опубликовано', value: 'published' },
          { disabled: true, label: 'Недоступно', value: 'disabled' },
          { label: 'Черновик', value: 'draft' },
        ]}
        value="published"
      />,
    );

    const trigger = screen.getByRole('button', { name: /Версия текста/u });
    expect(trigger.querySelector('.lucide-chevron-down')?.getAttribute('stroke')).toBe('var(--ck-muted-foreground)');
    const triggerStyle = getComputedStyle(trigger);
    expect(Number.parseFloat(triggerStyle.border) + Number.parseFloat(triggerStyle.paddingLeft)).toBe(14);
    expect(Number.parseFloat(triggerStyle.border) + Number.parseFloat(triggerStyle.paddingRight)).toBe(14);
    expect(getComputedStyle(trigger).fontSize).toBe('14px');
    expect(getComputedStyle(trigger).fontWeight).toBe('400');
    expect(getComputedStyle(trigger).minHeight).toBe('40px');
    const coarseMinimums = [...document.styleSheets]
      .flatMap((sheet) => [...sheet.cssRules])
      .filter((rule): rule is CSSMediaRule => rule instanceof CSSMediaRule && rule.conditionText === '(pointer: coarse), (max-width: 899px)')
      .flatMap((rule) => [...rule.cssRules])
      .filter((rule): rule is CSSStyleRule => rule instanceof CSSStyleRule && trigger.matches(rule.selectorText))
      .map((rule) => rule.style.getPropertyValue('min-height'));
    expect(coarseMinimums).toContain('44px');
    fireEvent.click(trigger);
    const options = screen.getAllByRole('option');
    expect(getComputedStyle(options[0]!).fontSize).toBe('13px');
    expect(getComputedStyle(options[0]!).fontWeight).toBe('500');
    expect(document.activeElement).toBe(options[0]!);

    fireEvent.keyDown(options[0]!, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(options[2]!);
    fireEvent.keyDown(options[2]!, { key: 'Enter' });

    expect(onValueChange).toHaveBeenCalledWith('draft');
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(document.activeElement).toBe(trigger);
    expect(document.querySelector<HTMLInputElement>('input[name="sourceView"]')?.value).toBe('published');
    fireEvent.keyDown(trigger, { key: 'ArrowUp' });
    expect(document.activeElement).toBe(screen.getByRole('option', { name: 'Черновик' }));
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowUp' });
    expect(document.activeElement).toBe(screen.getByRole('option', { name: 'Опубликовано' }));
    fireEvent.keyDown(document.activeElement!, { key: ' ' });
    expect(onValueChange).toHaveBeenLastCalledWith('published');
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it.each([
    { viewport: 320, triggerLeft: 260, expectedLeft: 132, expectedWidth: 180 },
    { viewport: 320, triggerLeft: -20, expectedLeft: 8, expectedWidth: 180 },
    { viewport: 150, triggerLeft: 100, expectedLeft: 8, expectedWidth: 134 },
  ])('bounds the Select portal at viewport $viewport and left $triggerLeft', ({ viewport, triggerLeft, expectedLeft, expectedWidth }) => {
    vi.spyOn(document.documentElement, 'clientWidth', 'get').mockReturnValue(viewport);
    vi.spyOn(document.documentElement, 'clientHeight', 'get').mockReturnValue(200);
    vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(120);
    render(<Select label="Версия" onValueChange={vi.fn()} options={[{ label: 'Черновик', value: 'draft' }]} value="draft" />);
    const trigger = screen.getByRole('button', { name: /Версия/ });
    vi.spyOn(trigger, 'getBoundingClientRect').mockReturnValue(new DOMRect(triggerLeft, 160, 50, 32));
    fireEvent.click(trigger);
    const popup = screen.getByRole('listbox');
    expect.soft(popup.parentElement).toBe(document.body);
    expect.soft(getComputedStyle(popup).position).toBe('fixed');
    expect.soft(popup.style.left).toBe(`${expectedLeft}px`);
    expect.soft(popup.style.width).toBe(`${expectedWidth}px`);
    expect.soft(Number.parseFloat(popup.style.top)).toBeLessThan(160);
    expect.soft(Number.parseFloat(popup.style.maxHeight)).toBeLessThanOrEqual(152);
  });

  it('dismisses only Select on Escape, closes on Tab and outside pointer, and preserves blocked options', () => {
    const onDismiss = vi.fn();
    const onValueChange = vi.fn();
    render(<Overlay kind="drawer" label="Проекты" onDismiss={onDismiss}>
      <Select label="Проект" name="project" onValueChange={onValueChange} options={[
        { label: 'Первый', value: 'first' },
        { label: 'Закрытый', value: 'blocked', disabled: true },
        { label: 'Последний', value: 'last' },
      ]} value="first" />
      <button type="button">Следующее поле</button>
    </Overlay>);
    const trigger = screen.getByRole('button', { name: /Проект/ });
    fireEvent.keyDown(trigger, { key: 'End' });
    expect(document.activeElement).toBe(screen.getByRole('option', { name: 'Последний' }));
    fireEvent.keyDown(document.activeElement!, { key: 'Home' });
    expect(document.activeElement).toBe(screen.getByRole('option', { name: 'Первый' }));
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    expect(onDismiss).not.toHaveBeenCalled();
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(document.activeElement).toBe(trigger);
    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole('option', { name: 'Закрытый' }));
    expect(onValueChange).not.toHaveBeenCalled();
    fireEvent.keyDown(screen.getByRole('option', { name: 'Первый' }), { key: 'Tab' });
    expect(screen.queryByRole('listbox')).toBeNull();
    fireEvent.click(trigger);
    fireEvent.pointerDown(screen.getByRole('button', { name: 'Следующее поле' }));
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it.each([{ disabled: true }, { loading: true }])('does not open a blocked Select: %j', (blocked) => {
    render(<Select {...blocked} label="Версия" onValueChange={vi.fn()} options={[{ label: 'Черновик', value: 'draft' }]} value="draft" />);
    const trigger = screen.getByRole('button', { name: /Версия/ });
    expect((trigger as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(trigger);
    fireEvent.keyDown(trigger, { key: 'ArrowDown' });
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('toggles Checkbox through its visible label', () => {
    render(<Checkbox label="Запомнить меня" />);

    const checkbox = screen.getByLabelText('Запомнить меня') as HTMLInputElement;
    fireEvent.click(checkbox);
    expect(checkbox.checked).toBe(true);
  });

  it('associates Textarea hint and error without replacing its label', () => {
    render(<Textarea error="Поле обязательно" hint="До 500 знаков" label="Инструкция" />);

    const textarea = screen.getByLabelText('Инструкция');
    expect(textarea.getAttribute('aria-invalid')).toBe('true');
    expect(textarea.getAttribute('aria-describedby')?.split(' ').map((id) => document.getElementById(id)?.textContent)).toEqual([
      'До 500 знаков',
      'Поле обязательно',
    ]);
  });

  it('clears SearchField without shadow state and exposes loading and error semantics', () => {
    const onClear = vi.fn();
    const view = render(
      <SearchField error="Поиск недоступен" label="Поиск по книгам" onChange={() => undefined} onClear={onClear} value="маяк" />,
    );

    const search = screen.getByRole('searchbox', { name: 'Поиск по книгам' });
    expect(search.getAttribute('aria-invalid')).toBe('true');
    fireEvent.click(screen.getByRole('button', { name: 'Очистить поиск' }));
    expect(onClear).toHaveBeenCalledTimes(1);

    view.rerender(
      <SearchField label="Поиск по книгам" loading onChange={() => undefined} onClear={onClear} value="маяк" />,
    );
    expect(search.getAttribute('aria-busy')).toBe('true');
    expect(screen.queryByRole('button', { name: 'Очистить поиск' })).toBeNull();
  });

  it('keeps Dropzone a native file input and reports dropped files to its owner', () => {
    const onFilesDrop = vi.fn();
    render(
      <Dropzone
        accept=".fb2,.epub,.docx"
        description="До 50 MB"
        label="Перетащите книги"
        onFilesDrop={onFilesDrop}
      />,
    );

    const input = screen.getByLabelText(/Перетащите книги/u) as HTMLInputElement;
    const dropzone = input.closest('label')!;
    const file = new File(['book'], 'book.fb2');
    expect(input.type).toBe('file');
    expect(input.accept).toBe('.fb2,.epub,.docx');

    fireEvent.dragEnter(dropzone);
    expect(dropzone.getAttribute('data-dragging')).toBe('true');
    fireEvent.drop(dropzone, { dataTransfer: { files: [file] } });
    expect(dropzone.hasAttribute('data-dragging')).toBe(false);
    expect(onFilesDrop).toHaveBeenCalledWith([file]);
  });
});
