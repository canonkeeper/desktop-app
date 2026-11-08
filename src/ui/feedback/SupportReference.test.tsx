import { render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { SupportMailLink, SupportReference } from './SupportReference';

afterEach(() => window.history.replaceState(null, '', '/'));

describe('SupportReference', () => {
  it('shows the code and a letter to support with the code and the page path', () => {
    window.history.replaceState(null, '', '/chat?projectId=p&chatId=c');
    render(<SupportReference code="req-1" />);

    expect(screen.getByText('req-1').closest('span')?.textContent).toBe('Код для поддержки: req-1');
    const link = screen.getByRole('link', { name: 'Написать в поддержку' });
    const href = decodeURIComponent(link.getAttribute('href') ?? '');
    expect(href).toMatch(/^mailto:support@canonkeeper\.ru\?/);
    expect(href).toContain('Canon Keeper: ошибка req-1');
    expect(href).toContain('Страница: /chat');
    expect(href).not.toContain('projectId');
  });

  it('offers only the letter when there is no code', () => {
    render(<SupportReference />);

    expect(screen.queryByText(/Код для поддержки/)).toBeNull();
    expect(decodeURIComponent(screen.getByRole('link', { name: 'Написать в поддержку' }).getAttribute('href') ?? ''))
      .toContain('Canon Keeper: вопрос');
  });

  it('gives a toast action the same letter', () => {
    render(<SupportMailLink code="req-2" />);

    expect(decodeURIComponent(screen.getByRole('link').getAttribute('href') ?? '')).toContain('Код для поддержки: req-2');
  });
});
