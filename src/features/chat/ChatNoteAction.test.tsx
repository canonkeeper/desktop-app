import { useRef } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { NotesContext } from '../../shared/notes/notes-context';
import type { CanonicalAgentTimelineEvent } from './feed/types';
import { ChatNoteAction } from './ChatNoteAction';

const event: CanonicalAgentTimelineEvent = {
  id: 'answer', cursor: 1, kind: 'assistant', role: 'main', jobId: 'job', logicalSection: 0,
  providerTurnId: 'turn', providerItemId: 'item', text: 'Первый ответ. Продолжение.',
};

function Answer() {
  const contentRef = useRef<HTMLDivElement>(null);
  return <section><div ref={contentRef}>Первый <strong>ответ.</strong> Продолжение.</div>
    <ChatNoteAction chatId="chat" event={event} contentRef={contentRef} />
  </section>;
}

function setup() {
  const capture = vi.fn();
  const { container } = render(<NotesContext.Provider value={{ capture, open: vi.fn(), openWithNavigation: vi.fn() }}>
    <Answer /><p>Другой ответ</p>
  </NotesContext.Provider>);
  return { capture, container };
}

function select(start: Node, end?: Node) {
  const range = document.createRange();
  range.selectNodeContents(start);
  if (end) range.setEnd(end, end.textContent!.length);
  Object.defineProperty(range, 'getBoundingClientRect', { value: () => new DOMRect(80, 220, 150, 25) });
  const selection = window.getSelection()!;
  selection.removeAllRanges();
  selection.addRange(range);
  fireEvent(document, new Event('selectionchange'));
}

afterEach(() => window.getSelection()?.removeAllRanges());

describe('Chat selection notes', () => {
  it('opens selection actions directly and captures only the fragment with its exact message', () => {
    const { capture, container } = setup();
    select(container.querySelector('strong')!);
    const menu = screen.getByRole('toolbar', { name: 'Действия с выделением' });
    expect(menu.textContent).not.toContain('Найти в книге');
    fireEvent.click(menu.querySelector('button')!);
    expect(capture).toHaveBeenCalledWith({ body: 'ответ.', source: {
      kind: 'chat', chatId: 'chat', providerTurnId: 'turn', providerItemId: 'item', quote: 'ответ.',
    } });
    expect(screen.queryByRole('toolbar')).toBeNull();
  });

  it('dismisses stale selection actions and rejects selection across messages', () => {
    const { capture, container } = setup();
    select(container.querySelector('strong')!);
    expect(screen.getByRole('toolbar')).toBeTruthy();
    fireEvent.scroll(window);
    expect(screen.queryByRole('toolbar')).toBeNull();
    select(container.querySelector('strong')!, container.querySelector('p')!.firstChild!);
    expect(screen.queryByRole('toolbar')).toBeNull();
    expect(capture).not.toHaveBeenCalled();
  });
});
