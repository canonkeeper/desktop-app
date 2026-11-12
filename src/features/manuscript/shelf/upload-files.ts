import type { AccountQuota } from '../../../entities/account-quota/api';
import type { Book, ImportQueue, ImportQueueItem } from '../../../entities/manuscript/api';
import { formatCount, pluralize } from '../../../ui/copy';
import { oversizedFileMessage, unsupportedFileMessage, type ImportFileConstraints } from './import-failure';

/**
 * One way from a file to a book (specs/2026-11-11-ux7-upload.md): the files the author gives the page are checked here
 * before any transfer, put in the order of the volumes and, once sent, shown as cards of the shelf in the words of the
 * author. Everything the page says of the upload is the server's queue (`listImportQueues`) and the files of this tab.
 */

/** A file of the author with the key the page names it by while it is local. */
export type LocalFile = Readonly<{ key: string; file: File }>;

/** A file the page takes: its volume number from its name, if it names one. */
export type ChosenFile = LocalFile & Readonly<{ volume: number | null }>;

/** A file the page does not take, with the reason in the words of the author (UP1.2). */
export type RefusedFile = LocalFile & Readonly<{ reason: string }>;

const volumePattern = /(?:^0*(?<leading>\d+)(?=[\s._-])|(?:том|tom|volume|vol|книга|part)[\s._-]*0*(?<labelled>\d+))/giu;

/** The volume a file name names — «Том 5.fb2», «05-severny.epub» — or null when it names none or several. */
export function detectImportVolume(fileName: string): number | null {
  const volumes = new Set<number>();
  for (const match of fileName.matchAll(volumePattern)) {
    const volume = Number(match.groups?.leading ?? match.groups?.labelled);
    if (Number.isSafeInteger(volume) && volume > 0) volumes.add(volume);
  }
  return volumes.size === 1 ? [...volumes][0]! : null;
}

const fileKinds = ['.fb2.zip', '.fb2', '.epub', '.docx', '.txt', '.md', '.rtf', '.odt'];

/** The name of a file without the end of its kind: «Том 8. Ледяной причал» of «Том 8. Ледяной причал.fb2». */
export function fileStem(fileName: string) {
  const lower = fileName.toLowerCase();
  const kind = fileKinds.find((each) => lower.endsWith(each));
  return (kind ? fileName.slice(0, fileName.length - kind.length) : fileName).trim();
}

let nextKey = 0;

/** The files of a choice with keys of this tab. */
export function localFiles(files: readonly File[]): LocalFile[] {
  return files.map((file) => ({ key: `local-${(nextKey += 1)}-${file.name}`, file }));
}

/**
 * UP1.2: the check before any transfer — the kind of the file by the kinds of the server, its size and that it is not
 * empty. A file that does not pass gets its reason; it does not hold the others back. The server still checks every file.
 */
export function screenFiles(files: readonly LocalFile[], constraints: ImportFileConstraints) {
  const chosen: ChosenFile[] = [];
  const refused: RefusedFile[] = [];
  for (const local of files) {
    const reason = unsupportedFileMessage(local.file.name, constraints)
      ?? (local.file.size > constraints.maxFileSizeBytes ? oversizedFileMessage(constraints) : null)
      ?? (local.file.size === 0 ? 'Файл пустой — в нём нет текста. Выберите файл книги.' : null);
    if (reason) refused.push({ ...local, reason });
    else chosen.push({ ...local, volume: detectImportVolume(local.file.name) });
  }
  return { chosen, refused };
}

/** A row of the window «Порядок томов»: `check` — its name has no number among files that have one (UP2.3). */
export type OrderRow = ChosenFile & Readonly<{ check: boolean }>;

/**
 * UP2.3: the files with a number in their name stand by it, the others below them in the order they came, each with
 * «проверьте место» when the others are numbered. The order of the rows is the order of the volumes.
 */
export function orderRows(files: readonly ChosenFile[]): OrderRow[] {
  const numbered = files.filter((file) => file.volume !== null).sort((left, right) => left.volume! - right.volume!);
  const rest = files.filter((file) => file.volume === null);
  return [
    ...numbered.map((file) => ({ ...file, check: false })),
    ...rest.map((file) => ({ ...file, check: numbered.length > 0 })),
  ];
}

/** The row moved to the place of another one: the order of the volumes changes only so (UP2.3). */
export function moveRow<T extends { key: string }>(rows: readonly T[], fromKey: string, toKey: string): T[] {
  const from = rows.findIndex((row) => row.key === fromKey);
  const to = rows.findIndex((row) => row.key === toKey);
  if (from < 0 || to < 0 || from === to) return [...rows];
  const moved = [...rows];
  const [row] = moved.splice(from, 1);
  moved.splice(to, 0, row!);
  return moved;
}

/** Words as the comparison reads them: lower case, «ё» as «е», a soft hyphen is nothing, any other sign a space. */
const normalize = (words: string) => words.toLowerCase().replace(/ё/gu, 'е').replace(/[\u00ad\u200b-\u200d\u2060]/gu, '')
  .replace(/[^\p{L}\p{N}]+/gu, ' ').trim();

/** How much of the beginning of a book the beginning of a file must hold, and the least that tells anything (SC1.3). */
const openingLetters = 80;
const shortestOpening = 24;

/**
 * The file begins as the book does (specs/2026-11-11-ux9-shelf-chat.md SC1.3): the first letters of how the book begins
 * stand among the first pages of the file — under the title, the author and the annotation a file carries before them.
 */
function beginsAs(fileText: string, opening: string | undefined) {
  const start = normalize(opening ?? '').slice(0, openingLetters).trim();
  return start.length >= shortestOpening && normalize(fileText).includes(start);
}

/**
 * UP2.2, SC1.3: the book of the shelf a new file looks like — the same title, the same volume number in its name as the
 * place of a book or a number in its title, or the same beginning of the text, whatever the name of the file. A wrong
 * guess costs a question, a missed one a second copy of a volume. `fileText` is how the file begins (`fileOpening`).
 */
export function similarBook<B extends Pick<Book, 'title' | 'place'> & { opening?: string | undefined }>(
  fileName: string, books: readonly B[], fileText: string | null = null,
): B | null {
  const stem = normalize(fileStem(fileName));
  const volume = detectImportVolume(fileName);
  return books.find((book) => {
    const title = normalize(book.title);
    if (title && (stem === title || (stem.includes(title) && title.length >= 4))) return true;
    if (volume === null) return false;
    return book.place === volume || detectImportVolume(book.title) === volume;
  }) ?? (fileText ? books.find((book) => beginsAs(fileText, book.opening)) ?? null : null);
}

/** How many new books the counters of the account still take, and the limit that says so (UP1.3). */
export type UploadRoom = Readonly<{ room: number; code: 'quota.project_books' | 'quota.account_books' | 'quota.imports_daily' }>;

/**
 * UP1.3: the room of the saga, of all the sagas and of the uploads of the day — the same counters the quiet line of the
 * shelf reads. Null while the counters are not read or nothing is limited: then the server decides alone.
 */
export function uploadRoom(quota: AccountQuota | undefined, projectId: string | null): UploadRoom | null {
  if (!quota) return null;
  const left = (counter: { used: number; limit: number } | undefined) => counter && counter.limit > 0
    ? Math.max(0, counter.limit - counter.used) : null;
  const limits: UploadRoom[] = [];
  const saga = left(quota.ownedProjects.find((project) => project.projectId === projectId)?.sources.books);
  if (saga !== null) limits.push({ room: saga, code: 'quota.project_books' });
  const account = left(quota.account.books);
  if (account !== null) limits.push({ room: account, code: 'quota.account_books' });
  const day = left(quota.imports);
  if (day !== null) limits.push({ room: day, code: 'quota.imports_daily' });
  return limits.sort((left, right) => left.room - right.room)[0] ?? null;
}

const numberWords = ['одну', 'две', 'три', 'четыре'];

/** «Загрузить две», «Загрузить 5»: the number of books a button loads, in words up to four. */
export function loadNumber(count: number) {
  return numberWords[count - 1] ?? String(count);
}

/** The main button of the window: «Загрузить книгу», «Загрузить 3 книги», «Загрузить 12 книг» (UP2.4). */
export function uploadButtonWords(count: number) {
  return count === 1 ? 'Загрузить книгу' : `Загрузить ${formatCount(count, 'книгу', 'книги', 'книг')}`;
}

/**
 * The words of the block that says what fits before any transfer (UP1.3): what fits of the chosen books and when the
 * rest can go. The next tier is the third part of the block, said by the build.
 */
export function fitWords(room: UploadRoom, chosen: number) {
  if (room.code === 'quota.imports_daily') {
    return {
      ended: `Сегодня можно загрузить ещё ${formatCount(room.room, 'книгу', 'книги', 'книг')} из ${chosen}`,
      returns: 'Остальные — после 00:00 по Москве.',
    };
  }
  const where = room.code === 'quota.project_books' ? 'В этой саге' : 'Во всех ваших сагах';
  return {
    ended: `${where} поместится ${room.room} из ${formatCount(chosen, 'книги', 'книг', 'книг')}`,
    returns: room.code === 'quota.project_books' ? 'Остальные можно загрузить в другую сагу.' : 'Новые поместятся, когда книг станет меньше.',
  };
}

/** The state of a volume on its card of the shelf (UP3.1, UP3.2). */
export type VolumeState = 'waiting' | 'transfer' | 'loading' | 'lost' | 'failed';

/** What this tab knows of the file of a volume it sends. */
export type TransferState = Readonly<{ state: 'waiting' | 'sending' | 'sent' | 'failed'; sent: number; total: number | null }>;

/** A volume of the upload that is not a book of the shelf yet: a card at its place (UP3.1). */
export type VolumeView = Readonly<{
  key: string;
  itemId: string | null;
  position: number;
  fileName: string;
  state: VolumeState;
  /** 0..1 of the file this tab sends. */
  progress: number | null;
  /** The code of the reason a volume waits for the author: of the check of its file, or of its import Job. */
  reasonCode: string | null;
  /** The import Job of a volume that did not read: its error names the reason. */
  failedJobId: string | null;
}>;

/** The volumes of a queue that are still the queue's: in work, waiting for the author — not skipped, not stopped. */
export function liveItems(queue: ImportQueue) {
  return queue.items.filter((item) => item.processingStatus !== 'skipped' && item.processingStatus !== 'canceled'
    && item.processingStatus !== 'canceling').sort((left, right) => left.position - right.position);
}

/** A volume waits for the author at its place: its Job did not read the file, or its file did not pass the check (UP3.2). */
export function waits(item: ImportQueueItem) {
  return item.processingStatus === 'failed';
}

/**
 * The cards of the volumes of the upload that are not on the shelf yet, by the queue of the server and the files of
 * this tab: «Ждёт загрузки» — waiting its turn, «Загружается» — the file goes or the server reads it, «Не загрузилась» —
 * it waits for the author, «Файл не дошёл» — its file was not sent, and this tab does not have it (UP3.1, UP3.2).
 */
export function volumeViews(queue: ImportQueue | null, transfers: Readonly<Record<string, TransferState>>, shelfBookIds: ReadonlySet<string>): VolumeView[] {
  if (!queue || !['uploading', 'draft', 'running'].includes(queue.status)) return [];
  const items = liveItems(queue);
  const working = items.find((item) => ['importing', 'retrying'].includes(item.processingStatus))
    ?? items.find((item) => item.processingStatus === 'queued');
  return items.filter((item) => !(item.bookId && shelfBookIds.has(item.bookId))).map((item) => {
    const transfer = transfers[item.clientItemId];
    const base = {
      key: item.clientItemId, itemId: item.id, position: item.position, fileName: item.sourceFileName,
      progress: null, reasonCode: null, failedJobId: null,
    };
    if (waits(item)) {
      return {
        ...base, state: 'failed' as const,
        reasonCode: item.preflightError?.code ?? null,
        failedJobId: item.failedStep === 'import' ? item.importJobId : null,
      };
    }
    if (item.preflightStatus === 'uploading') {
      if (transfer?.state === 'sending') {
        return { ...base, state: 'transfer' as const, progress: transfer.total ? transfer.sent / transfer.total : null };
      }
      if (transfer?.state === 'failed') return { ...base, state: 'lost' as const };
      return { ...base, state: transfer ? 'waiting' as const : 'lost' as const };
    }
    if (item.id === working?.id || item.processingStatus === 'indexing' || item.processingStatus === 'succeeded') {
      return { ...base, state: 'loading' as const };
    }
    return { ...base, state: 'waiting' as const };
  });
}

/** The cards of the volumes of a choice not yet sent: each is a card at once (UP3.1). */
export function pendingViews(files: readonly LocalFile[]): VolumeView[] {
  return files.map((local, index) => ({
    key: local.key, itemId: null, position: index + 1, fileName: local.file.name, state: 'waiting',
    progress: null, reasonCode: null, failedJobId: null,
  }));
}

/** The words of a card of a volume (UP3.1): «Ждёт загрузки» → «Загружается» → the card of its book. */
export function volumeWord(view: VolumeView) {
  switch (view.state) {
    case 'transfer': return view.progress === null ? 'Загружается' : `Загружается · ${Math.round(view.progress * 100)} %`;
    case 'loading': return 'Загружается';
    case 'lost': return 'Файл не дошёл';
    case 'failed': return 'Не загрузилась';
    default: return 'Ждёт загрузки';
  }
}

/** One entry of the shelf: a book, or a card of a volume of the upload at its place (UP3.1). */
export type ShelfEntry<B> = { kind: 'book'; book: B } | { kind: 'volume'; volume: VolumeView };

/**
 * The books of the shelf with the cards of the upload at their places: the books not of the upload by their place, then
 * the volumes of the upload in the order of their rows — a volume with its book on the shelf as that book, the others as
 * cards (UP3.1, UP3.2: a volume that waits keeps its place between the others).
 */
export function shelfEntries<B extends { id: string }>(books: readonly B[], queue: ImportQueue | null, volumes: readonly VolumeView[]): ShelfEntry<B>[] {
  if (volumes.length === 0 || !queue) return books.map((book) => ({ kind: 'book', book }));
  const ofUpload = new Map<string, number>();
  for (const item of queue.items) if (item.bookId) ofUpload.set(item.bookId, item.position);
  const shelf: ShelfEntry<B>[] = books.filter((book) => !ofUpload.has(book.id)).map((book) => ({ kind: 'book', book }));
  const upload: Array<{ position: number; entry: ShelfEntry<B> }> = [
    ...books.filter((book) => ofUpload.has(book.id)).map((book) => ({ position: ofUpload.get(book.id)!, entry: { kind: 'book' as const, book } })),
    ...volumes.map((volume) => ({ position: volume.position, entry: { kind: 'volume' as const, volume } })),
  ];
  return [...shelf, ...upload.sort((left, right) => left.position - right.position).map(({ entry }) => entry)];
}

/** The line of the saga about the upload (UP3.1): what goes, how far, what waits for the author. */
export type UploadLineView = Readonly<{
  title: string;
  detail: string;
  /** 0..1 of the volumes on the shelf. */
  progress: number | null;
  /** The volumes not on the shelf yet that a stop leaves: «Остановить остальные» while there are. */
  remaining: number;
}>;

/**
 * «Загружаю книги: 3 из 12 готово, идёт „Том 8“» (UP3.1): ready — the volumes whose books are on the shelf; the one that
 * goes — the file this tab sends or the volume the server reads; a volume that waits is named. No word of the machinery.
 */
export function uploadLine(queue: ImportQueue, views: readonly VolumeView[], sendingHere: boolean): UploadLineView {
  const items = liveItems(queue);
  const total = items.length;
  const onShelf = total - views.length;
  const goes = views.find((view) => view.state === 'transfer') ?? views.find((view) => view.state === 'loading');
  const waiting = views.filter((view) => view.state === 'failed' || view.state === 'lost');
  const remaining = views.filter((view) => view.state !== 'failed').length;
  const named = (view: VolumeView) => `«${fileStem(view.fileName)}»`;
  let title: string;
  if (goes) {
    title = total === 1 ? `Загружаю ${named(goes)}…` : `Загружаю книги: ${onShelf} из ${total} готово, идёт ${named(goes)}`;
  } else if (waiting.length > 0) {
    const who = waiting.length === 1 ? `${named(waiting[0]!)} ждёт` : `${formatCount(waiting.length, 'книга', 'книги', 'книг')} ждут`;
    title = total === 1 ? `${who} вашего решения` : `Загружаю книги: ${onShelf} из ${total} готово · ${who} вашего решения`;
  } else {
    title = total === 1 ? 'Загружаю книгу…' : `Загружаю книги: ${onShelf} из ${total} готово`;
  }
  const lost = views.some((view) => view.state === 'lost');
  const detail = lost
    ? 'Файл не дошёл до сервера: выберите его на карточке книги снова'
    : waiting.length > 0
      ? `Замените файл или пропустите — ${pluralize(waiting.length, 'на карточке книги', 'на карточках книг', 'на карточках книг')}`
      : sendingHere
        ? 'Не закрывайте вкладку, пока файлы уходят — дальше загрузка пойдёт сама'
        : 'Страницу можно закрыть — загрузка продолжится';
  return { title, detail, progress: total > 0 ? onShelf / total : null, remaining };
}

/**
 * The end of an upload (UP4.1, UP4.3): «Загружено 12 книг: 214 глав», and what the reading of the saga allows. While the
 * server says the saga is read, the books are loaded, not ready.
 */
export function uploadEndWords(books: number, chapters: number, preparing: boolean, waiting: string | null = null) {
  const one = books === 1;
  const message = one
    ? `Книга загружена: ${formatCount(chapters, 'глава', 'главы', 'глав')}`
    : `Загружено ${formatCount(books, 'книга', 'книги', 'книг')}: ${formatCount(chapters, 'глава', 'главы', 'глав')}`;
  // A reading that waits for the helper says its one line (specs/2026-11-12-desk-window.md WN3.2).
  const detail = waiting ?? (preparing
    ? `${one ? 'Её' : 'Их'} уже можно читать и спрашивать. Карта саги будет через несколько минут`
    : `Помощник прочитал ${one ? 'её' : 'их'} — можно спрашивать`);
  return { message, detail, look: one ? 'Как разобралась книга' : 'Посмотреть, как разобрались' };
}
