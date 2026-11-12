import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { forgetQuestionDraft, forgetQuestionDrafts, keepQuestionDraft, questionSlot, readQuestionDraft } from './question-draft';

const storageKey = 'canonkeeper.chat.question-drafts';

beforeEach(() => {
  window.localStorage.clear();
  forgetQuestionDrafts();
});

afterEach(() => vi.restoreAllMocks());

// specs/2026-11-10-ux1-chat.md CH1: what the author typed in the question field lives in this browser by chat.
describe('the draft of a question', () => {
  it('names a chat by its saga, a new chat by the saga alone, and both by their author', () => {
    expect(questionSlot('saga-1', 'chat-1')).toBe('saga-1:chat-1');
    expect(questionSlot('saga-1', '')).toBe('saga-1:new');
    expect(questionSlot('saga-1', 'chat-1', 'author-1')).toBe('author-1:saga-1:chat-1');
    expect(questionSlot('saga-1', '', 'author-2')).toBe('author-2:saga-1:new');
  });

  it('comes back for its chat and is not seen from another one', () => {
    keepQuestionDraft('saga-1:chat-1', 'Кто первым заметил дым?');
    keepQuestionDraft('saga-1:new', 'Где была Мара');
    forgetQuestionDrafts();
    expect(readQuestionDraft('saga-1:chat-1')).toBe('Кто первым заметил дым?');
    expect(readQuestionDraft('saga-1:new')).toBe('Где была Мара');
    expect(readQuestionDraft('saga-1:chat-2')).toBeNull();
  });

  it('is forgotten by an empty field and by the server taking the question', () => {
    keepQuestionDraft('saga-1:chat-1', 'Кто первым заметил дым?');
    keepQuestionDraft('saga-1:chat-1', '   ');
    expect(readQuestionDraft('saga-1:chat-1')).toBeNull();
    keepQuestionDraft('saga-1:chat-1', 'Снова');
    forgetQuestionDraft('saga-1:chat-1');
    forgetQuestionDrafts();
    expect(readQuestionDraft('saga-1:chat-1')).toBeNull();
    expect(window.localStorage.getItem(storageKey)).toBe('{}');
  });

  it('keeps 30 chats: the one written longest ago leaves first', () => {
    for (let index = 0; index < 30; index += 1) keepQuestionDraft(`saga-1:chat-${index}`, `вопрос ${index}`);
    keepQuestionDraft('saga-1:chat-0', 'вопрос 0, дописан');
    keepQuestionDraft('saga-1:chat-30', 'вопрос 30');
    forgetQuestionDrafts();
    expect(readQuestionDraft('saga-1:chat-0')).toBe('вопрос 0, дописан');
    expect(readQuestionDraft('saga-1:chat-1')).toBeNull();
    expect(readQuestionDraft('saga-1:chat-30')).toBe('вопрос 30');
  });

  it('reads an unreadable record as none and lives without the storage', () => {
    window.localStorage.setItem(storageKey, '{"saga-1:chat-1":7,"saga-1:chat-2":"цел"');
    expect(readQuestionDraft('saga-1:chat-1')).toBeNull();
    forgetQuestionDrafts();
    window.localStorage.setItem(storageKey, '{"saga-1:chat-1":7,"saga-1:chat-2":"цел"}');
    expect(readQuestionDraft('saga-1:chat-1')).toBeNull();
    expect(readQuestionDraft('saga-1:chat-2')).toBe('цел');

    forgetQuestionDrafts();
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('storage is off'); });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('storage is off'); });
    expect(readQuestionDraft('saga-1:chat-1')).toBeNull();
    keepQuestionDraft('saga-1:chat-1', 'Без хранилища');
    // The page remembers until it reloads.
    expect(readQuestionDraft('saga-1:chat-1')).toBe('Без хранилища');
  });
});
