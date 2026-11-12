import { act, fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Button } from './Button';
import { IconButton } from './IconButton';

/**
 * A button closed now (specs/2026-11-10-ux2-patterns-refusals.md PR1.1; П-5): it stays in its place, dimmed and
 * `aria-disabled`, takes the focus and the press, says the reason and the way out beside itself and does nothing else.
 */
/** The words the button is described by. */
function description(button: HTMLElement) {
  return button.getAttribute('aria-description');
}

/** The reason stands beside the button: its plate is shown. */
function shown(text: string) {
  return Boolean(screen.queryByText(text)?.closest('[data-reason]'));
}

describe('a closed button says why', () => {
  const reason = 'Сначала напишите вопрос — поле пустое.';

  it('stays in its place, takes the focus and is described by the reason', () => {
    render(<Button disabledReason={reason}>Спросить</Button>);
    const button = screen.getByRole('button', { name: 'Спросить' });
    expect(button.getAttribute('aria-disabled')).toBe('true');
    expect((button as HTMLButtonElement).disabled).toBe(false);
    expect(button.getAttribute('data-refused')).toBe('');
    expect(description(button)).toBe(reason);
    button.focus();
    expect(document.activeElement).toBe(button);
  });

  it('describes a closed button by the bar that already says why, and lights it by a press', () => {
    const onRefusedPress = vi.fn();
    render(<><p id="bar">Пробные рисунки закончились</p><Button disabledReason={{ text: 'Пробные рисунки закончились', at: 'bar' }} onRefusedPress={onRefusedPress}>Нарисовать</Button></>);
    const button = screen.getByRole('button', { name: 'Нарисовать' });
    expect(button.getAttribute('aria-describedby')).toBe('bar');
    fireEvent.click(button);
    expect(onRefusedPress).toHaveBeenCalledTimes(1);
    expect(screen.getAllByText('Пробные рисунки закончились')).toHaveLength(1);
  });

  it('says the reason in the place of messages of the page where the page has one', () => {
    const say = vi.fn();
    const onClick = vi.fn();
    render(<Button disabledReason={{ text: 'Появится после первой правки', say }} onClick={onClick}>До / После</Button>);
    const button = screen.getByRole('button', { name: 'До / После' });
    expect(description(button)).toBe('Появится после первой правки');
    fireEvent.click(button);
    expect(say).toHaveBeenCalledWith('Появится после первой правки');
    expect(onClick).not.toHaveBeenCalled();
    expect(document.querySelector('[data-reason]')).toBeNull();
  });

  it('says the reason by a press and does not run its action', () => {
    const onClick = vi.fn();
    const onRefusedPress = vi.fn();
    render(<Button disabledReason={reason} onClick={onClick} onRefusedPress={onRefusedPress}>Спросить</Button>);
    expect(shown(reason)).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Спросить' }));
    expect(shown(reason)).toBe(true);
    expect(onClick).not.toHaveBeenCalled();
    expect(onRefusedPress).toHaveBeenCalledTimes(1);
  });

  // The walk of TEST 12.11: the chat's «Спросить» with a blank of a question in the field selected the first bracket and
  // said nothing — the press moved the focus to the field, and the plate closed by the blur of the button at once.
  it('keeps the reason shown when the press itself moves the focus to where the author mends it', () => {
    function Composer() {
      return (
        <>
          <textarea aria-label="Вопрос" />
          <Button disabledReason={reason} onRefusedPress={() => screen.getByLabelText('Вопрос').focus()}>Спросить</Button>
        </>
      );
    }
    render(<Composer />);
    const button = screen.getByRole('button', { name: 'Спросить' });
    act(() => button.focus());
    fireEvent.click(button);
    expect(document.activeElement).toBe(screen.getByLabelText('Вопрос'));
    expect(shown(reason)).toBe(true);
    // A press elsewhere takes the plate away, as before.
    fireEvent.pointerDown(document.body);
    expect(shown(reason)).toBe(false);
  });

  it('does not submit its form', () => {
    const onSubmit = vi.fn((event: React.FormEvent) => event.preventDefault());
    render(<form onSubmit={onSubmit}><Button disabledReason={reason} type="submit">Спросить</Button></form>);
    fireEvent.click(screen.getByRole('button', { name: 'Спросить' }));
    expect(onSubmit).not.toHaveBeenCalled();
    expect(shown(reason)).toBe(true);
  });

  it('says the reason by the focus of the keyboard and goes with Escape and the blur', () => {
    render(<><Button disabledReason={reason}>Спросить</Button><Button>Дальше</Button></>);
    const button = screen.getByRole('button', { name: 'Спросить' });
    act(() => button.focus());
    expect(shown(reason)).toBe(true);
    fireEvent.keyDown(button, { key: 'Escape' });
    expect(shown(reason)).toBe(false);
    fireEvent.click(button);
    expect(shown(reason)).toBe(true);
    act(() => screen.getByRole('button', { name: 'Дальше' }).focus());
    expect(shown(reason)).toBe(false);
  });

  it('offers the way out beside the reason', () => {
    const onTariff = vi.fn();
    render(<Button disabledReason={{ text: 'Глава с помощником на «Бесплатно» — одна, и она уже написана.', action: { label: 'Тариф', onClick: onTariff } }}>Написать главу</Button>);
    fireEvent.click(screen.getByRole('button', { name: 'Написать главу' }));
    fireEvent.click(screen.getByRole('button', { name: 'Тариф' }));
    expect(onTariff).toHaveBeenCalledTimes(1);
  });

  it('opens again when the reason goes: the press runs the action', () => {
    const onClick = vi.fn();
    const view = render(<Button disabledReason={reason} onClick={onClick}>Спросить</Button>);
    fireEvent.click(screen.getByRole('button', { name: 'Спросить' }));
    view.rerender(<Button disabledReason={null} onClick={onClick}>Спросить</Button>);
    const button = screen.getByRole('button', { name: 'Спросить' });
    expect(button.hasAttribute('aria-disabled')).toBe(false);
    expect(screen.queryByText(reason)).toBeNull();
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('works the same on a button of one sign', () => {
    const onClick = vi.fn();
    render(<IconButton aria-label="Остановить" disabledReason="Остановить можно, когда ответ начнёт готовиться." onClick={onClick}>■</IconButton>);
    const button = screen.getByRole('button', { name: 'Остановить' });
    expect(button.getAttribute('aria-disabled')).toBe('true');
    expect(description(button)).toBe('Остановить можно, когда ответ начнёт готовиться.');
    fireEvent.click(button);
    expect(onClick).not.toHaveBeenCalled();
    expect(shown('Остановить можно, когда ответ начнёт готовиться.')).toBe(true);
  });
});
