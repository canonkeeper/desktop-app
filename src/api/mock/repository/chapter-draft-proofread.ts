import type { ChapterFileInput, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';

type Draft = Schema<'ChapterDraft'>;
type Note = Schema<'ChapterDraftNote'>;
type NewNote = Omit<Note, 'id' | 'number' | 'status' | 'createdAt' | 'updatedAt'>;
type Operation = 'startChapterProofread' | 'loadChapterDraftFile' | 'takeChapterDraftOriginalLook';

/**
 * The proofreading of the mock (specs/2026-11-07-writing-proofread.md, PR2, PR3): a chapter from a file of the author —
 * a text, Markdown or a Word file the mock does not open and reads as its own chapter — and corrections the mock finds
 * by a fixed list, one step of the Job on each read of the draft.
 */

/** The limits of a chapter file, as the server holds them (PR1.6). */
export const mockChapterFileBytes = 10 * 1024 * 1024;

/** What the mock reads from a Word file: it does not open the archive. */
export const mockWordChapter = 'Кайл шол к реке, и вода была холодной.\n\nОн сказал что пора идти. Олм кивнул и и пошёл следом.';

export interface MockOriginal {
  fileName: string;
  format: 'docx' | 'txt' | 'md' | 'text';
  title: string;
  text: string;
  uploadedAt: string;
}

const originals = new WeakMap<Draft, MockOriginal>();

/** The chapter of the file: its title (a heading or «Глава N…» on the first line) and its paragraphs. */
export async function readChapterFile(input: ChapterFileInput, operationId: Operation): Promise<MockOriginal> {
  let format: MockOriginal['format'] = 'text';
  let raw = input.text ?? '';
  let fileName = '';
  if (input.file) {
    fileName = input.file.name;
    if (input.file.size > mockChapterFileBytes) throw new MockApiError(413, 'The file is over 10 MiB.', operationId, 'chapter_draft.file_too_large');
    const extension = fileName.toLowerCase().split('.').pop();
    if (extension === 'docx') {
      format = 'docx';
      raw = mockWordChapter;
    } else if (extension === 'txt' || extension === 'md' || extension === 'markdown') {
      format = extension === 'txt' ? 'txt' : 'md';
      raw = await input.file.text();
    } else {
      throw new MockApiError(422, 'A chapter is read from .docx, .txt or .md.', operationId, 'chapter_draft.file_format');
    }
  }
  const lines = raw.replace(/\r\n?/gu, '\n').split('\n').map((line) => line.trim()).filter(Boolean)
    .map((line) => (format === 'md' ? line.replace(/^#+\s*/u, '').replace(/\*\*|__|\*|_/gu, '') : line));
  let title = '';
  if (lines[0] && (/^#/u.test(raw.trimStart()) && format === 'md' || /^(глава\s+\d+|пролог|эпилог)/iu.test(lines[0]))) title = lines.shift()!;
  if (!lines.length) throw new MockApiError(422, 'The file has no text.', operationId, 'chapter_draft.no_text');
  return { fileName: fileName || `${title || 'Глава'}.txt`, format, title, text: lines.join('\n\n'), uploadedAt: new Date().toISOString() };
}

export const keepOriginal = (draft: Draft, original: MockOriginal) => {
  originals.set(draft, original);
  draft.original = {
    fileName: original.fileName, format: original.format, words: original.text.split(/\s+/u).filter(Boolean).length, uploadedAt: original.uploadedAt,
    // A Word file keeps its look (PR1.7); a text, Markdown or a chapter from the chat come without it (PR9.3).
    look: original.format === 'docx',
    // Each answer counts them anew (mockRevisions).
    revisions: 0,
  };
};

export const originalOf = (draft: Draft) => originals.get(draft);

/** The chapter attached in a chat a proofreading began from (specs/2026-11-11-ux5-proofread-mode.md PM3.3), by its draft. */
const attachments = new WeakMap<Draft, { chatId: string; number: number }>();
export const tieToAttachment = (draft: Draft, attachment: { chatId: string; number: number } | null) => {
  if (attachment) attachments.set(draft, attachment);
  else attachments.delete(draft);
};
export const attachmentOf = (draft: Draft) => attachments.get(draft);

const tokenPattern = /\s+|[\p{L}\p{N}]+|./gu;
type Kind = 'same' | 'gone' | 'new';

/** The steps turning one list into another by their longest common run: what stays, goes and comes, in order. */
function steps<T>(a: readonly T[], b: readonly T[]): Kind[] {
  const lengths = Array.from({ length: a.length + 1 }, () => new Array<number>(b.length + 1).fill(0));
  for (let i = a.length - 1; i >= 0; i -= 1) {
    for (let j = b.length - 1; j >= 0; j -= 1) lengths[i]![j] = a[i] === b[j] ? lengths[i + 1]![j + 1]! + 1 : Math.max(lengths[i + 1]![j]!, lengths[i]![j + 1]!);
  }
  const out: Kind[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length || j < b.length) {
    if (i < a.length && j < b.length && a[i] === b[j]) {
      out.push('same');
      i += 1;
      j += 1;
    } else if (i < a.length && (j === b.length || lengths[i + 1]![j]! >= lengths[i]![j + 1]!)) {
      out.push('gone');
      i += 1;
    } else {
      out.push('new');
      j += 1;
    }
  }
  return out;
}

/**
 * The revisions of Word the file with corrections would have (PM1.4), as the mock reckons them: a stretch of deleted or
 * inserted words is one revision, a word replaced is two; a paragraph put in or taken out whole is one. The server counts
 * them by the writer of the file itself.
 */
export function mockRevisions(draft: Draft) {
  const original = originals.get(draft);
  if (!original) return 0;
  const paragraphs = (text: string) => text.replace(/[⟦⟧⟨⟩⟪⟫]/gu, '').split(/\n+/u).map((line) => line.trim()).filter(Boolean);
  const before = paragraphs(original.text);
  const now = paragraphs(draft.text);
  let count = 0;
  let previous: Kind = 'same';
  const step = (kind: Kind) => {
    if (kind !== 'same' && kind !== previous) count += 1;
    previous = kind;
  };
  const ops = steps(before, now);
  let a = 0;
  let b = 0;
  for (let k = 0; k < ops.length;) {
    if (ops[k] === 'same') {
      step('same');
      a += 1;
      b += 1;
      k += 1;
      continue;
    }
    const gone: string[] = [];
    const put: string[] = [];
    for (; k < ops.length && ops[k] !== 'same'; k += 1) {
      if (ops[k] === 'gone') gone.push(before[a++]!);
      else put.push(now[b++]!);
    }
    // A paragraph taken out and one put in at the same place are one paragraph changed word by word.
    const pairs = Math.min(gone.length, put.length);
    for (let index = 0; index < pairs; index += 1) {
      for (const piece of steps(gone[index]!.match(tokenPattern) ?? [], put[index]!.match(tokenPattern) ?? [])) step(piece);
      step('same');
    }
    for (let index = pairs; index < gone.length; index += 1) step('gone');
    for (let index = pairs; index < put.length; index += 1) step('new');
  }
  return count;
}

/** The corrections the mock finds: a fixed list of fragments, each where it stands once. */
const corrections: ReadonlyArray<{ find: RegExp; replace: (match: string) => string; category: string; body: string }> = [
  { find: /(?<!\p{L})(\p{L}+) \1(?!\p{L})/u, replace: (match) => match.split(' ')[0]!, category: 'опечатка', body: 'Слово повторено дважды.' },
  { find: /Кайл шол/u, replace: () => 'Кайл шёл', category: 'опечатка', body: 'Пропущена буква: «шёл».' },
  { find: /извени/u, replace: () => 'извини', category: 'орфография', body: '«Извини» — от «извинить».' },
  { find: /в течении (\p{L}+)/u, replace: (match) => match.replace('течении', 'течение'), category: 'орфография', body: '«В течение» чего-то — с «е» на конце.' },
  { find: /сказал что/u, replace: () => 'сказал, что', category: 'пунктуация', body: 'Запятая перед «что» в сложноподчинённом предложении.' },
  { find: /думаю что/u, replace: () => 'думаю, что', category: 'пунктуация', body: 'Запятая перед «что» в сложноподчинённом предложении.' },
  { find: /они пошёл/u, replace: () => 'они пошли', category: 'грамматика', body: '«Они» — множественное число: «пошли».' },
  { find: /Олм/u, replace: () => 'Ольм', category: 'имена', body: '«Олм» — в саге «Ольм».' },
  { find: /спросил вопрос/u, replace: () => 'задал вопрос', category: 'оборот', body: 'Тавтология: «спросил вопрос». «Задал вопрос» или просто «спросил».' },
];

export function proofreadFindings(text: string): NewNote[] {
  const found: NewNote[] = [];
  for (const correction of corrections) {
    const match = correction.find.exec(text);
    if (!match) continue;
    const quote = match[0];
    if (text.indexOf(quote) !== text.lastIndexOf(quote)) continue;
    found.push({ kind: 'proofread', target: 'draft', origin: 'job', level: 'info', category: correction.category, quote, replacement: correction.replace(quote), body: correction.body, refs: [] });
  }
  return found.sort((left, right) => text.indexOf(left.quote!) - text.indexOf(right.quote!));
}

/** The categories «Принять все» takes (PR4.4). */
export const batchCategories: ReadonlySet<string> = new Set(['опечатка', 'орфография', 'пунктуация']);

/** One step of a proofreading on a read: its pieces one by one, then the corrections. */
export function advanceProofread(draft: Draft): NewNote[] | null {
  const job = draft.job;
  if (!job || job.mode !== 'proofread' || !['queued', 'running', 'canceling'].includes(job.status)) return null;
  if (job.status === 'canceling') {
    job.status = 'canceled';
    return [];
  }
  if (job.status === 'queued') {
    Object.assign(job, { status: 'running', phase: 'proofread', done: 0, total: 2 });
    return [];
  }
  if (job.done < job.total - 1) {
    job.done += 1;
    return [];
  }
  Object.assign(job, { status: 'succeeded', done: job.total });
  return proofreadFindings(draft.text);
}
