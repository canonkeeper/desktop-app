import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AccountSessionContext, type AccountSession } from '../../shared/session/account-session';
import { ResearchChatComposer } from './ResearchChatComposer';

function renderComposer(session: AccountSession) {
  const noop = vi.fn();
  render(
    <AccountSessionContext.Provider value={session}>
      <ResearchChatComposer
        active={false} busy={false} canDelete={false} canStop={false} deleteConfirmationOpen={false} deleting={false}
        onCancelDelete={noop} onConfirmDelete={noop} onDelete={noop} onStop={noop} onSubmit={noop} onTaskChange={noop}
        recovery={false} stopping={false} taskText=""
      />
    </AccountSessionContext.Provider>,
  );
}

describe('ResearchChatComposer email verification', () => {
  // R1.4.4, R5.5: the notice stands instead of the question field; nothing can be sent.
  it('replaces the question field with the notice for an unverified address', () => {
    renderComposer({ busy: false, emailVerification: { email: 'new-author@example.test', status: 'idle', canSend: true, onSend: vi.fn() } });

    expect(screen.getByText('Подтвердите адрес new-author@example.test')).toBeTruthy();
    expect(screen.getByText('Мы отправили на него письмо со ссылкой. Пока адрес не подтверждён, задавать вопросы о саге нельзя.')).toBeTruthy();
    expect(screen.queryByRole('textbox')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Спросить' })).toBeNull();
  });

  it('shows the question field to a verified writer', () => {
    renderComposer({ busy: false });
    expect(screen.getByRole('textbox')).toBeTruthy();
    expect(screen.queryByText(/Подтвердите адрес/)).toBeNull();
  });
});
