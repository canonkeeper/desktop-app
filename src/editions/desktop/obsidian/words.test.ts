// SPDX-License-Identifier: AGPL-3.0-only
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { MockApiError } from '../../../api/mock';
import { hiddenIn, hiddenWords } from '../../../shared/copy/dictionary';
import { refusalOf, refusalTable, updateRefusalOf, updateRefusalTable } from './refusals';
import {
  choiceRefusalWords, exportEndLine, exportName, exportSkipWords, exportSummary, exportWords, materialChoiceWords, refusalReason, skipWords,
  updateWords, vaultReasonWords, words,
} from './words';

const root = process.cwd();
const contract = JSON.parse(readFileSync(path.join(root, 'contracts/openapi.desktop.json'), 'utf8')) as {
  paths: Record<string, Record<string, { operationId?: string }>>;
  components: { schemas: Record<string, { enum?: string[]; properties?: Record<string, { enum?: string[] }> }> };
};

/**
 * The operations of the page of the bridge (OW7.1): «Обновить из Obsidian» is its second part; the export of the map is its part
 * «Карта саги в Obsidian» (specs/2026-11-16-desk-obsidian-export-web.md XW3.3).
 */
const pageOperations = new Set([
  'getObsidianVault', 'beginObsidianVaultChoice', 'unlinkObsidianVault', 'scanObsidianVault', 'previewObsidianImport',
  'importObsidianBooks', 'importObsidianMaterials', 'linkBookToObsidianFolder', 'unlinkBookFromObsidianFolder', 'exportObsidianSaga',
]);
const codePattern = /(?<![-/\w])(?:obsidian|request|idempotency|rate_limit|server|import)\.[a-z_]+\b(?!\.)|\bempty_import_content\b/gu;

function operationText(match: (operationId: string) => boolean) {
  return Object.values(contract.paths).flatMap((methods) => Object.values(methods))
    .filter((operation) => operation.operationId && match(operation.operationId)).map((operation) => JSON.stringify(operation)).join(' ');
}

const ofPage = new Set(operationText((id) => pageOperations.has(id)).match(codePattern) ?? []);
const ofUpdateOnly = new Set([...(operationText((id) => id === 'updateBookFromObsidian').match(codePattern) ?? [])].filter((code) => !ofPage.has(code)));
// The codes of the bridge and of the export (XW3.3): both files of `domain/obsidian` declare them by `Code… = "…"`.
const domain = ['obsidian.go', 'export.go'].map((file) => readFileSync(path.join(root, 'backend/internal/domain/obsidian', file), 'utf8')).join('\n');
const ofDomain = [...domain.matchAll(/Code\w+\s*=\s*"([a-z_.]+)"/gu)].map((match) => match[1]!).filter((code) => !ofUpdateOnly.has(code));
const common = ['request.validation', 'idempotency.conflict', 'conflict', 'not_found', 'forbidden', 'rate_limit.exceeded', 'request.canceled', 'server.error'];

// specs/2026-11-15-desk-obsidian-web.md OW7.3: every code of a refusal of the operations of the page and every reason of the
// enumerations has words — read from the contract and from `domain/obsidian`.
describe('the words of the bridge with Obsidian', () => {
  it('words every code of a refusal of the operations of the page, by the contract and the domain', () => {
    const refusals = [...new Set([...ofPage, ...ofDomain, ...common])].filter((code) => !code.startsWith('import.') && code !== 'empty_import_content');
    expect(refusals.length).toBeGreaterThan(15);
    expect(refusals).toEqual(expect.arrayContaining(['obsidian.map_not_ready', 'obsidian.export_dir_unsafe', 'obsidian.export_manifest_unreadable']));
    expect(refusals.filter((code) => !Object.hasOwn(refusalTable, code))).toEqual([]);
    // The codes a folder of a book is refused with are said of the folder.
    const ofBooks = [...ofPage].filter((code) => code.startsWith('import.'));
    expect(ofBooks.length).toBeGreaterThan(0);
    expect([...ofBooks, 'empty_import_content', 'import.too_large', 'import.file_too_large', 'request.validation'].filter((code) => !refusalReason(code, 52_428_800))).toEqual([]);
    expect(refusalReason('import.file_too_large', 52_428_800)).toBe('заметки папки вместе больше 50 МБ — разделите папку');
  });

  // specs/2026-11-15-desk-obsidian-book-web.md OU2.7: every code of «Обновить из Obsidian» has the words and the way out of Data —
  // its own codes by its description in the contract, the codes of the bridge it shares, the refusals of the import of the core.
  it('words every code of a refusal of «Обновить из Obsidian», by the contract', () => {
    const own = [...ofUpdateOnly].filter((code) => !code.startsWith('import.'));
    expect(own.sort()).toEqual(['obsidian.book_not_linked', 'obsidian.notes_unread', 'obsidian.nothing_to_apply']);
    const shared = ['obsidian.folder_missing', 'obsidian.queue_busy', 'obsidian.folder_changed', 'obsidian.busy', 'obsidian.slow', 'obsidian.vault_missing', 'obsidian.not_linked', 'rate_limit.exceeded'];
    expect([...own, ...shared].filter((code) => !Object.hasOwn(updateRefusalTable, code))).toEqual([]);
    const ofTheCore = (code: string) => updateRefusalOf({ status: 422 }, code, 52_428_800).text;
    expect(ofTheCore('import.too_many_chapters')).toBe(updateWords.broken('получается больше 2000 глав — разделите папку на несколько книг'));
    expect(ofTheCore('import.unknown_reason')).toBe(updateWords.broken('книгу из этой папки собрать не удалось'));
    expect(updateRefusalOf(new Error('lost'), null, null)).toMatchObject({ failure: true, exits: ['retry'] });
  });

  it('words every reason of `ObsidianSkipReason`, `ObsidianChoiceReason` and `vaultReason`', () => {
    const { schemas } = contract.components;
    expect(Object.keys(skipWords).sort()).toEqual([...schemas.ObsidianSkipReason!.enum!].sort());
    expect(Object.keys(choiceRefusalWords).sort()).toEqual([...schemas.ObsidianChoiceReason!.enum!].sort());
    expect(Object.keys(vaultReasonWords).sort()).toEqual([...schemas.Problem!.properties!.vaultReason!.enum!].sort());
    expect(Object.keys(materialChoiceWords).sort()).toEqual([...schemas.AuthorMaterialKind!.enum!].sort());
    // XW2.6: the four reasons a file of the folder of the export did not read.
    expect(Object.keys(exportSkipWords).sort()).toEqual([...schemas.ObsidianExportSkip!.properties!.reason!.enum!].sort());
  });

  it('says the words of Data of the update of a book the board draws', () => {
    expect(updateWords.folderCounted('Книги/Книга 1', 24, 26)).toBe('Папка «Книги/Книга 1»: 24 заметки, 26 глав');
    expect(updateWords.unread(2, 1)).toBe('Сейчас не читаются 3 заметки (только в облаке — 2, не открылись — 1). Если обновить без них, их главы уйдут из книги в «Недавно удалённые» — вернуть можно 7 дней.');
    // The verb agrees with the number as everywhere (`pluralize`); the pronoun stands for the notes: one or many.
    expect(updateWords.unread(21, 0)).toBe('Сейчас не читается 21 заметка (только в облаке — 21). Если обновить без них, их главы уйдут из книги в «Недавно удалённые» — вернуть можно 7 дней.');
    expect(updateWords.unread(0, 5)).toBe('Сейчас не читаются 5 заметок (не открылись — 5). Если обновить без них, их главы уйдут из книги в «Недавно удалённые» — вернуть можно 7 дней.');
    expect(updateWords.unread(11, 0)).toContain('Сейчас не читаются 11 заметок');
    expect(updateWords.unread(1, 0)).toBe('Сейчас не читается 1 заметка (только в облаке — 1). Если обновить без неё, её главы уйдут из книги в «Недавно удалённые» — вернуть можно 7 дней.');
    expect(updateWords.done('Книга «Книга 1»', 'изменены 2 главы, добавлена 1 глава')).toBe('Книга «Книга 1» обновлена из Obsidian: изменены 2 главы, добавлена 1 глава');
    expect(updateWords.failed('Книга «Книга 1»')).toBe('Книга «Книга 1» не обновилась — повторите «Обновить из Obsidian…» в меню книги');
    expect(updateWords.failedUnknown('Книга «Книга 1»')).toBe('Книга «Книга 1» не обновилась — повторите обновление в меню книги');
    expect(updateWords.working('Книга 1')).toBe('Обновляю «Книга 1» из Obsidian…');
  });

  it('says the words of Data the board draws', () => {
    expect(words.busy(5)).toBe('Папка ещё читается — попробую снова через 5 секунд');
    expect(words.inside(2)).toBe('внутри: 2 папки со своим выбором');
    expect(words.nested('Герои', 5)).toBe('Вложенные папки «Герои»: 5');
    expect(words.folderChanged(['Книга 1', 'Застава'])).toBe('заметки в папках «Книга 1», «Застава» изменились, пока вы смотрели.');
  });
});

// specs/2026-11-16-desk-obsidian-export-web.md XW1.1, XW1.2, XW2.4–XW2.8, XW3.1, XW3.2; Data of board 44·49, 44·59–44·67.
describe('the words of the export to Obsidian', () => {
  const group = (count: number) => ({ count, names: [] });
  const exported = { created: group(124), updated: group(14), unchanged: group(602), moved: group(3), kept: group(5) };

  it('says the page in two directions and its part of the export by Data', () => {
    expect(words.pageTitle).toBe('Obsidian');
    expect(words.startText).toBe('Выберите папку хранилища Obsidian — ту, которую вы открываете в Obsidian. Из неё CanonKeeper берёт книги и записи, а пишет только в свою папку «CanonKeeper» — когда вы выгружаете карту саги.');
    expect(words.partTitle).toBe('Книги и записи из Obsidian');
    expect(exportWords.title).toBe('Карта саги в Obsidian');
    expect(exportWords.last('сегодня в 14:20', 'CanonKeeper/Хроники Белого порта')).toBe('Последняя выгрузка — сегодня в 14:20 · папка «CanonKeeper/Хроники Белого порта»');
    expect(exportWords.folder('CanonKeeper/Хроники Белого порта')).toBe('Папка выгрузки — «CanonKeeper/Хроники Белого порта»');
  });

  it('sums the result up in the order of Data, without the files the author changed, and names the files by their paths', () => {
    expect(exportSummary(exported)).toBe('Создано 124 · обновлено 14 · без изменений 602 · убрано в «_Удалено» 3');
    expect(exportSummary({ ...exported, created: group(0), moved: group(0) })).toBe('Обновлено 14 · без изменений 602');
    expect(exportSummary({ ...exported, created: group(0), updated: group(0), unchanged: group(0), moved: group(0) })).toBeNull();
    expect(exportWords.group(exportWords.created, 124)).toBe('Созданы: 124');
    expect(exportWords.group(exportWords.kept, 1)).toBe('Не обновлены — вы их правили: 1');
    expect(exportWords.group(exportWords.skipped, 4)).toBe('Не прочитались — оставлены как есть: 4');
    expect(['Герои/Мара.md', 'Главы/Последний свет/1 Пепел.md', '_Удалено/Призрак.md', 'Сага.MD'].map(exportName))
      .toEqual(['Герои/Мара', 'Главы/Последний свет/1 Пепел', '_Удалено/Призрак', 'Сага']);
    expect(exportWords.question(5, 'CanonKeeper/Хроники Белого порта')).toBe('Ваши версии (5) уйдут в папку «_Удалено» внутри «CanonKeeper/Хроники Белого порта» — их можно открыть там же, в Obsidian. На место живых героев и глав лягут свежие заметки CanonKeeper.');
  });

  it('says the end of an export on another page: created and updated, or what stayed unchanged', () => {
    expect(exportEndLine({ ...exported, complete: true })).toBe('Выгрузка в Obsidian закончена: создано 124, обновлено 14');
    expect(exportEndLine({ ...exported, updated: group(0), complete: true })).toBe('Выгрузка в Obsidian закончена: создано 124');
    expect(exportEndLine({ ...exported, created: group(0), updated: group(0), complete: true })).toBe('Выгрузка в Obsidian закончена: без изменений 602');
    expect(exportEndLine({ ...exported, complete: false })).toBe('Выгрузка в Obsidian не закончилась');
    // A refusal: no answer.
    expect(exportEndLine(null)).toBe('Выгрузка в Obsidian не закончилась');
  });

  it('says the refusals of the export at its place: the folder named when the page knows it, no ways out but «Повторить» of busy', () => {
    const refused = (code: string, fields: object = {}, status = 409) => new MockApiError(status, code, 'exportObsidianSaga', code, { fields });
    expect(refusalOf(refused('obsidian.export_dir_unsafe'), 'export', undefined, 'CanonKeeper/Хроники Белого порта')).toMatchObject({
      text: 'Папку «CanonKeeper/Хроники Белого порта» записать нельзя: на пути к ней вместо папки стоит файл или ярлык. Переименуйте его в Obsidian и выгрузите снова.',
      exits: [], reread: true,
    });
    expect(refusalOf(refused('obsidian.export_dir_unsafe'), 'export').text).toBe('Папку выгрузки внутри «CanonKeeper» записать нельзя: на пути к ней вместо папки стоит файл или ярлык. Переименуйте его в Obsidian и выгрузите снова.');
    expect(refusalOf(refused('obsidian.export_manifest_unreadable'), 'export', undefined, 'CanonKeeper/Хроники Белого порта').text)
      .toBe('CanonKeeper не узнаёт свою папку «CanonKeeper/Хроники Белого порта»: её служебный файл повреждён. Перенесите свои правки из неё, удалите папку в Obsidian и выгрузите снова — папка появится заново.');
    // The server says it only of the folder the saga keeps: without it the page does not invent a name — the block of a failure.
    expect(refusalOf(refused('obsidian.export_manifest_unreadable'), 'export')).toMatchObject({ failure: true, exits: ['retry'] });
    expect(refusalOf(refused('obsidian.slow', {}, 503), 'export')).toEqual({ text: 'Папка Obsidian отвечает слишком долго — ничего не записано. Если она на облачном диске, сохраните её на этом устройстве и выгрузите снова.', exits: [] });
    expect(refusalOf(refused('obsidian.slow', {}, 503), 'scan').text).toBe(words.slow);
    expect(refusalOf(refused('obsidian.busy', {}, 429), 'export')).toEqual({ text: 'Папка всё ещё читается.', exits: ['retry'] });
    expect(refusalOf(refused('obsidian.map_not_ready', { reason: 'updating' }), 'export')).toEqual({ text: '', exits: [], map: true });
    expect(refusalOf(refused('obsidian.vault_missing', { vaultReason: 'not_found' }), 'export')).toEqual({ text: words.missing, exits: [], reread: true });
    expect(refusalOf(refused('obsidian.not_linked'), 'export')).toMatchObject({ exits: [], reread: true });
    expect(refusalOf(refused('rate_limit.exceeded', {}, 429), 'export')).toEqual({ text: words.rate, exits: [] });
    for (const code of ['idempotency.conflict', 'request.validation', 'server.error', 'conflict']) {
      expect(refusalOf(refused(code, {}, 400), 'export'), code).toMatchObject({ failure: true, exits: ['retry'] });
    }
    expect(refusalOf(new Error('lost'), 'export')).toMatchObject({ failure: true, exits: ['retry'] });
  });

  it('says the reasons of a card that waits and of the button that waits, by Data', () => {
    expect(exportWords.updating).toBe('Карта саги обновляется — выгрузить можно, когда она будет готова');
    expect(exportWords.noCards).toBe('Выгружать пока нечего: помощник ещё не прочитал ни одной главы');
    expect(exportWords.noBooks).toBe('Выгружать пока нечего: в саге ещё нет книг');
    expect(exportWords.going).toBe('Идёт выгрузка в Obsidian…');
    expect(exportWords.unlinkGoing).toBe('Выгрузка, которая идёт, не закончится.');
    expect(Object.values(exportSkipWords)).toEqual([
      'лежат только в облаке — сохраните папку на этом устройстве и выгрузите снова', 'открыты в другой программе — закройте их и выгрузите снова',
      'не открылись: нет прав на чтение', 'это ярлыки на другие файлы — по ним CanonKeeper не ходит',
    ]);
  });
});

/** Every saying of the words of the export, its templates filled. */
function exportSayings(): string[] {
  const fill = (value: unknown): string[] => (typeof value === 'string' ? [value]
    : [(value as (...args: unknown[]) => string)(...Array.from({ length: (value as () => string).length }, (_, index) => ['CanonKeeper/Хроники Белого порта', 5][index]))]);
  return Object.values(exportWords).flatMap(fill).concat(Object.values(exportSkipWords),
    exportSummary({ created: { count: 1 }, updated: { count: 2 }, unchanged: { count: 3 }, moved: { count: 4 } })!);
}

/** Every saying of the table of the bridge, its templates filled. */
function sayings() {
  const sample = ['Книга 1', 'Книги/Книга 1', 'Застава'];
  /** The templates that take a list: their own samples. */
  const lists: Partial<Record<keyof typeof words, unknown[]>> = { folderChanged: [['Книга 1', 'Застава']], wCount: [26, 24, ['Прилив и отлив']] };
  return Object.entries(words).flatMap(([name, value]) => {
    if (typeof value === 'string') return [value];
    const fill = value as (...args: unknown[]) => string;
    return [fill(...(lists[name as keyof typeof words] ?? Array.from({ length: fill.length }, (_, index) => sample[index])))];
  }).concat(Object.values(skipWords), Object.values(choiceRefusalWords), Object.values(materialChoiceWords));
}

/** Every saying of the words of the update of a book, its templates filled. */
function updateSayings(): string[] {
  const fill = (value: unknown): string[] => {
    if (typeof value === 'string') return [value];
    if (typeof value === 'function') {
      const template = value as (...args: unknown[]) => string;
      return [template(...Array.from({ length: template.length }, (_, index) => ['Книга 1', 24, 26][index]))];
    }
    return Object.values(value as Record<string, unknown>).flatMap(fill);
  };
  return Object.values(updateWords).flatMap(fill).concat(updateWords.unread(2, 1), updateWords.unread(1, 0));
}

// OW7.3: «хранилище» in the app is only the password store of the system and the vault of Obsidian, in any case; no saying of
// the bridge is a word the dictionary hides at the places it stands.
describe('the dictionary and the bridge', () => {
  const store = hiddenWords.find((word) => word.says.startsWith('«хранилище»'))!;

  it('hides «хранилище» in the app outside the two names, in any case, and keeps the two names', () => {
    for (const saying of ['Хранилище заметок', 'в хранилище ключей', 'нет хранилищ', 'хранилищу данных']) {
      expect(hiddenIn(saying, 'src/editions/desktop/obsidian/words.ts')).toContain(store);
    }
    for (const saying of [
      'Хранится в хранилище паролей системы', 'в системе нет хранилища паролей', 'хранилищу паролей системы', 'Выберите папку хранилища Obsidian',
      'с хранилищем Obsidian', 'хранилищами Obsidian',
    ]) {
      expect(hiddenIn(saying, 'src/editions/desktop/obsidian/words.ts')).not.toContain(store);
    }
    expect(hiddenIn('Хранилище заметок', 'src/features/chat/Chat.tsx')).not.toContain(store);
  });

  it('hides no saying of the bridge where it stands', () => {
    const found = sayings().flatMap((saying) => hiddenIn(saying, 'src/editions/desktop/obsidian/words.ts').map((word) => `${word.says}: ${saying}`));
    expect(found).toEqual([]);
    // The words of the update of a book (specs/2026-11-15-desk-obsidian-book-web.md): where they stand and in «Книги» and the shell
    // that say them.
    const update = updateSayings();
    expect(update.length).toBeGreaterThan(40);
    for (const where of ['src/editions/desktop/obsidian/words.ts', 'src/features/manuscript/shelf/ManuscriptShelfPage.tsx', 'src/app/shell/AppShell.tsx']) {
      expect(update.flatMap((saying) => hiddenIn(saying, where).map((word) => `${word.says}: ${saying}`))).toEqual([]);
    }
    for (const where of ['src/features/manuscript/shelf/ManuscriptShelfPage.tsx', 'src/features/saga/materials/MaterialsPart.tsx']) {
      expect([words.from, words.fromCaption].flatMap((saying) => hiddenIn(saying, where))).toEqual([]);
    }
    // The words of the export (XW3.3): at the page, the line of the end in the area of the toasts and the item of the map.
    const exportsSaid = exportSayings();
    expect(exportsSaid.length).toBeGreaterThan(30);
    expect(exportsSaid.flatMap((saying) => hiddenIn(saying, 'src/editions/desktop/obsidian/words.ts').map((word) => `${word.says}: ${saying}`))).toEqual([]);
    expect([exportWords.ended('создано 1, обновлено 2'), exportWords.notEnded, words.openResult]
      .flatMap((saying) => hiddenIn(saying, 'src/app/shell/AppShell.tsx').map((word) => `${word.says}: ${saying}`))).toEqual([]);
    expect(hiddenIn(exportWords.mapItem, 'src/features/saga/MapToolbar.tsx')).toEqual([]);
  });
});
