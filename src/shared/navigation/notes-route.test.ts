import { describe, expect, it } from 'vitest';
import { createAppRoute, parseRoute, toHref } from './app-route';
import { notesNavigationQuery, notesWorkspaceRoute } from './notes-route';

describe('Notes workspace navigation', () => {
  it('keeps the current chat separate from account-wide note filters', () => {
    const chat = createAppRoute('/chat', { projectId: 'saga-a', chatId: 'chat-a' });
    const notes = parseRoute(toHref('/notes', notesNavigationQuery(chat)));
    expect(notes.query.has('projectId')).toBe(false);
    expect(notesWorkspaceRoute(notes)).toEqual(chat);
    const filtered = parseRoute(toHref('/notes', notesNavigationQuery(notes, { projectId: 'saga-b', source: 'chat' })));
    expect(notesWorkspaceRoute(filtered)).toEqual(chat);
    const reset = parseRoute(toHref('/notes', notesNavigationQuery(filtered)));
    expect(reset.query.has('source')).toBe(false);
    expect(reset.query.has('projectId')).toBe(false);
    expect(notesWorkspaceRoute(reset)).toEqual(chat);
  });

  it('preserves a legacy deep-link workspace when its note filters are cleared', () => {
    const notes = createAppRoute('/notes', { projectId: 'saga-b', bookId: 'book-b' });
    const reset = parseRoute(toHref('/notes', notesNavigationQuery(notes)));
    expect(Object.fromEntries(notesWorkspaceRoute(reset).query)).toEqual({ projectId: 'saga-b', bookId: 'book-b' });
  });

  it('replaces context after an explicit project switch without carrying the old chat', () => {
    const notes = createAppRoute('/notes', { returnTo: '/chat?projectId=saga-a&chatId=chat-a' });
    const switched = parseRoute(toHref('/notes', notesNavigationQuery(notes, { returnTo: '/manuscript/books?projectId=saga-b' })));
    expect(Object.fromEntries(notesWorkspaceRoute(switched).query)).toEqual({ projectId: 'saga-b' });
  });

  it('keeps manuscript navigation, not its open menus or capture actions', () => {
    const reader = createAppRoute('/manuscript/books', { projectId: 'saga-a', bookId: 'book-a', chapterId: 'chapter-a', mode: 'read', menu: 'open', notes: 'open' });
    const notes = parseRoute(toHref('/notes', notesNavigationQuery(reader)));
    expect(Object.fromEntries(notesWorkspaceRoute(notes).query)).toEqual({ projectId: 'saga-a', bookId: 'book-a', chapterId: 'chapter-a', mode: 'read' });
  });
});
