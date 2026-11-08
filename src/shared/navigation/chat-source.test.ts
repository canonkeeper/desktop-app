import { describe, expect, it } from 'vitest';
import { attachmentNumber, chatSourceQuery, parseAttachmentCitation, parseChatSourceTarget, parseSourceCitation, sameChatSource } from './chat-source';

describe('source citations', () => {
  it('reads chapter keys with an optional ordered paragraph range', () => {
    expect(parseSourceCitation('source:481/15-17')).toEqual({ chapterKey: '481', from: 15, to: 17 });
    expect(parseSourceCitation('source:481/15–17')).toEqual({ chapterKey: '481', from: 15, to: 17 });
    expect(parseSourceCitation('source:481/15')).toEqual({ chapterKey: '481', from: 15, to: 15 });
    expect(parseSourceCitation('source:b3c51')).toEqual({ chapterKey: 'b3c51' });
    for (const href of [
      'source:s17/p9-p10', 'source:s17', 'source:481/17-15', 'source:0', 'source:481/0-2', 'source:481/1-2?admin=1',
      'source:481/123456', 'source:b3', 'https://other.test/481', 'javascript:alert(1)',
    ]) {
      expect(parseSourceCitation(href)).toBeNull();
    }
  });

  it('keeps the open source in the URL and requires a range for a fragment', () => {
    const target = { chapterKey: '481', from: 15, to: 17 };
    const query = new URLSearchParams({ source: chatSourceQuery(target).source });
    expect(query.get('source')).toBe('481/15-17');
    expect(parseChatSourceTarget(query)).toEqual(target);
    query.set('sourceMode', 'chapter');
    expect(parseChatSourceTarget(query)).toEqual({ ...target, mode: 'chapter' });
    const whole = new URLSearchParams({ source: '481', sourceMode: 'fragment' });
    expect(parseChatSourceTarget(whole)).toBeNull();
    query.append('source', '12');
    expect(parseChatSourceTarget(query)).toBeNull();
    expect(sameChatSource(target, { chapterKey: '481', from: 15, to: 17 })).toBe(true);
    expect(sameChatSource(target, { chapterKey: '481' })).toBe(false);
  });
});

// specs/2026-11-07-chat-attach-chapter.md CA4.4: a place of the chapter attached to the chat is its own link; the address
// of the source pane names it `aN`, and `source:` never does.
describe('a place of an attached chapter', () => {
  it('parses attachment links into the key of the pane and keeps them out of source links', () => {
    expect(parseAttachmentCitation('attachment:1')).toEqual({ chapterKey: 'a1' });
    expect(parseAttachmentCitation('attachment:2/12')).toEqual({ chapterKey: 'a2', from: 12, to: 12 });
    expect(parseAttachmentCitation('attachment:1/12-14')).toEqual({ chapterKey: 'a1', from: 12, to: 14 });
    expect(parseAttachmentCitation('attachment:0/1')).toBeNull();
    expect(parseAttachmentCitation('attachment:1/14-12')).toBeNull();
    expect(parseSourceCitation('source:a1/2')).toBeNull();
    expect(parseChatSourceTarget(new URLSearchParams('source=a1/3-4&sourceMode=fragment'))).toEqual({ chapterKey: 'a1', from: 3, to: 4, mode: 'fragment' });
    expect(attachmentNumber({ chapterKey: 'a12' })).toBe(12);
    expect(attachmentNumber({ chapterKey: '12' })).toBeNull();
  });
});
