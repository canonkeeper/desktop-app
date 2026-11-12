import { fireEvent, render, screen } from '@testing-library/react';
import { useState, type ComponentProps } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { reasonOf, shownReason } from '../../test/disabled-reason';
import { ResearchChatComposer } from './ResearchChatComposer';

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

type ComposerProps = ComponentProps<typeof ResearchChatComposer>;

function Composer(props: Partial<ComposerProps>) {
  const [text, setText] = useState(props.taskText ?? '');
  return (
    <ResearchChatComposer active={false} busy={false} onSubmit={vi.fn()} recovery={false} stopping={false}
      {...props} onTaskChange={setText} taskText={text} />
  );
}

describe('the question field of the chat', () => {
  // specs/2026-11-10-ux2-patterns-refusals.md PR1.3: an empty field does not hide or silence «Спросить» — it says why.
  it('keeps «Спросить» in place with its reason while the field is empty, and sends nothing', () => {
    const submit = vi.fn((event: { preventDefault: () => void }) => event.preventDefault());
    render(<Composer onSubmit={submit} />);
    const ask = screen.getByRole('button', { name: 'Спросить' });
    expect(ask.getAttribute('aria-disabled')).toBe('true');
    expect(reasonOf(ask)).toBe('Сначала напишите вопрос.');
    fireEvent.click(ask);
    expect(shownReason(ask)).toBe('Сначала напишите вопрос.');
    expect(submit).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(screen.getByRole('textbox', { name: 'Вопрос' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Вопрос' }), { target: { value: 'Кто первым заметил дым?' } });
    expect(ask.hasAttribute('aria-disabled')).toBe(false);
  });

  // specs/2026-11-10-ux1-chat.md CH2.1: deleting a chat is not an action of the field.
  it('has no «Удалить чат» beside it', () => {
    render(<Composer taskText="Кто первым заметил дым?" />);
    expect(screen.getByRole('button', { name: 'Спросить' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Удалить чат' })).toBeNull();
  });

  // specs/2026-11-11-ux4-after-answer.md AA2.5 (Ч-72): on a touch screen Enter breaks the line, only the button sends.
  it('breaks the line on Enter of a touch screen and sends on Enter of a keyboard', () => {
    const submit = vi.fn((event: { preventDefault: () => void }) => event.preventDefault());
    const touch = { value: true };
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: query === '(pointer: coarse)' ? touch.value : false, addEventListener: vi.fn(), removeEventListener: vi.fn(),
    }));
    const view = render(<Composer onSubmit={submit} taskText="Первая строка" />);
    const typed = fireEvent.keyDown(screen.getByRole('textbox', { name: 'Вопрос' }), { key: 'Enter' });
    expect(typed).toBe(true);
    expect(submit).not.toHaveBeenCalled();
    view.unmount();
    touch.value = false;
    render(<Composer onSubmit={submit} taskText="Первая строка" />);
    fireEvent.keyDown(screen.getByRole('textbox', { name: 'Вопрос' }), { key: 'Enter' });
    expect(submit).toHaveBeenCalledTimes(1);
  });

  // AA2.8 (Ч-35): while «Спросить снова» counts down, the author may still mend the question.
  it('keeps the field open while the button counts down', () => {
    render(<Composer recovery retryAfterSeconds={73} taskText="Кто первым заметил дым?" />);
    expect((screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement).disabled).toBe(false);
    expect(screen.getByRole('button', { name: 'Спросить снова через 1:13' })).toHaveProperty('disabled', true);
  });

  // CH1.7: the field is as high as its text; the page caps it at eight lines and scrolls beyond.
  it('grows with the text it holds', () => {
    let content = 28;
    vi.spyOn(HTMLTextAreaElement.prototype, 'scrollHeight', 'get').mockImplementation(() => content);
    render(<Composer />);
    const field = screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
    expect(field.style.height).toBe('28px');
    content = 126;
    fireEvent.change(field, { target: { value: 'Первая строка\nвторая\nтретья\nчетвёртая\nпятая\nшестая' } });
    expect(field.style.height).toBe('126px');
    content = 28;
    fireEvent.change(field, { target: { value: '' } });
    expect(field.style.height).toBe('28px');
  });

  // CH5.1, specs/2026-11-10-ux4-chat-waiting.md CW2.5: the question waits in the queue — the field takes the text,
  // «Уточнить» stays dimmed and says why, nothing goes.
  it('keeps «Уточнить» closed while the question waits in the queue', () => {
    const submit = vi.fn((event: { preventDefault: () => void }) => event.preventDefault());
    render(<Composer active onSubmit={submit} steering steerWaits taskText="Без дракона" />);
    const field = screen.getByRole('textbox', { name: 'Уточнение' }) as HTMLTextAreaElement;
    expect(field.disabled).toBe(false);
    expect(field.placeholder).toBe('Уточнение — когда помощник начнёт…');
    // specs/2026-11-10-ux2-patterns-refusals.md PR1.1: closed, it stays and says why.
    const steer = screen.getByRole('button', { name: 'Уточнить' });
    expect(steer.getAttribute('aria-disabled')).toBe('true');
    fireEvent.click(steer);
    expect(shownReason(steer)).toBe('Уточнить можно будет, когда помощник начнёт.');
    fireEvent.keyDown(field, { key: 'Enter' });
    expect(submit).not.toHaveBeenCalled();
  });

  // CW2.4 (Ч-15): while the turn runs the field has one action — «Уточнить», a word at any width; «Остановить» is in the
  // status of the turn.
  it.each([[false], [true]])('keeps one worded action at the field of a running turn (phone: %s)', (phone) => {
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: phone && query.includes('760px'), media: query, onchange: null,
      addEventListener: vi.fn(), removeEventListener: vi.fn(), addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: vi.fn(),
    }) as unknown as MediaQueryList);
    render(<Composer active steering taskText="Без дракона" />);
    const buttons = screen.getAllByRole('button');
    expect(buttons.map((button) => button.textContent)).toEqual(['Уточнить']);
    expect(screen.queryByRole('button', { name: 'Остановить' })).toBeNull();
  });

  // Ч-30: a stopped turn — the field waits for it to stop.
  it('says the answer stops while the turn is being stopped', () => {
    render(<Composer active busy stopping />);
    const field = screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
    expect(field.placeholder).toBe('Останавливаю ответ…');
    expect(field.disabled).toBe(true);
    expect(screen.queryAllByRole('button')).toHaveLength(0);
  });

  // CH3.4: nothing is left today — the field says so and «Спросить» is no main action, but neither is locked.
  it('does not look ready to ask once the questions of the day are over', () => {
    render(<Composer spent taskText="Кто первым заметил дым?" />);
    const field = screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
    expect(field.placeholder).toBe('Вопросы на сегодня закончились');
    expect(field.disabled).toBe(false);
    const ask = screen.getByRole('button', { name: 'Спросить' });
    expect(ask).toHaveProperty('disabled', false);
    expect(ask.getAttribute('data-variant')).toBe('secondary');
  });

  it('keeps «Спросить» the main action while questions are left', () => {
    render(<Composer taskText="Кто первым заметил дым?" />);
    expect(screen.getByRole('textbox', { name: 'Вопрос' })).toHaveProperty('placeholder', 'Спросите о своей саге…');
    expect(screen.getByRole('button', { name: 'Спросить' }).getAttribute('data-variant')).toBe('primary');
  });
});

// specs/2026-11-12-desk-window.md WN3.3: while the reading waits for the helper the field takes the words of the author and
// keeps them, and «Спросить» stays in place, says the same reason as the line of the reading and sends nothing — by the
// button or by Enter.
describe('the question field without the helper', () => {
  const line = 'Помощник ещё не подключён. Читать книги и искать по ним можно уже сейчас';

  it('keeps the question and says why it does not go', () => {
    const submit = vi.fn((event: { preventDefault: () => void }) => event.preventDefault());
    render(<Composer onSubmit={submit} taskText="Кто стоял на стене заставы?" unavailable={line} />);
    const field = screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
    expect(field.disabled).toBe(false);
    const ask = screen.getByRole('button', { name: 'Спросить' });
    expect(ask.getAttribute('aria-disabled')).toBe('true');
    expect(reasonOf(ask)).toBe(line);
    fireEvent.click(ask);
    expect(shownReason(ask)).toBe(line);
    fireEvent.keyDown(field, { key: 'Enter' });
    expect(submit).not.toHaveBeenCalled();
    expect(field.value).toBe('Кто стоял на стене заставы?');
  });
});
