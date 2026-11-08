/** A source cited by a chat answer: a chapter key and an optional paragraph range. */
export interface SourceCitation {
  chapterKey: string;
  from?: number;
  to?: number;
}

export type ChatSourceMode = 'fragment' | 'chapter';

/** The source open beside the chat; the URL owns it. */
export interface ChatSourceTarget extends SourceCitation {
  mode?: ChatSourceMode;
}

const chapterKey = '(?:[1-9][0-9]{0,5}|b[0-9]{1,4}c[0-9]{1,5})';
/**
 * The chapter of the author attached to a question of the chat, `a1` for attachment 1
 * (specs/2026-11-07-chat-attach-chapter.md CA4.4): the source pane shows it as it shows a chapter of the book, but it is
 * not one — `source:` links never name it.
 */
const attachmentPattern = '(?:a[1-9][0-9]{0,2})';
const paragraph = '([1-9][0-9]{0,4})';
const paragraphRange = `(?:/${paragraph}(?:[-–]${paragraph})?)?`;
const sourcePattern = new RegExp(`^(${chapterKey})${paragraphRange}$`, 'u');
const chatSourcePattern = new RegExp(`^(${chapterKey}|${attachmentPattern})${paragraphRange}$`, 'u');
const attachmentLinkPattern = new RegExp(`^([1-9][0-9]{0,2})${paragraphRange}$`, 'u');

function parseSource(value: string, pattern = sourcePattern): SourceCitation | null {
  const match = pattern.exec(value);
  if (!match) return null;
  const from = match[2] ? Number(match[2]) : undefined;
  const to = match[3] ? Number(match[3]) : from;
  if (from !== undefined && to !== undefined && to < from) return null;
  return { chapterKey: match[1]!, ...(from !== undefined ? { from, to } : {}) };
}

/** Parses `source:481`, `source:481/15` or `source:481/15-17`; older `source:s17` links are not sources. */
export function parseSourceCitation(href: string): SourceCitation | null {
  return href.startsWith('source:') ? parseSource(href.slice('source:'.length)) : null;
}

/** The key of attachment N in the source pane. */
export const attachmentKey = (number: number) => `a${number}`;

/** The number of the attachment a source names, or null for a chapter of the book. */
export function attachmentNumber(source: Pick<SourceCitation, 'chapterKey'>): number | null {
  const match = /^a([1-9][0-9]{0,2})$/u.exec(source.chapterKey);
  return match ? Number(match[1]) : null;
}

/** Parses `attachment:1`, `attachment:1/12` or `attachment:1/12-14`: a place of the chapter attached to the chat (CA4.4). */
export function parseAttachmentCitation(href: string): SourceCitation | null {
  if (!href.startsWith('attachment:')) return null;
  const citation = parseSource(href.slice('attachment:'.length), attachmentLinkPattern);
  return citation ? { ...citation, chapterKey: attachmentKey(Number(citation.chapterKey)) } : null;
}

export function parseChatSourceTarget(query: URLSearchParams): ChatSourceTarget | null {
  const value = query.get('source');
  const mode = query.get('sourceMode');
  if (!value || query.getAll('source').length !== 1 || query.getAll('sourceMode').length > 1 ||
    mode !== null && mode !== 'fragment' && mode !== 'chapter') return null;
  const source = parseSource(value, chatSourcePattern);
  if (!source || mode === 'fragment' && source.from === undefined) return null;
  return { ...source, ...(mode ? { mode } : {}) };
}

export function chatSourceQuery(target: ChatSourceTarget) {
  const range = target.from === undefined ? '' : target.to !== undefined && target.to !== target.from ? `/${target.from}-${target.to}` : `/${target.from}`;
  return { source: `${target.chapterKey}${range}`, sourceMode: target.mode };
}

export function sameChatSource(left: SourceCitation | null | undefined, right: SourceCitation | null | undefined) {
  return Boolean(left && right && left.chapterKey === right.chapterKey && left.from === right.from && left.to === right.to);
}
