import { describe, expect, it } from 'vitest';
import { forgetAuthorDrafts } from './author-drafts';

describe('what the author typed and has not sent', () => {
  it('is forgotten by the browser on a sign-out, and the settings of the device stay', () => {
    window.localStorage.setItem('canonkeeper.chat.question-drafts', '{"author:saga:new":"Кто убил лорда?"}');
    window.localStorage.setItem('canonkeeper.materials.review.import-1', '{"excluded":[2]}');
    window.localStorage.setItem('canonkeeper.feedback.draft.user-1', '{"blocks":[]}');
    window.localStorage.setItem('canonkeeper.feedback.reply.thread-1', '{"blocks":[]}');
    window.sessionStorage.setItem('canonkeeper.writing.unsaved.draft-1', '{"edits":[]}');
    // The idea of a drawing, a message to its helper and the words of an edit (specs/2026-11-10-ux1-drawing.md DX2.7).
    window.sessionStorage.setItem('canonkeeper.drawing.draft.idea.saga-1', 'Мара у северных ворот');
    window.sessionStorage.setItem('canonkeeper.drawing.draft.message.drawing-1', 'Сделай паруса белыми');
    window.localStorage.setItem('canonkeeper.answer-ready.browser', 'on');
    window.localStorage.setItem('canonkeeper.voice.autostart', '1');
    window.localStorage.setItem('other-site-key', 'kept');

    forgetAuthorDrafts();

    expect(Object.keys(window.localStorage).sort()).toEqual(['canonkeeper.answer-ready.browser', 'canonkeeper.voice.autostart', 'other-site-key']);
    expect(window.sessionStorage.length).toBe(0);
  });
});
