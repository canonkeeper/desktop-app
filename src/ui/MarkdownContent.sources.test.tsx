import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { MarkdownContent } from './MarkdownContent';
import { SourceLink } from './controls/SourceLink';

describe('Markdown source links', () => {
  it('renders a complete source link as a shared control and no other link', () => {
    const open = vi.fn();
    const { container } = render(<MarkdownContent renderSourceLink={(source, label) => <SourceLink onClick={() => open(source)}>{label}</SourceLink>}>
      {'Ответ. [гл. 12 ¶9–10](source:12/9-10) [Сайт](https://example.org) [Опасно](javascript:alert%281%29)'}
    </MarkdownContent>);
    fireEvent.click(screen.getByRole('button', { name: 'гл. 12 ¶9–10' }));
    expect(open).toHaveBeenCalledWith({ chapterKey: '12', from: 9, to: 10 });
    // Any other link of the model is text (SEC-16, MarkdownContent.links.test.tsx).
    expect(screen.queryByRole('link')).toBeNull();
    expect(container.textContent).toBe('Ответ. гл. 12 ¶9–10 Сайт (https://example.org) Опасно');
  });

  it('keeps a citation together with the punctuation that touches it', () => {
    const text = 'Мара гл. 1 ¶23, а Сава (гл. 2 ¶4). Конец гл. 3 ¶1 и дальше.';
    const { container } = render(<MarkdownContent renderSourceLink={(_source, label) => <SourceLink>{label}</SourceLink>}>
      {'Мара [гл. 1 ¶23](source:1/23), а Сава ([гл. 2 ¶4](source:2/4)). Конец [гл. 3 ¶1](source:3/1) и дальше.'}
    </MarkdownContent>);
    // A citation is a button, and a line may break on either side of a button.
    expect([...container.querySelectorAll('button')].map((button) => [button.parentElement!.tagName, button.parentElement!.textContent])).toEqual([
      ['SPAN', 'гл. 1 ¶23,'],
      ['SPAN', '(гл. 2 ¶4).'],
      ['P', text],
    ]);
    expect(container.textContent).toBe(text);
  });

  it('does not activate invalid, partial or code-block links', () => {
    const renderSource = vi.fn();
    const { rerender } = render(<MarkdownContent renderSourceLink={renderSource}>{'[Глава](source:12/2-1)'}</MarkdownContent>);
    expect(screen.getByText('Глава').getAttribute('title')).toBe('Источник недоступен');
    rerender(<MarkdownContent renderSourceLink={renderSource}>{'[Старая глава](source:s1/p1-p2)'}</MarkdownContent>);
    expect(screen.getByText('Старая глава').getAttribute('title')).toBe('Источник недоступен');
    for (const body of ['[Глава](source:12/1-', '`[Глава](source:12)`', '```text\n[Глава](source:12)\n```']) {
      rerender(<MarkdownContent renderSourceLink={renderSource}>{body}</MarkdownContent>);
    }
    expect(renderSource).not.toHaveBeenCalled();
  });
});

// specs/2026-10-29-saga-illustrations.md R1.6: `![подпись](image:K)` is a drawing the host draws.
describe('Markdown drawings', () => {
  it('draws an image link of the saga as a block and leaves any other image as its caption', () => {
    const drawn = vi.fn((image: { src: string; alt: string }) => <figure aria-label={image.alt}>{image.src}</figure>);
    const { container } = render(<MarkdownContent renderImage={drawn}>
      {'![Мирон у костра](image:42)\n\nНарисовал по тексту. ![чужая](https://example.org/x.png)'}
    </MarkdownContent>);
    expect(drawn).toHaveBeenCalledWith({ src: 'image:42', alt: 'Мирон у костра' });
    const figure = screen.getByRole('figure', { name: 'Мирон у костра' });
    expect(figure.parentElement!.tagName).toBe('DIV');
    expect(container.querySelector('img')).toBeNull();
    expect(container.textContent).toContain('Нарисовал по тексту. чужая');
  });

  it('keeps the caption as text without a drawing host', () => {
    const { container } = render(<MarkdownContent>{'![Мирон у костра](image:42)'}</MarkdownContent>);
    expect(container.textContent).toBe('Мирон у костра');
    expect(container.querySelector('p')).not.toBeNull();
  });

  // specs/2026-11-07-chat-attach-chapter.md CA4.4: a place of the chapter the author attached is a source of its own kind.
  it('renders a place of an attached chapter as a source and refuses a broken one', () => {
    const open = vi.fn();
    const { container } = render(<MarkdownContent renderSourceLink={(source, label) => <SourceLink onClick={() => open(source)}>{label}</SourceLink>}>
      {'Расходится: [прил. ¶12–14](attachment:1/12-14), сломанная [прил.](attachment:x).'}
    </MarkdownContent>);
    fireEvent.click(screen.getByRole('button', { name: 'прил. ¶12–14' }));
    expect(open).toHaveBeenCalledWith({ chapterKey: 'a1', from: 12, to: 14 });
    expect(screen.getByText('прил.').getAttribute('title')).toBe('Источник недоступен');
    expect(container.querySelectorAll('button')[0]!.parentElement!.textContent).toBe('прил. ¶12–14,');
  });
});
