// SPDX-License-Identifier: AGPL-3.0-only
import type { Schema } from '../../../shared/api';
import { formatCount, pluralize } from '../../../ui/copy';

/**
 * The one table of the words of the bridge with an Obsidian vault (specs/2026-11-15-desk-obsidian-web.md, Data; board 44·3,
 * 44·47–44·54): the page of the bridge, its windows, its lines in the area of the toasts and the entries of «Книги» and «Ваши
 * материалы». The name of the thing is «папка Obsidian»; «хранилище Obsidian» only where the author must know the word of
 * Obsidian itself (OW7.3).
 */
export const words = {
  from: 'Из Obsidian…',
  fromCaption: 'Пишете в Obsidian? Возьму книги и записи прямо из папки с заметками',
  pageTitle: 'Книги и записи из Obsidian',
  startText: 'Выберите папку хранилища Obsidian — ту, которую вы открываете в Obsidian. CanonKeeper читает в ней только заметки (.md) и ничего в ней не меняет.',
  choose: 'Выбрать папку Obsidian…',
  windowOpen: 'Окно выбора папки открыто',
  expired: 'Папка не выбрана. Если окно выбора папки ещё открыто — закройте его и попробуйте ещё раз.',
  expiredChange: 'Папка не сменилась. Если окно выбора папки ещё открыто — закройте его и попробуйте ещё раз.',
  unavailable: 'Окно выбора папки не открылось. Перезапустите CanonKeeper и попробуйте снова.',
  folderName: (name: string) => `Папка Obsidian: ${name}`,
  change: 'Сменить папку…',
  changeNote: 'Книги и записи останутся связаны с папками и заметками по тем же путям внутри новой папки',
  unlinkItem: 'Отвязать папку…',
  folderActions: 'Действия папки Obsidian',
  unlinkTitle: (saga: string) => `Отвязать папку Obsidian от саги «${saga}»?`,
  unlinkText: 'Книги и записи останутся в саге, папка и заметки на диске не изменятся. Связи с заметками пропадут: обновлять книги из Obsidian будет нельзя. Если потом выбрать папку снова, свяжите каждую книгу с её папкой — иначе загрузка добавит книги второй раз; о записях приложение спросит «Заменить заметкой».',
  unlink: 'Отвязать',
  unlinked: 'Папка Obsidian отвязана',
  missing: 'Папка не найдена: возможно, её переименовали, перенесли или отключён диск.',
  checkAgain: 'Проверить снова',
  notFolderNow: 'По этому пути теперь не папка.',
  unreadableNow: 'Папку не удалось открыть: нет права на чтение.',
  retry: 'Повторить',
  checking: 'Проверяю папку…',
  slowFolder: 'Папка отвечает медленно',
  choiceTitle: (name: string) => `Что взять из папки «${name}»`,
  choiceExplain: 'Отметьте папки. «Книга» — заметки папки станут главами одной книги, по порядку. «Записи» — каждая заметка станет записью в «Ваших материалах». Остальное не берётся.',
  scanning: 'Читаю папку…',
  scanLong: 'В большой папке это занимает до 20 секунд',
  nested: (name: string, count: number) => `Вложенные папки «${name}»: ${count}`,
  inside: (count: number) => `внутри: ${formatCount(count, 'папка', 'папки', 'папок')} со своим выбором`,
  fixed: 'Состав книги — как при её загрузке',
  fixedPart: 'В составе книги',
  fixedLeft: 'Не берётся — как при её загрузке',
  bookIn: (title: string) => `книга «${title}» уже в саге`,
  bookTrash: (title: string) => `книга «${title}» в «Недавно удалённых»`,
  choiceOf: (name: string) => `Что взять из папки «${name}»`,
  skip: 'Не брать',
  book: 'Книга',
  together: (name: string) => `Вместе с «${name}»`,
  whyInside: (book: string) => `Папка внутри книги «${book}»: её заметки уже входят в эту книгу`,
  whyHolds: (book: string) => `В папке лежит книга «${book}»: выберите что-то одно`,
  show: 'Показать, что получится',
  whyNone: 'Отметьте хотя бы одну папку: книгу или записи',
  whyLimit: 'За раз — не больше 200 книг и 200 папок записей',
  whyExcl: 'Слишком много исключений: уберите часть или отметьте папки иначе',
  whyReading: 'Дочитываю прежний выбор…',
  rescan: 'Прочитать папку заново',
  cut: 'Папка очень большая — показана её часть. Выберите папку поменьше: «Сменить папку…»',
  noNotes: 'В этой папке нет заметок (.md).',
  cloudEmpty: 'Заметки этой папки лежат только в облаке. В проводнике выберите у папки «Всегда сохранять на этом устройстве», дождитесь загрузки и прочитайте папку заново.',
  cloudNote: 'Часть заметок лежит только в облаке: папки, где таких заметок все, здесь не видны.',
  busy: (seconds: number) => `Папка ещё читается — попробую снова через ${formatCount(seconds, 'секунду', 'секунды', 'секунд')}`,
  busyEnd: 'Папка всё ещё читается.',
  slow: 'Папка читается слишком долго — ничего не записано. Если она на облачном диске, сохраните её на этом устройстве; или отметьте меньше папок.',
  rate: 'Слишком часто. Подождите минуту и повторите.',
  /** A write of «Загрузить» refused as too often: after «Записи не сохранены:» / «Книги не загружены:», with «Повторить». */
  rateLoad: 'слишком часто, подождите минуту.',
  scanFailed: 'Не прочиталась папка Obsidian',
  previewFailed: 'Не прочитались заметки папки',
  decisionTitle: 'Нужно ваше решение',
  relink: (book: string, folder: string, holder: string) => `Книга «${book}» была связана с папкой «${folder}», теперь эта папка у книги «${holder}».`,
  bookFolderMissing: (book: string) => `Папки книги «${book}» нет на месте: её переименовали или перенесли?`,
  linkOther: 'Связать с другой папкой…',
  unlinkBook: 'Снять связь',
  unlinkedBook: (book: string) => `Связь книги «${book}» с папкой снята`,
  linkTitle: 'Связать с папкой Obsidian',
  linkedTo: (folder: string) => `Книга связана с папкой «${folder}».`,
  linkMissing: 'Папки нет на месте: её переименовали или перенесли.',
  linkWhy: 'Свяжите книгу с папкой заметок, чтобы обновлять её из Obsidian одной кнопкой. Текст книги при этом не меняется.',
  heldBook: (book: string) => `у книги «${book}»`,
  insideBook: (book: string) => `внутри папки книги «${book}»`,
  holdsBook: (book: string) => `в ней папка книги «${book}»`,
  doLink: 'Связать',
  waitLoad: 'Дождитесь конца загрузки книг',
  pickFolder: 'Отметьте папку',
  linkedDone: (book: string, folder: string) => `Книга «${book}» связана с папкой «${folder}»`,
  folderLinked: (book: string) => `Эта папка уже у книги «${book}»`,
  folderMissing: 'Этой папки уже нет — прочитайте папку заново',
  bookGone: 'Этой книги уже нет в саге',
  cancel: 'Отмена',
  previewTitle: 'Что получится',
  books: 'Книги',
  records: 'Записи',
  readingNotes: 'Читаю заметки…',
  readingLong: 'Обычно это секунды, в большой папке — до двух минут. Ничего не меняется, пока вы не нажмёте «Загрузить».',
  changeChoice: 'Изменить выбор папок',
  stNew: 'Новая книга',
  stCant: (reason: string) => `Не загрузится: ${reason}`,
  stSimilar: (book: string) => `В саге уже есть «${book}». Если это она — свяжите; иначе загрузится новой книгой.`,
  stSimilarMissing: (book: string) => `В саге уже есть «${book}» — её папки нет на месте. Если это она — свяжите; иначе загрузится новой книгой.`,
  linkSimilar: (book: string) => `Связать с «${book}»`,
  stSame: (book: string) => `Уже в саге: «${book}» — без изменений`,
  stChanged: (book: string) => `Уже в саге: «${book}» — в заметках есть изменения`,
  stBroken: (book: string, reason: string) => `Уже в саге: «${book}» — из заметок папки книга сейчас не собирается: ${reason}`,
  stTrash: (book: string) => `Книга «${book}» этой папки — в «Недавно удалённых»`,
  openTrash: 'Открыть «Недавно удалённые»',
  stLoading: 'Загружается или ждёт вашего решения на странице «Книги»',
  toBooks: 'К книгам',
  cantOther: 'Книгу из этой папки собрать не удалось',
  wCount: (chapters: number, notes: number, examples: readonly string[]) => `Глав получилось ${chapters}, а заметок ${notes}${examples.length ? ` — например: ${quoted(examples)}` : ''}`,
  wOne: (note: string) => `Вся книга в одной заметке «${note}»: главы взяты по её заголовкам`,
  wOrder: 'Порядок глав — по именам заметок, номеров в них нет: проверьте порядок ниже. Свой порядок задают номера в именах или заметка «_book» со ссылками на главы',
  wLong: 'Название длиннее 120 знаков — обрезано',
  notesToggle: (count: number) => `Заметки и главы (${count})`,
  chapter: (number: number, title: string) => `гл. ${number} „${title}“`,
  orderFront: 'по свойству заметки',
  orderIndex: 'по заметке «_book»',
  orderNumber: 'по номеру в имени',
  orderName: 'по имени',
  find: 'Найти запись',
  more: 'Показать ещё',
  rNew: 'Новая',
  rNewIn: (name: string) => `Новая; в книге есть «${name}»`,
  rUpd: 'Обновится',
  rSame: 'Без изменений',
  rEdited: 'Вы правили её в приложении',
  rTaken: (name: string) => `Имя «${name}» уже у записи в «Ваших материалах»`,
  rHeld: (name: string, note: string) => `Имя «${name}» у заметки «${note}»: эта заметка записью не станет. Переименуйте одну из заметок в Obsidian и покажите заново`,
  replace: 'Заменить заметкой',
  keep: 'Не менять',
  forEdited: (count: number) => `Для всех правленных в приложении (${count}):`,
  forTaken: (count: number) => `Для всех с занятым именем (${count}):`,
  deadlock: (first: string, second: string) => `Заметки «${first}» и «${second}» поменялись названиями, а запись одной из них правлена в приложении. Переименуйте или удалите запись в «Ваших материалах» либо верните название в Obsidian — и покажите заново.`,
  rwList: 'Заметка-список: запись будет одна',
  rwSections: 'В заметке несколько разделов: запись будет одна',
  rwCut: 'Часть текста записи обрезана до предела',
  rwProps: (count: number) => `Не вошли свойства: ${count} (больше 50 или слишком длинное имя)`,
  staleChoice: 'Выбор изменён',
  notTaken: (count: number) => `Не берутся: ${formatCount(count, 'заметка', 'заметки', 'заметок')}`,
  giveBack: 'Вернуть',
  staleBooks: 'Книги изменились',
  showAgain: 'Показать заново',
  whyStale: 'Сначала покажите заново',
  readNone: 'Читать и искать по новым книгам можно сразу. Помощник прочитает их, когда вы его подключите.',
  readNow: (amount: string) => `Помощнику предстоит прочитать новые и изменённые главы: ${amount}.`,
  readLater: (amount: string) => `Новые и изменённые главы (${amount}) помощник прочитает, когда вы разрешите: «Прочитать книги…» на странице «Книги».`,
  notUpdated: (book: string) => `Книга «${book}» этой кнопкой не обновится: её обновляет «Обновить из Obsidian…» в меню книги`,
  load: 'Загрузить',
  nothing: 'Загружать нечего: книги уже в саге, записи без изменений',
  nothingRefused: 'Загружать нечего: новые книги не загрузятся (причины — на карточках), записи без изменений',
  queueRunning: 'Книги сейчас не загрузить: ещё идёт загрузка книг. Записи загрузятся; за книгами вернитесь, когда она закончится.',
  queueWaits: (book: string) => `Книги сейчас не загрузить: книга «${book}» ждёт вашего решения на странице «Книги». Записи загрузятся.`,
  saving: 'Сохраняю записи…',
  adding: 'Добавляю книги в сагу…',
  keptTaken: 'Оставлены — имя занято: ',
  keptEdited: 'Оставлены — вы правили их в приложении: ',
  keptChanged: 'Не записаны — их записи в «Ваших материалах» изменились, пока вы смотрели: ',
  bookQueued: (book: string) => `«${book}» — загружается`,
  bookLinked: (book: string) => `«${book}» — уже в саге`,
  bookInTrash: (book: string) => `«${book}» — книга в «Недавно удалённых»`,
  bookRefused: (book: string, reason: string) => `«${book}» — не загрузилась: ${reason}`,
  toMaterials: 'К вашим материалам',
  loadMore: 'Загрузить ещё',
  openResult: 'Открыть итог',
  loadGoing: 'Идёт загрузка из Obsidian…',
  loadEnded: 'Последняя загрузка из Obsidian закончена',
  partRecords: (reason: string) => `Записи не сохранены: ${reason}`,
  partBooks: (reason: string) => `Книги не загружены: ${reason}`,
  folderChanged: (folders: readonly string[]) => `заметки в ${folders.length === 1 ? 'папке' : 'папках'} ${quoted(folders)} изменились, пока вы смотрели.`,
  interrupted: 'загрузка прервалась — лишнего не создано.',
  loadBooksAgain: 'Загрузить книги ещё раз',
  queueBusy: 'в саге уже идёт загрузка книг.',
  overlap: (outer: string, inner: string) => `Папка «${inner}» лежит внутри книги «${outer}» — выберите что-то одно.`,
  gone: 'Одной из отмеченных папок уже нет.',
  validation: 'Не получилось отправить выбор. Покажите заново и загрузите ещё раз.',
  conflict: 'записи саги менялись в ту же секунду.',
  // `too_large` of a choice: the numbers of the refusal; in a preview the line begins with a capital (Data `too_large` (форма)).
  tooManyNotes: (notes: number, limit: number) => `выбрано слишком много: ${notes.toLocaleString('ru-RU')} заметок при пределе ${limit.toLocaleString('ru-RU')}. Уберите часть папок.`,
  tooHeavy: (size: string, limit: string) => `выбрано слишком много: ${size} при пределе ${limit}. Уберите часть папок.`,
  tooManyEntries: (entries: number, limit: number) => `в выбранных папках слишком много файлов: ${entries.toLocaleString('ru-RU')} при пределе ${limit.toLocaleString('ru-RU')}. Уберите часть папок.`,
  tooManyRecords: (records: number, limit: number) => `записей получится ${records.toLocaleString('ru-RU')} при пределе ${limit.toLocaleString('ru-RU')} на сагу. Уберите часть папок записей.`,
  /** A failure without a reason of its own (specs/2026-11-10-ux2-patterns-refusals.md PR3.3). */
  failure: 'Это сбой на нашей стороне, попробуйте через минуту.',
  /** The line of the toasts of a failed action of the page: the unlink of the folder, the link of a book and its unlink. */
  tryAgain: 'Не получилось — попробуйте ещё раз',
  /** The block of a failure to read the books of the saga — the words of «Книги» (ShelfHeader). */
  booksFailed: 'Не открылись книги саги',
} as const;

/**
 * The words of the book and its folder (specs/2026-11-15-desk-obsidian-book-web.md, Data; board 44·55–44·57): the item of the
 * menu of a book, the window «Обновить книгу из Obsidian», its refusals, the lines of «Книги» and of the shell of an update from a
 * folder, and the words of the app about the update of a book — no file where the app does not know where the update came from,
 * no site.
 */
export const updateWords = {
  updateItem: 'Обновить из Obsidian…',
  linkItem: 'Связать с папкой Obsidian…',
  title: 'Обновить книгу из Obsidian',
  reading: 'Читаю заметки…',
  unchangedUntil: (book: string) => `«${book}» не меняется, пока вы не нажмёте «Обновить книгу».`,
  /** The folder of the book before the server read it: its numbers come with the report. */
  folder: (folder: string) => `Папка «${folder}»`,
  folderCounted: (folder: string, notes: number, chapters: number) => (
    `Папка «${folder}»: ${formatCount(notes, 'заметка', 'заметки', 'заметок')}, ${formatCount(chapters, 'глава', 'главы', 'глав')}`),
  otherFolder: 'Другая папка…',
  report: 'Подробнее о заметках папки',
  folderGone: (folder: string) => `Папки «${folder}» нет на месте: её переименовали или перенесли. Книга не изменилась.`,
  vaultGone: 'Папка Obsidian саги не найдена: возможно, её перенесли или отключён диск.',
  openBridge: 'Открыть «Из Obsidian…»',
  planTitle: (book: string) => `Что изменится в «${book}»`,
  notesKept: 'Ваши заметки к книге останутся у своих мест; если цитаты заметки в новом тексте нет, заметка сохранит её и откроет начало главы.',
  notesLeaving: 'Ваши заметки уходящих глав сохранятся и вернутся вместе с главой из «Недавно удалённых».',
  wrong: 'Если заметки окажутся не те, прежнюю версию книги можно вернуть 7 дней — в «Что изменилось» у книги.',
  text: 'текстом заметок',
  confirm: 'Обновить книгу',
  noList: 'Слова глав те же: изменились только порядок глав или курсив и жирный.',
  nothingTitle: 'В заметках нет изменений',
  nothingText: (book: string) => `Текст «${book}» совпадает с заметками папки. Обновлять нечего.`,
  close: 'Закрыть',
  /** «Сейчас не читаются 3 заметки (только в облаке — 2, не открылись — 1). …» */
  unread: (cloud: number, unreadable: number) => {
    const total = cloud + unreadable;
    const parts = [cloud ? `только в облаке — ${cloud}` : '', unreadable ? `не открылись — ${unreadable}` : ''].filter(Boolean).join(', ');
    const one = total === 1;
    return `Сейчас ${pluralize(total, 'не читается', 'не читаются', 'не читаются')} ${formatCount(total, 'заметка', 'заметки', 'заметок')} (${parts}). `
      + `Если обновить без ${one ? 'неё, её' : 'них, их'} главы уйдут из книги в «Недавно удалённые» — вернуть можно 7 дней.`;
  },
  readAgain: 'Прочитать заново',
  without: 'Обновить без этих заметок',
  notLinked: 'Книга больше не связана с папкой.',
  broken: (reason: string) => `Из заметок папки книга сейчас не собирается: ${reason}`,
  nothingToApply: 'В заметках нет изменений для книги',
  changedSince: 'Заметки изменились, пока вы смотрели.',
  queueBusy: 'В саге идёт загрузка книг — обновите книгу, когда она закончится.',
  working: (book: string) => `Обновляю «${book}» из Obsidian…`,
  done: (book: string, what: string) => `${book} обновлена из Obsidian: ${what}`,
  failed: (book: string) => `${book} не обновилась — повторите «Обновить из Obsidian…» в меню книги`,
  /** The failure of an update whose source the app does not know (it restarted while the update went): no file, no folder. */
  failedUnknown: (book: string) => `${book} не обновилась — повторите обновление в меню книги`,
  updated: 'Обновлена',
  revert: {
    addedOne: 'её добавило обновление',
    addedMany: (count: number) => `Главы, которые добавило обновление (${count})`,
    changedAll: 'Всё, что изменило обновление',
  },
  trashTitle: (title: string) => `Книга «${title}» обновлена`,
  updating: {
    mark: 'Обновляется',
    wait: 'Дождитесь, пока книга обновится.',
    already: 'Книга уже обновляется.',
    noDelete: 'Пока книга обновляется, удалить её нельзя.',
    noRevert: 'Книга сейчас обновляется — вернуть прежнюю версию можно будет после этого.',
  },
  edits: { made: 'Правки, сделанные в приложении', mark: 'есть правки в приложении' },
} as const;

/** «А», «Б»: names in quotes, by a comma. */
export function quoted(names: readonly string[]) {
  return names.map((name) => `«${name}»`).join(', ');
}

/** «12 заметок», «12 заметок и ещё 40 во вложенных», and a folder with notes only in its folders. */
export function notesLine(notes: number, below: number) {
  if (below === 0) return formatCount(notes, 'заметка', 'заметки', 'заметок');
  if (notes === 0) return `${formatCount(below, 'заметка', 'заметки', 'заметок')} во вложенных`;
  return `${formatCount(notes, 'заметка', 'заметки', 'заметок')} и ещё ${below.toLocaleString('ru-RU')} во вложенных`;
}

/** «Выбрано: 3 книги, 2 папки записей»; null when nothing is chosen — the reason of the button says it then. */
export function pickedLine(books: number, materials: number) {
  const parts = [
    books ? formatCount(books, 'книга', 'книги', 'книг') : null,
    materials ? `${materials.toLocaleString('ru-RU')} ${pluralize(materials, 'папка записей', 'папки записей', 'папок записей')}` : null,
  ].filter(Boolean);
  return parts.length ? `Выбрано: ${parts.join(', ')}` : null;
}

const decimal = new Intl.NumberFormat('ru-RU', { maximumFractionDigits: 1 });

/** «310 тыс. знаков», «1,2 млн знаков», «900 знаков». */
export function charactersWords(value: number) {
  if (value >= 1_000_000) return `${decimal.format(Math.round(value / 100_000) / 10)} млн знаков`;
  if (value >= 1_000) return `${decimal.format(Math.round(value / 1_000))} тыс. знаков`;
  return formatCount(value, 'знак', 'знака', 'знаков');
}

/** «24 главы · 312 тыс. знаков». */
export function bookMeta(chapters: number, characters: number) {
  return `${formatCount(chapters, 'глава', 'главы', 'глав')} · ${charactersWords(characters)}`;
}

/** The kinds of the records of a folder (OW2.3): the words of the bridge, «Записи о героях»… */
export const materialChoiceWords: Readonly<Record<Schema<'AuthorMaterialKind'>, string>> = {
  character: 'Записи о героях',
  place: 'Записи о местах',
  skill: 'Записи о навыках',
  item: 'Записи о предметах',
  faction: 'Записи о фракциях',
  other: 'Другие записи',
};

/** The order of the kinds in the choice of a folder (board 44·50). */
export const materialKindsInOrder: readonly Schema<'AuthorMaterialKind'>[] = ['character', 'place', 'skill', 'item', 'faction', 'other'];

/** Why an entry or a note was not read (OW2.8, OW3.2): every reason of `ObsidianSkipReason`. */
export const skipWords: Readonly<Record<Schema<'ObsidianSkipReason'>, string>> = {
  link: 'ссылки на другие папки и файлы',
  hidden: 'скрытые (имя с точки)',
  cloud_only: 'лежат только в облаке — в проводнике выберите у папки «Всегда сохранять на этом устройстве» и прочитайте папку заново',
  not_note: 'не заметки: картинки, PDF и другое',
  too_big: 'больше 10 МБ',
  service: 'служебные заметки плагинов и шаблоны',
  unreadable: 'не открылись: нет прав или файл занят',
  encoding: 'не в UTF-8 — пересохраните заметку в UTF-8',
  empty: 'пустые',
  no_name: 'без имени',
  long_name: 'слишком длинное имя',
};

/** «Пропущено при чтении: 41 файл» of the look; «… 2 заметки» of a preview and the windows. */
export function skippedTitle(count: number, of: 'files' | 'notes') {
  return `Пропущено при чтении: ${of === 'files' ? formatCount(count, 'файл', 'файла', 'файлов') : formatCount(count, 'заметка', 'заметки', 'заметок')}`;
}

/** Why the folder was not linked (OW1.3): every reason of `ObsidianChoiceReason`. */
export const choiceRefusalWords: Readonly<Record<Schema<'ObsidianChoiceReason'>, string>> = {
  data_dir: 'Эту папку выбрать нельзя: в ней лежат данные CanonKeeper. Выберите папку с заметками.',
  too_wide: 'Эта папка слишком общая — весь диск или домашняя папка. Выберите саму папку хранилища Obsidian.',
  not_found: 'Папка не найдена. Выберите её ещё раз.',
  not_folder: 'Это не папка. Выберите папку с заметками.',
  unreadable: 'Папку не удалось открыть: нет права на чтение. Проверьте права или выберите другую папку.',
};

/** The folder of the saga does not open now (OW1.5, OW2.8): every `vaultReason`. */
export const vaultReasonWords: Readonly<Record<'not_found' | 'not_folder' | 'unreadable', string>> = {
  not_found: words.missing,
  not_folder: words.notFolderNow,
  unreadable: words.unreadableNow,
};

/**
 * Why a folder does not give a book (OW3.2), by the code of the check of the import queue: the reasons said of a folder, not
 * of a file. A code the table does not know is «Книгу из этой папки собрать не удалось» (`cantOther`).
 */
export function refusalReason(code: string | undefined, bookLimitBytes: number | null): string | null {
  switch (code) {
    case 'empty_import_content': return 'в заметках папки не нашлось текста глав';
    case 'import.too_many_chapters': return 'получается больше 2000 глав — разделите папку на несколько книг';
    case 'import.too_large': return 'в заметках больше 10 миллионов знаков — разделите папку на несколько книг';
    case 'import.file_too_large': return `заметки папки вместе больше ${bookLimitBytes ? `${Math.round(bookLimitBytes / (1024 * 1024))} МБ` : 'предела книги'} — разделите папку`;
    case 'request.validation': return 'имя папки не годится для книги — переименуйте папку и покажите заново';
    default: return null;
  }
}

/** The name of a note by its path: the end of the path without «.md». */
export function noteName(path: string) {
  const name = path.slice(path.lastIndexOf('/') + 1);
  return name.toLowerCase().endsWith('.md') ? name.slice(0, -3) : name;
}

/** «до 48 глав, 310 тыс. знаков» of the word of the reading (OW3.5): the numbers of the server are «до». */
export function amountWords(chapters: number, characters: number) {
  const signs = characters >= 1_000 ? charactersWords(characters) : `${characters.toLocaleString('ru-RU')} ${pluralize(characters, 'знака', 'знаков', 'знаков')}`;
  return `до ${chapters.toLocaleString('ru-RU')} ${pluralize(chapters, 'главы', 'глав', 'глав')}, ${signs}`;
}

/** «Будет загружено: 3 книги; записи — 98 новых, 4 обновятся» (OW3.6), without the part there is none of. */
export function willWords(books: number, created: number, updated: number) {
  const records = [
    created ? `${created.toLocaleString('ru-RU')} ${pluralize(created, 'новая', 'новых', 'новых')}` : null,
    updated ? `${updated.toLocaleString('ru-RU')} ${pluralize(updated, 'обновится', 'обновятся', 'обновятся')}` : null,
  ].filter(Boolean).join(', ');
  const parts = [books ? formatCount(books, 'книга', 'книги', 'книг') : null, records ? `записи — ${records}` : null].filter(Boolean);
  return parts.length ? `Будет загружено: ${parts.join('; ')}` : null;
}

/** «Не вошло в текст: сноски — 3, …» (OW3.2): what did not go into the text, counted; null when everything went. */
export function droppedWords(dropped: Schema<'ObsidianDropped'>) {
  const parts = [
    ['сноски', dropped.footnotes], ['вставки других заметок', dropped.embeds], ['комментарии', dropped.comments + dropped.unpairedComments],
    ['блоки плагинов', dropped.pluginBlocks], ['таблицы', dropped.tables],
  ].filter(([, count]) => Number(count) > 0).map(([name, count]) => `${name} — ${Number(count).toLocaleString('ru-RU')}`);
  return parts.length ? `Не вошло в текст: ${parts.join(', ')}` : null;
}

/** «98 новых · 4 обновятся · 20 без изменений · 12 правлены в приложении · 5 с занятым именем» (OW3.3). */
export function recordCountsWords(counts: { created: number; updated: number; same: number; edited: number; taken: number }) {
  return [
    counts.created ? `${counts.created.toLocaleString('ru-RU')} ${pluralize(counts.created, 'новая', 'новых', 'новых')}` : null,
    counts.updated ? `${counts.updated.toLocaleString('ru-RU')} ${pluralize(counts.updated, 'обновится', 'обновятся', 'обновятся')}` : null,
    counts.same ? `${counts.same.toLocaleString('ru-RU')} без изменений` : null,
    counts.edited ? `${counts.edited.toLocaleString('ru-RU')} ${pluralize(counts.edited, 'правлена', 'правлены', 'правлены')} в приложении` : null,
    counts.taken ? `${counts.taken.toLocaleString('ru-RU')} с занятым именем` : null,
  ].filter(Boolean).join(' · ');
}

/** «Создано 98, обновлено 4, заменено заметкой 1, без изменений 20» (OW4.4); null when the load wrote and kept nothing. */
export function resultWords(created: number, updated: number, replaced: number, unchanged: number) {
  const parts = [
    created ? `создано ${created.toLocaleString('ru-RU')}` : null,
    updated ? `обновлено ${updated.toLocaleString('ru-RU')}` : null,
    replaced ? `заменено заметкой ${replaced.toLocaleString('ru-RU')}` : null,
    unchanged ? `без изменений ${unchanged.toLocaleString('ru-RU')}` : null,
  ].filter((part): part is string => part !== null);
  if (!parts.length) return null;
  const line = parts.join(', ');
  return line.charAt(0).toUpperCase() + line.slice(1);
}

/** The names of a group of the result, the first 50 and the rest by their count (OW4.4). */
export function namesWords(names: readonly string[], count: number) {
  return count > names.length ? `${names.join(', ')} и ещё ${(count - names.length).toLocaleString('ru-RU')}` : names.join(', ');
}

/** The line of the end of a load on another page (OW4.1): both parts, one of them, a part that failed. */
export function outcomeLine(records: { saved: number } | 'failed' | null, books: { loading: number } | 'failed' | null) {
  const recordsPart = records === 'failed' ? 'записи не сохранились' : records ? `записей сохранено — ${records.saved.toLocaleString('ru-RU')}` : null;
  const booksPart = books === 'failed' ? 'книги не загрузились' : books ? `книг загружается — ${books.loading.toLocaleString('ru-RU')}` : null;
  if (recordsPart && booksPart) return `Из Obsidian: ${recordsPart}${records === 'failed' || books === 'failed' ? '; ' : ', '}${booksPart}`;
  return `Из Obsidian: ${recordsPart ?? booksPart ?? ''}`;
}
