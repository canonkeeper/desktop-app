import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { MarkdownContent } from './MarkdownContent';

// SEC-16 (specs/2026-10-28-security-hardening.md, UC3): in text the model wrote only source
// citations are links; any other link is its label and address as plain text.
describe('Markdown links in text the model wrote', () => {
  it('shows a masked link as its label and real address, with nothing to follow', () => {
    const { container } = render(<MarkdownContent>
      {'Сессия истекла. [Войдите заново](https://evil.example/login), чтобы продолжить.'}
    </MarkdownContent>);

    expect(container.querySelector('a')).toBeNull();
    expect(container.textContent).toBe('Сессия истекла. Войдите заново (https://evil.example/login), чтобы продолжить.');
  });

  it('does not repeat an address the label already shows', () => {
    const { container } = render(<MarkdownContent>
      {'Сайт https://example.org, www.example.org, почта writer@example.org и [условия](/legal/terms).'}
    </MarkdownContent>);

    expect(container.querySelector('a')).toBeNull();
    expect(container.textContent).toBe('Сайт https://example.org, www.example.org, почта writer@example.org и условия (/legal/terms).');
  });

  it('drops an address the URL filter removed', () => {
    const { container } = render(<MarkdownContent>{'[Опасно](javascript:alert%281%29)'}</MarkdownContent>);

    expect(container.querySelector('a')).toBeNull();
    expect(container.textContent).toBe('Опасно');
  });

  it('keeps footnotes and other links inside the page as links', () => {
    const { container } = render(<MarkdownContent>{'Он лжёт[^1].\n\n[^1]: Глава 3.'}</MarkdownContent>);

    const hrefs = [...container.querySelectorAll('a')].map((link) => link.getAttribute('href'));
    expect(hrefs).toEqual(['#user-content-fn-1', '#user-content-fnref-1']);
    expect(container.querySelector('#user-content-fnref-1')).not.toBeNull();
    expect(container.textContent).not.toContain('(#');
  });

  it('shows an address with letters decoded and does not repeat it for a trailing slash', () => {
    const { container, rerender } = render(<MarkdownContent>{'См. https://ru.wikipedia.org/wiki/Кошка'}</MarkdownContent>);
    expect(container.textContent).toBe('См. https://ru.wikipedia.org/wiki/Кошка');

    rerender(<MarkdownContent>{'[Кошка](https://ru.wikipedia.org/wiki/Кошка_домашняя)'}</MarkdownContent>);
    expect(container.textContent).toBe('Кошка (https://ru.wikipedia.org/wiki/Кошка_домашняя)');

    rerender(<MarkdownContent>{'[https://example.org](https://example.org/) и [example.org/](https://example.org)'}</MarkdownContent>);
    expect(container.textContent).toBe('https://example.org и example.org/');
  });

  it('keeps an address encoded when decoding would hide what it is', () => {
    const { container } = render(<MarkdownContent>{'[отчёт](https://evil.example/%E2%80%AEfdp.exe) [битый](https://evil.example/%E0%A4%A)'}</MarkdownContent>);

    // micromark escapes the stray «%» itself; the broken sequence stays as it is instead of failing the answer.
    expect(container.textContent).toBe('отчёт (https://evil.example/%E2%80%AEfdp.exe) битый (https://evil.example/%E0%A4%25A)');
  });

  it('keeps the links of the product’s own documents', () => {
    render(<MarkdownContent ownLinks>{'[Согласие](/legal/consent), support@canonkeeper.ru'}</MarkdownContent>);

    expect(screen.getByRole('link', { name: 'Согласие' }).getAttribute('href')).toBe('/legal/consent');
    expect(screen.getByRole('link', { name: 'support@canonkeeper.ru' }).getAttribute('href')).toBe('mailto:support@canonkeeper.ru');
  });

  // specs/2026-11-02-image-workshop.md IW7.3: `drawing:new` and `drawing:K` of the chat lead into the drawing workshop.
  it('draws the links into the drawing workshop by its renderer and keeps only their label without it', () => {
    const text = 'Рисунки делаются в мастерской: [Открыть мастерскую рисунков](drawing:new) или [Открыть рисунок 42 в мастерской](drawing:42).';
    const seen: Array<string | number> = [];
    render(<MarkdownContent renderWorkshopLink={(target, label) => { seen.push(target); return <button type="button">{label}</button>; }}>{text}</MarkdownContent>);
    expect(seen).toEqual(['new', 42]);
    expect(screen.getByRole('button', { name: 'Открыть мастерскую рисунков' })).toBeTruthy();
    const { container } = render(<MarkdownContent>{text}</MarkdownContent>);
    expect(container.textContent).toBe('Рисунки делаются в мастерской: Открыть мастерскую рисунков или Открыть рисунок 42 в мастерской.');
    expect(container.querySelector('a')).toBeNull();
  });
});
