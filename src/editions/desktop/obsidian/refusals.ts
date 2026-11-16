// SPDX-License-Identifier: AGPL-3.0-only
import { refusalWords as quotaWords } from '../../../entities/account-quota/refusal-words';
import { isApiStatusFailure, loadFailureWords } from '../../../entities/api-errors';
import { problemCode, problemField } from './obsidian-api';
import { exportWords, refusalReason, updateWords, vaultReasonWords, words } from './words';

/**
 * Where a refusal came: a read of the vault, a write of «Загрузить», the link of a book, the choice of the folder, the export of
 * the map (specs/2026-11-16-desk-obsidian-export-web.md XW3.3).
 */
export type RefusalPlace = 'scan' | 'preview' | 'materials' | 'books' | 'link' | 'choice' | 'export';

/** The way out a refusal offers (specs/2026-11-15-desk-obsidian-web.md, Data; specs/2026-11-10-ux2-patterns-refusals.md). */
export type RefusalExit = 'retry' | 'change' | 'checkAgain' | 'rescan' | 'showAgain' | 'toBooks' | 'loadBooksAgain';

/**
 * What the page says of a refusal: its words and ways out; `reread` — the folder of the saga is read again; `map` — the map of
 * the saga is read again, and its answer leads the button of the export (XW3.1); `failure` — the block of a failure.
 */
export interface Refusal {
  text: string;
  exits: RefusalExit[];
  reread?: true;
  map?: true;
  failure?: true;
}

/** The words of a part of «Загрузить» start with a small letter after «Записи не сохранены:» (Data `too_large` (форма)). */
function sentence(text: string, small: boolean) {
  return small ? text.charAt(0).toLowerCase() + text.slice(1) : text.charAt(0).toUpperCase() + text.slice(1);
}

const megabytes = (bytes: number) => `${Math.round(bytes / (1024 * 1024)).toLocaleString('ru-RU')} МБ`;

/**
 * «выбрано слишком много…» of the three limits of a choice, by the numbers of the refusal (Data `too_large`): the records name
 * only the numbers the server gives — the saga after the load past its limit, else the records of the load.
 */
function tooLarge(error: unknown) {
  const field = (name: string) => problemField<number>(error, name) ?? 0;
  if (problemField<number>(error, 'limitRecords') !== undefined) {
    const limit = field('limitRecords');
    return words.tooManyRecords(field('sagaRecords') > limit ? field('sagaRecords') : field('records'), limit);
  }
  if (problemField<number>(error, 'limitEntries') !== undefined) return words.tooManyEntries(field('entries'), field('limitEntries'));
  if (field('notes') > (problemField<number>(error, 'limitNotes') ?? Infinity)) return words.tooManyNotes(field('notes'), field('limitNotes'));
  return words.tooHeavy(megabytes(field('bytes')), megabytes(field('limitBytes')));
}

/** The folder of the export of the saga, when the page knows it (`getObsidianVault.exportDir`). */
type Words = (error: unknown, place: RefusalPlace, bookTitle: (bookId: string) => string, exportDir?: string) => Refusal;

const failure: Words = () => ({ text: '', exits: ['retry'], failure: true });

/**
 * Every code a refusal of an operation of the bridge has (OW7.3): the codes the operations of this page name in the contract
 * and in `domain/obsidian` (the codes of «Обновить из Obsidian» alone — its second part — aside) and the common ones.
 */
export const refusalTable: Readonly<Record<string, Words>> = {
  'obsidian.not_linked': () => ({ text: '', exits: [], reread: true }),
  'obsidian.vault_missing': (error, place) => {
    const reason = problemField<'not_found' | 'not_folder' | 'unreadable'>(error, 'vaultReason') ?? 'not_found';
    // The export has no ways out of its own: the line of the folder mends the folder (XW3.2), read again to say it.
    if (place === 'export') return { text: vaultReasonWords[reason], exits: [], reread: true };
    const exits: RefusalExit[] = reason === 'not_found' ? ['checkAgain', 'change'] : reason === 'not_folder' ? ['change'] : ['retry', 'change'];
    return { text: vaultReasonWords[reason], exits };
  },
  'obsidian.busy': () => ({ text: words.busyEnd, exits: ['retry'] }),
  // Of the export — its own words: nothing written, the way out is the button of the part (XW3.2).
  'obsidian.slow': (_error, place) => (place === 'export' ? { text: exportWords.slow, exits: [] } : { text: words.slow, exits: ['retry'] }),
  // The export (XW3.1, XW3.2): the map is read again and leads the button; the folder of the export by its name when the page
  // knows it — the folder of the saga read again first, the first step of a write may have kept it.
  'obsidian.map_not_ready': () => ({ text: '', exits: [], map: true }),
  'obsidian.export_dir_unsafe': (_error, _place, _bookTitle, exportDir) => ({
    text: exportDir ? exportWords.unsafe(exportDir) : exportWords.unsafeNoName, exits: [], reread: true,
  }),
  // Only of the folder the saga keeps (a first export passes a folder it does not know, services/obsidian/export.go): without it
  // in the folder of the saga read again, a failure.
  'obsidian.export_manifest_unreadable': (error, place, bookTitle, exportDir) => (exportDir
    ? { text: exportWords.manifest(exportDir), exits: [], reread: true } : failure(error, place, bookTitle)),
  'obsidian.too_large': (error, place) => ({ text: sentence(tooLarge(error), place === 'materials' || place === 'books'), exits: [] }),
  'obsidian.folders_overlap': (error, place) => {
    const [outer = '', inner = ''] = problemField<string[]>(error, 'folders') ?? [];
    const load = place === 'materials' || place === 'books';
    return { text: sentence(words.overlap(outer, inner), load), exits: load ? ['showAgain'] : [] };
  },
  'obsidian.choice_unavailable': () => ({ text: words.unavailable, exits: [] }),
  'obsidian.queue_busy': (_error, place) => ({ text: place === 'link' ? words.waitLoad : words.queueBusy, exits: ['toBooks'] }),
  'obsidian.folder_changed': (error) => ({ text: words.folderChanged(problemField<string[]>(error, 'folders') ?? []), exits: ['showAgain'] }),
  'obsidian.folder_missing': () => ({ text: words.folderMissing, exits: ['rescan'] }),
  'obsidian.folder_linked': (error, _place, bookTitle) => ({ text: words.folderLinked(bookTitle(problemField<string>(error, 'bookId') ?? '')), exits: [] }),
  'obsidian.load_interrupted': () => ({ text: words.interrupted, exits: ['loadBooksAgain'] }),
  'request.validation': (_error, place) => place === 'preview' ? { text: words.gone, exits: ['rescan'] }
    : place === 'materials' || place === 'books' ? { text: words.validation, exits: ['showAgain'] } : failure(_error, place, () => ''),
  'idempotency.conflict': (error, place, bookTitle) => (place === 'export' ? failure(error, place, bookTitle) : { text: words.validation, exits: ['showAgain'] }),
  conflict: (error, place, bookTitle) => place === 'materials' ? { text: words.conflict, exits: ['retry'] }
    : place === 'books' ? { text: words.interrupted, exits: ['loadBooksAgain'] } : failure(error, place, bookTitle),
  not_found: (error, place, bookTitle) => place === 'link' ? { text: words.bookGone, exits: [] } : failure(error, place, bookTitle),
  forbidden: failure,
  // A write is repeated with the same key once the minute passed (Sm6 of the review): the server answers it as the first.
  'rate_limit.exceeded': (_error, place) => (place === 'materials' || place === 'books' ? { text: words.rateLoad, exits: ['retry'] } : { text: words.rate, exits: [] }),
  'request.canceled': failure,
  'server.error': (error, place, bookTitle) => place === 'books' ? { text: words.interrupted, exits: ['loadBooksAgain'] } : failure(error, place, bookTitle),
  'auth.session_required': failure,
  'csrf.invalid': failure,
};

/**
 * What the page says of a refusal of an operation of the bridge at its place: the words and ways out of Data by its code; a
 * limit of books of the saga by the words of «Книги» for the code of the limit; any other refusal and a lost connection —
 * the block of a failure with «Повторить» (a write repeats with the same key).
 */
export function refusalOf(error: unknown, place: RefusalPlace, bookTitle: (bookId: string) => string = () => '', exportDir?: string): Refusal {
  const code = problemCode(error);
  if (code?.startsWith('quota.')) {
    const said = quotaWords(code, undefined);
    if (said) return { text: sentence(said.ended, place === 'books'), exits: [] };
  }
  if (code && Object.hasOwn(refusalTable, code)) return refusalTable[code]!(error, place, bookTitle, exportDir);
  if (isApiStatusFailure(error) && error.status >= 400 && error.status < 500 && place === 'books') return { text: words.interrupted, exits: ['loadBooksAgain'] };
  return { text: '', exits: ['retry'], failure: true };
}

/** A limit of books of the saga, by the words of «Книги» for the code of the limit (OW4.7). */
export function quotaText(code: string | undefined) {
  return code?.startsWith('quota.') ? quotaWords(code, undefined)?.ended ?? null : null;
}

/** The cause of a failure in the words of the author, after «Записи не сохранены:» in a part of the result. */
export function failureCause(error: unknown) {
  const { cause } = loadFailureWords(error);
  return sentence(cause || words.failure, true);
}


/** The way out of a refusal of «Обновить из Obsidian» (specs/2026-11-15-desk-obsidian-book-web.md OU2.7). */
export type UpdateExit = 'readAgain' | 'retry' | 'link' | 'otherFolder' | 'unlink' | 'openBridge' | 'without';

/**
 * What the window of the update says of a refusal: its words and ways out; `place` — what stands in place of the line of the
 * folder (the book is no longer linked, its folder or the folder of the saga is gone); `unread` — the notes that do not read
 * now, by their reasons; `reread` — the folder of the saga is read again; `queues` — the loads of «Книги» are read again;
 * `failure` — the block of a failure.
 */
export interface UpdateRefusal {
  text: string;
  exits: UpdateExit[];
  place?: 'notLinked' | 'folderGone' | 'vaultGone';
  unread?: { cloud: number; unreadable: number };
  reread?: true;
  queues?: true;
  failure?: true;
}

/** The notes that did not read for a passing reason, by the counts of a refusal or of the report of a folder. */
export function unreadOf(skipped: ReadonlyArray<{ reason: string; count: number }> | undefined) {
  const count = (reason: string) => (skipped ?? []).filter((skip) => skip.reason === reason).reduce((sum, skip) => sum + skip.count, 0);
  const unread = { cloud: count('cloud_only'), unreadable: count('unreadable') };
  return unread.cloud + unread.unreadable > 0 ? unread : null;
}

/** The codes of «Обновить из Obsidian» and the codes of the bridge it shares, with the words and ways out of Data. */
export const updateRefusalTable: Readonly<Record<string, (error: unknown) => UpdateRefusal>> = {
  'obsidian.book_not_linked': () => ({ text: updateWords.notLinked, exits: ['link'], place: 'notLinked', reread: true }),
  'obsidian.not_linked': () => ({ text: updateWords.notLinked, exits: ['openBridge'], place: 'notLinked', reread: true }),
  'obsidian.folder_missing': () => ({ text: '', exits: ['otherFolder', 'unlink'], place: 'folderGone', reread: true }),
  'obsidian.vault_missing': () => ({ text: updateWords.vaultGone, exits: ['openBridge'], place: 'vaultGone', reread: true }),
  'obsidian.queue_busy': () => ({ text: updateWords.queueBusy, exits: [], queues: true }),
  'obsidian.folder_changed': () => ({ text: updateWords.changedSince, exits: ['readAgain'] }),
  'obsidian.nothing_to_apply': () => ({ text: updateWords.nothingToApply, exits: [] }),
  'obsidian.notes_unread': (error) => ({
    text: '', exits: ['without', 'readAgain'], unread: unreadOf(problemField<Array<{ reason: string; count: number }>>(error, 'unread')) ?? { cloud: 0, unreadable: 0 },
  }),
  'obsidian.busy': () => ({ text: words.busyEnd, exits: ['retry'] }),
  'obsidian.slow': () => ({ text: words.slow, exits: ['retry'] }),
  'rate_limit.exceeded': () => ({ text: words.rate, exits: ['retry'] }),
  not_found: () => ({ text: words.bookGone, exits: [] }),
};

/**
 * What the window of «Обновить из Obsidian» says of a refusal of its start or of the end of its Job (OU2.4, OU2.7): the words
 * and ways out of Data by the code; a book the notes of its folder do not give — the reasons of the first part said of the
 * folder; a limit — the words of «Книги» for its code; anything else — the block of a failure.
 */
export function updateRefusalOf(error: unknown, code: string | null | undefined, bookLimitBytes: number | null): UpdateRefusal {
  if (code?.startsWith('quota.')) {
    const said = quotaWords(code, undefined);
    if (said) return { text: said.ended, exits: [] };
  }
  if (code && Object.hasOwn(updateRefusalTable, code)) return updateRefusalTable[code]!(error);
  // The refusals of the import of the core (those of importBookFile): the notes of the folder do not give a book now. A request
  // the bridge itself refused (400) is no reason of the folder.
  const ofTheCore = isApiStatusFailure(error) && [413, 415, 422].includes(error.status);
  const reason = code === 'request.validation' && isApiStatusFailure(error) && !ofTheCore ? null : refusalReason(code ?? undefined, bookLimitBytes);
  if (reason || ofTheCore || code === 'empty_import_content' || code?.startsWith('import.')) {
    return { text: updateWords.broken(reason ?? words.cantOther.toLowerCase()), exits: [] };
  }
  return { text: '', exits: ['retry'], failure: true };
}
