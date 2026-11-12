import { act, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { usePageFocus } from './page-focus';

/**
 * The content of the one shell stays from page to page (specs/2026-11-10-ux2-shell-core.md SC1.9): the page left by the
 * change may stay hidden in it while the new one loads, as React keeps it behind a loading one.
 */
function InShellContent({ object, path, next }: { object?: string | null; path: string; next: boolean }) {
  usePageFocus(path, object);
  return (
    <main aria-label="Содержимое" data-page-content="" tabIndex={-1}>
      <div style={{ display: 'none' }}><h1 data-page-heading="" tabIndex={-1}>Прежняя страница</h1></div>
      {next ? <h1 data-page-heading="" tabIndex={-1}>Новая страница</h1> : null}
    </main>
  );
}

const frames = () => act(() => new Promise((resolve) => setTimeout(resolve, 80)));

describe('the focus after a change of the page in the one shell', () => {
  it('waits on the content while the new page loads and goes on to its heading, never to the hidden one', async () => {
    const view = render(<InShellContent next={false} path="/saga" />);
    view.rerender(<InShellContent next={false} path="/memory" />);
    const content = screen.getByRole('main', { name: 'Содержимое' });
    await waitFor(() => expect(document.activeElement).toBe(content));
    view.rerender(<InShellContent next path="/memory" />);
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('heading', { name: 'Новая страница' })));
  });

  it('leaves the focus the author moved to another section while the new page loads', async () => {
    const view = render(<><button type="button">Чат</button><InShellContent next={false} path="/saga" /></>);
    view.rerender(<><button type="button">Чат</button><InShellContent next={false} path="/memory" /></>);
    const content = screen.getByRole('main', { name: 'Содержимое' });
    await waitFor(() => expect(document.activeElement).toBe(content));
    const section = screen.getByRole('button', { name: 'Чат' });
    section.focus();
    view.rerender(<><button type="button">Чат</button><InShellContent next path="/memory" /></>);
    await frames();
    expect(document.activeElement).toBe(section);
  });

  // specs/2026-11-11-ux9-writing-a.md WA1.4 (6П-77): a chapter of «Письмо» opened from the list changes only the address
  // parameter `draft` — the focus goes to its heading as after a change of the path; a tab of the same chapter does not move it.
  it('goes to the heading when the object the page opens changes, not only its path', async () => {
    const view = render(<InShellContent next={false} object={null} path="/writing" />);
    const content = screen.getByRole('main', { name: 'Содержимое' });
    content.focus();
    view.rerender(<InShellContent next object="draft-1" path="/writing" />);
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('heading', { name: 'Новая страница' })));
    const elsewhere = document.createElement('button');
    document.body.append(elsewhere);
    elsewhere.focus();
    view.rerender(<InShellContent next object="draft-1" path="/writing" />);
    await frames();
    expect(document.activeElement).toBe(elsewhere);
    elsewhere.remove();
  });

  it('leaves the focus the author moved inside the new page', async () => {
    const view = render(<InShellContent next={false} path="/saga" />);
    view.rerender(<InShellContent next={false} path="/memory" />);
    const content = screen.getByRole('main', { name: 'Содержимое' });
    await waitFor(() => expect(document.activeElement).toBe(content));
    const field = document.createElement('input');
    content.append(field);
    field.focus();
    view.rerender(<InShellContent next path="/memory" />);
    await frames();
    expect(document.activeElement).toBe(field);
  });
});
