import type { OperationRequest, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import { clone } from './common';

type Draft = Schema<'ChapterDraft'>;
type Version = Schema<'ChapterDraftVersion'>;

/** The versions of the text of a mock draft (specs/2026-11-06-writing-agent.md, WA7.5), the earliest first. */
const versions = new WeakMap<Draft, Version[]>();
const wordsOf = (text: string) => text.split(/\s+/u).filter((token) => token && !/^[―—–-]+$/u.test(token)).length;

/** Keeps the working text before an action that changes it, or after the writing with its plan and where its scenes stand. */
export function keepVersion(draft: Draft, reason: Version['reason'], text: string, written: { plan: number; support: Version['support'] } | null = null) {
  const kept = versions.get(draft) ?? [];
  kept.push({
    number: kept.length + 1, reason, text, words: wordsOf(text.replace(/[⟦⟧]/gu, '')),
    authorWords: wordsOf(text.replace(/⟦[^⟧]*⟧/gu, '')), createdAt: new Date().toISOString(), support: written?.support ?? [],
    ...(written ? { plan: written.plan } : {}),
  });
  versions.set(draft, kept);
}

/** The versions of a draft, the earliest first. */
export const versionsOf = (draft: Draft): readonly Version[] => versions.get(draft) ?? [];

/** listChapterDraftVersions: the versions without their text, the latest first. */
export function listVersions(draft: Draft): Schema<'ChapterDraftVersionList'> {
  return { versions: [...(versions.get(draft) ?? [])].reverse().map(({ text: _text, support: _support, pack: _pack, ...summary }) => summary) };
}

/** getChapterDraftVersion: one version whole. */
export function versionOf(draft: Draft, number: number, operationId: 'getChapterDraftVersion' | 'takeChapterDraftVersion') {
  const version = versions.get(draft)?.find((item) => item.number === number);
  if (!version) throw new MockApiError(404, 'No such version.', operationId);
  return clone(version);
}

const spanOf = (text: string, quote?: string, from?: string, to?: string) => {
  const start = text.indexOf(quote || from || '');
  const end = quote ? start + quote.length : to && start >= 0 ? text.indexOf(to, start) + to.length : -1;
  if (!(quote || (from && to)) || start < 0 || end < start + 1 || (to && text.indexOf(to, start) < 0)) {
    throw new MockApiError(400, 'The place is not in the text.', 'takeChapterDraftVersion', 'request.validation');
  }
  return [start, end] as const;
};

/** takeChapterDraftVersion: the working text with the version whole, or with a piece of it in place of a place, after it, or at the end. */
export function takenText(working: string, version: Version, body: OperationRequest<'takeChapterDraftVersion'>) {
  if (body.whole) return version.text;
  const [start, end] = spanOf(version.text, undefined, body.from, body.to);
  const piece = version.text.slice(start, end);
  if (!body.place) return working.trim() ? `${working.trimEnd()}\n\n${piece}` : piece;
  const [placeStart, placeEnd] = spanOf(working, body.place.quote, body.place.from, body.place.to);
  if (!body.after) return `${working.slice(0, placeStart)}${piece}${working.slice(placeEnd)}`;
  const next = working.indexOf('\n', placeEnd);
  const at = next < 0 ? working.length : next;
  return `${working.slice(0, at)}\n\n${piece}${working.slice(at)}`;
}
