import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AccountSessionContext, type AccountSession } from '../../shared/session/account-session';
import { ResearchChatComposer } from './ResearchChatComposer';

function renderComposer(session: AccountSession) {
  const noop = vi.fn();
  render(
    <AccountSessionContext.Provider value={session}>
      <ResearchChatComposer
        above={<p>Осталось 5 из 5 вопросов на сегодня.</p>}
        active={false} busy={false} onSubmit={noop} onTaskChange={noop}
        recovery={false} stopping={false} taskText=""
      />
    </AccountSessionContext.Provider>,
  );
}

describe('ResearchChatComposer email verification', () => {
  // R1.4.4, R5.5: the notice stands instead of the question field; nothing can be sent.
  it('replaces the question field with the notice for an unverified address', () => {
    renderComposer({ busy: false, emailVerification: { email: 'new-author@example.test', status: 'idle', canSend: true, onSend: vi.fn() } });

    // The words of the line under the top bar, with the same letter (specs/2026-11-10-ux1-shell.md SH4.3).
    expect(screen.getByText('Подтвердите почту new-author@example.test — и можно будет загружать книги, спрашивать, рисовать и писать.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Отправить письмо ещё раз' })).toBeTruthy();
    expect(screen.queryByRole('textbox')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Спросить' })).toBeNull();
    // Nothing is counted for a field that is not there.
    expect(screen.queryByText('Осталось 5 из 5 вопросов на сегодня.')).toBeNull();
  });

  it('shows the question field to a verified writer', () => {
    renderComposer({ busy: false });
    expect(screen.getByRole('textbox')).toBeTruthy();
    expect(screen.getByText('Осталось 5 из 5 вопросов на сегодня.')).toBeTruthy();
    expect(screen.queryByText(/Подтвердите почту/)).toBeNull();
  });
});
