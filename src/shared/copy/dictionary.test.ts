import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import ts from 'typescript';
import { describe, expect, it } from 'vitest';
import { dictionary, hiddenIn, hiddenWords, readsAt } from './dictionary';

/**
 * The guard of the dictionary (specs/2026-11-11-ux2-dictionary.md DC2.1–DC2.4): every saying of the author — the strings
 * and the text of the markup of `src`, the letters of the site, the table of the pages of the voice and `index.html` — has
 * no word the table hides. The names of the code, the comments and the addresses are not sayings; a saying is a string
 * with a letter of the author's language in it. Only the places of the list below keep a hidden word, each for its reason.
 */

const root = process.cwd();

/**
 * Not the author's reading: the tests, the mock, the generated contract, the admin panel, the legal texts and the table
 * itself, which names the words it hides.
 */
const notTheAuthors = ['src/test/', 'src/api/mock/', 'src/features/admin/', 'src/entities/admin/', 'src/features/legal/content/'];
const theTable = 'src/shared/copy/dictionary.ts';

function sources(directory: string): string[] {
  return readdirSync(path.join(root, directory)).flatMap((name) => {
    const file = `${directory}/${name}`;
    if (statSync(path.join(root, file)).isDirectory()) return name === 'generated' || notTheAuthors.includes(`${file}/`) ? [] : sources(file);
    return /\.tsx?$/u.test(name) && !/\.test(?:-helpers)?\.tsx?$|\.d\.ts$/u.test(name) && file !== theTable ? [file] : [];
  });
}

/** The letters of the site: their templates live in the Go code of the backend (the open code has only some of them). */
const letters = [
  'backend/internal/account/mail_templates.go',
  'backend/internal/services/accountletters/letters.go',
  'backend/internal/services/accountletters/preference.go',
  'backend/internal/services/subscriptions/letters.go',
  'backend/internal/services/subscriptions/term_letters.go',
  'backend/internal/domain/subscriptions/letters.go',
  'backend/internal/domain/subscriptions/words.go',
  'backend/internal/services/feedback/letters.go',
  'backend/internal/services/promo/letters.go',
  // The line under the signature of every letter to an author (specs/2026-11-12-ux12-shell.md SH3.2).
  'backend/internal/domain/contacts/contacts.go',
];

/** The table of the pages of the voice: what the voice says of a page (the open code has no voice of the site). */
const pages = 'backend/internal/delivery/http/voice/pages.json';

/** The page the browser loads first: the name of the tab and what a link to the site says. */
const firstPage = 'index.html';

/**
 * What the desktop app says around its pages (specs/2026-11-12-desk-shell.md): the description of its package in the
 * installer and the software centre, the starting and failure page of the shell, and the words of a failure.
 */
const shell = {
  config: 'desktop/src-tauri/tauri.conf.json',
  page: 'desktop/app/index.html',
  words: 'desktop/src-tauri/crates/core/src/words.rs',
};

/** A letter of the author's language, the sign of a paragraph or the name of the product: what makes a string a saying. */
const authors = /[А-Яа-яЁё¶]|Canon\s*Keeper/iu;

/** A run of text and markup inside an element: the words around an expression are one saying. */
const between = ' ⟨⟩ ';

/** The sayings of a source of `src`: its strings, the parts of its templates and the text of its markup. */
function sayingsOfSource(text: string, file: string): string[] {
  const source = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, file.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
  const sayings: string[] = [];
  const visit = (node: ts.Node) => {
    if (ts.isImportDeclaration(node) || ts.isExportDeclaration(node) || ts.isLiteralTypeNode(node)) return;
    if (ts.isStringLiteralLike(node) || ts.isTemplateLiteralToken(node)) sayings.push(node.text);
    if (ts.isJsxElement(node) || ts.isJsxFragment(node)) {
      const run = node.children.map((child) => (ts.isJsxText(child) ? child.text.replace(/\s+/gu, ' ') : between)).join('').trim();
      if (run.replaceAll(between.trim(), '').trim()) sayings.push(run);
    }
    ts.forEachChild(node, visit);
  };
  visit(source);
  return sayings.filter((saying) => authors.test(saying));
}

/** The strings of a Go source, without its comments. */
function sayingsOfGo(text: string): string[] {
  const sayings: string[] = [];
  for (let at = 0; at < text.length; at += 1) {
    const sign = text[at];
    if (sign === '/' && text[at + 1] === '/') at = text.indexOf('\n', at) === -1 ? text.length : text.indexOf('\n', at);
    else if (sign === '/' && text[at + 1] === '*') at = text.indexOf('*/', at + 2) + 1;
    else if (sign === '`') {
      const end = text.indexOf('`', at + 1);
      sayings.push(text.slice(at + 1, end));
      at = end;
    } else if (sign === '"' || sign === "'") {
      let end = at + 1;
      while (end < text.length && text[end] !== sign) end += text[end] === '\\' ? 2 : 1;
      if (sign === '"') sayings.push(text.slice(at + 1, end));
      at = end;
    }
  }
  return sayings.filter((saying) => authors.test(saying));
}

interface Page { path: string; title: string; purpose: string; words?: string }

/** What the voice says of the pages of the author: the name and the purpose; `words` are what the author may say. */
function sayingsOfPages(text: string): string[] {
  return (JSON.parse(text) as Page[]).filter((page) => !page.path.startsWith('/admin')).flatMap((page) => [page.title, page.purpose]);
}

/** What the first page says before the product loads: its title and the descriptions of a link to it. */
function sayingsOfHtml(text: string): string[] {
  return [...text.matchAll(/<title>([^<]*)<\/title>|\scontent="([^"]*)"/gu)].map((match) => match[1] ?? match[2] ?? '').filter((saying) => authors.test(saying));
}

/** What the package of the desktop app says of itself: its name and its two descriptions. */
function sayingsOfShellConfig(text: string): string[] {
  const config = JSON.parse(text) as { productName?: string; bundle?: { shortDescription?: string; longDescription?: string } };
  return [config.productName, config.bundle?.shortDescription, config.bundle?.longDescription]
    .filter((saying): saying is string => typeof saying === 'string' && authors.test(saying));
}

/** The text of a page of markup and its title: every run between tags. */
function sayingsOfMarkup(text: string): string[] {
  return text.replace(/<!--[\s\S]*?-->/gu, '').split(/<[^>]*>/u).map((run) => run.replace(/\s+/gu, ' ').trim()).filter((saying) => authors.test(saying));
}

/** The strings of the Rust source of the words of a failure, without its tests (the comments are left as in Go). */
function sayingsOfShellWords(text: string): string[] {
  return sayingsOfGo(text.split('#[cfg(test)]')[0] ?? '');
}

function shellOffences(): Offence[] {
  const read = (file: string) => (existsSync(path.join(root, file)) ? readFileSync(path.join(root, file), 'utf8') : null);
  const [config, page, words] = [read(shell.config), read(shell.page), read(shell.words)];
  return [
    ...(config === null ? [] : offences(shell.config, sayingsOfShellConfig(config))),
    ...(page === null ? [] : offences(shell.page, sayingsOfMarkup(page))),
    ...(words === null ? [] : offences(shell.words, sayingsOfShellWords(words))),
  ];
}

interface Offence { file: string; says: string; saying: string }

function offences(file: string, sayings: readonly string[]): Offence[] {
  return sayings.flatMap((saying) => hiddenIn(saying, file).map((word) => ({ file, says: word.says, saying })));
}

function everyOffence(): Offence[] {
  return [
    ...sources('src').flatMap((file) => offences(file, sayingsOfSource(readFileSync(path.join(root, file), 'utf8'), file))),
    ...letters.filter((file) => existsSync(path.join(root, file))).flatMap((file) => offences(file, sayingsOfGo(readFileSync(path.join(root, file), 'utf8')))),
    ...(existsSync(path.join(root, pages)) ? offences(pages, sayingsOfPages(readFileSync(path.join(root, pages), 'utf8'))) : []),
    ...offences(firstPage, sayingsOfHtml(readFileSync(path.join(root, firstPage), 'utf8'))),
    ...shellOffences(),
  ];
}

/**
 * A place that keeps a hidden word, and why (DC2.2): the path, the word as the table says it, the reason, and — when the
 * file has other sayings with the same word — a part of the saying it keeps. The list only shrinks.
 */
interface Exception { file: string; says: string; reason: string; saying?: string }

const exceptions: readonly Exception[] = [
  // Shown only while an administrator starts or ends a sign-in as an author (specs/2026-10-28-writer-first-success.md R5.5).
  { file: 'src/editions/site/account.tsx', says: '«рабочее место», «рабочая область»', reason: 'подсказка администратору: видна только при входе от имени автора и выходе из него' },
  // The progress of a picture the author is uploading into a letter, with its per cent: an action going on, not a page.
  { file: 'src/entities/feedback/ui/PostEditor.tsx', says: '«Загружаем», «Загрузка…», «Проверяем»', saying: 'Загружаем картинку', reason: 'выгрузка картинки письма с процентом — ход действия автора, а не загрузка страницы (PS3.1)' },
  // The consent to an own picture, in the words of the documents the author agreed to (they change only with their edition).
  { file: 'src/entities/illustrations/ui/ImageAttachments.tsx', says: '«ИИ»', saying: 'передано модели ИИ', reason: 'цитата документа: согласие на свою картинку, передача поставщику модели ИИ (согласие, п. 7)' },
  { file: 'src/entities/illustrations/ui/ImageAttachments.tsx', says: '«модель»', saying: 'передано модели ИИ', reason: 'цитата документа: согласие на свою картинку, передача поставщику модели ИИ (согласие, п. 7)' },
  // The permission to show the saga in the stories of the service says what it covers in the words the author agreed to.
  { file: 'src/entities/promo-permissions/ui/PermissionView.tsx', says: '«фрагмент» как адрес', saying: 'фрагменты текста', reason: 'слово разрешения на продвижение, на которое автор согласился: кусок текста саги, а не адрес места (domain/promo/terms.go)' },
  // The scope of a permission the author grants, in the words he agreed to (specs/2026-10-30-promo-permissions.md).
  { file: 'src/entities/promo-permissions/ui/PermissionView.tsx', says: '«Галерея»', saying: 'все рисунки галереи саги', reason: 'объём разрешения на продвижение в словах, на которые автор согласился' },
  // The notice of the voice quotes the consent: the sound goes to the provider of a model, nothing is kept.
  { file: 'src/entities/voice/ui/VoicePanel.tsx', says: '«ИИ»', saying: 'Голос передаётся модели ИИ', reason: 'раскрытие словами согласия: звук разговора уходит поставщику модели ИИ, запись не хранится (согласие, п. 4)' },
  { file: 'src/entities/voice/ui/VoicePanel.tsx', says: '«модель»', saying: 'Голос передаётся модели ИИ', reason: 'раскрытие словами согласия: звук разговора уходит поставщику модели ИИ, запись не хранится (согласие, п. 4)' },
  { file: 'src/features/auth/AdminImpersonation.tsx', says: '«рабочее место», «рабочая область»', reason: 'подсказка администратору: видна только при входе от имени автора' },
  // The page of a reset link before the product (specs/2026-11-10-ux3-auth.md): the server checks the link.
  { file: 'src/features/auth/PasswordResetForm.tsx', says: '«Загружаем», «Загрузка…», «Проверяем»', saying: 'Проверяем ссылку', reason: 'страница ссылки до входа в продукт — сервер проверяет ссылку, формы содержимого нет (ux3-auth)' },
  // The message the page sends the helper for the first description: words for the model, never shown.
  { file: 'src/entities/drawings/prepare-message.ts', says: '«художник» как действующее лицо', saying: 'описание для художника', reason: 'сообщение помощнику для первого описания рисунка: слова для модели, автор их не видит' },
  // The disclosures of the guest page quote the documents: who prepares the answers and where the text goes.
  { file: 'src/features/landing/landing-copy.ts', says: '«ИИ»', saying: 'сервис передаёт модели ИИ', reason: 'раскрытие гостю словами политики: текст для ответа уходит поставщику модели ИИ (политика, передача данных)' },
  { file: 'src/features/landing/landing-copy.ts', says: '«модель»', saying: 'сервис передаёт модели ИИ', reason: 'раскрытие гостю словами политики: текст для ответа уходит поставщику модели ИИ (политика, передача данных)' },
  { file: 'src/features/landing/landing-copy.ts', says: '«фрагмент» как адрес', saying: 'только нужные фрагменты', reason: 'раскрытие гостю словами политики: текст для ответа уходит поставщику модели ИИ (политика, передача данных)' },
  { file: 'src/features/landing/landing-copy.ts', says: '«ИИ»', saying: 'Ответы готовит модель ИИ', reason: 'раскрытие гостю словами соглашения: ответы готовит модель ИИ и она может ошибаться (соглашение, разд. 4)' },
  { file: 'src/features/landing/landing-copy.ts', says: '«модель»', saying: 'Ответы готовит модель ИИ', reason: 'раскрытие гостю словами соглашения: ответы готовит модель ИИ и она может ошибаться (соглашение, разд. 4)' },
  { file: 'src/features/promo/PromoNotice.tsx', says: '«фрагмент» как адрес', saying: 'фрагменты вашей саги', reason: 'слово разрешения на продвижение, на которое автор согласился: кусок текста саги, а не адрес места (domain/promo/terms.go)' },
  // A value of the data, never shown: the type the cards of the server give a name, which the page compares.
  { file: 'src/features/saga/illustrations/EntryLikeness.tsx', says: '«персонаж» как общее слово списка', saying: 'персонаж', reason: 'тип имени из данных сервера, с которым страница сравнивает запись; автор его не читает' },
  // decision-sheet.ts holds the one rule that leaves these words of the model out of the sheet: its patterns name them.
  { file: 'src/features/writing/decision-sheet.ts', says: '«нити — продвигает»', reason: 'образец слов модели, которые одно правило листа убирает с листа (DSH1.4)' },
  { file: 'src/features/writing/decision-sheet.ts', says: '«нити и арки»', reason: 'образец слов модели, которые одно правило листа убирает с листа (DSH1.4)' },
  { file: 'src/features/writing/decision-sheet.ts', says: '«место главы»', reason: 'образец слов модели, которые одно правило листа убирает с листа (DSH1.4)' },
  { file: 'backend/internal/services/promo/letters.go', says: '«фрагмент» как адрес', saying: 'фрагменты текста', reason: 'слово разрешения на продвижение, на которое автор согласился: кусок текста саги, а не адрес места (domain/promo/terms.go)' },
  { file: 'backend/internal/services/promo/letters.go', says: '«Галерея»', saying: 'все рисунки галереи саги', reason: 'объём разрешения на продвижение в словах, на которые автор согласился' },
  { file: 'backend/internal/delivery/http/voice/pages.json', says: '«фрагмент» как адрес', saying: 'фрагменты саги', reason: 'слово разрешения на продвижение, на которое автор согласился: кусок текста саги, а не адрес места (domain/promo/terms.go)' },
];

function excuses(exception: Exception, offence: Offence) {
  return exception.file === offence.file && exception.says === offence.says && (!exception.saying || offence.saying.includes(exception.saying));
}

describe('the table of words (DC1.1)', () => {
  it('has a thing, a word and the words it hides in every row', () => {
    expect(dictionary.length).toBeGreaterThanOrEqual(29);
    for (const row of dictionary) {
      expect(row.thing.trim(), row.thing).not.toBe('');
      expect(row.word.trim(), row.thing).not.toBe('');
      expect(row.hidden.length, row.thing).toBeGreaterThan(0);
    }
  });

  it.each(hiddenWords.map((word) => [word.says, word] as const))('finds %s where it stands and nowhere else', (_says, word) => {
    expect(word.pattern.flags).not.toMatch(/[gy]/u);
    expect(word.pattern.test(word.sample), word.sample).toBe(true);
    for (const allowed of word.allowed ?? []) expect(word.pattern.test(allowed), allowed).toBe(false);
  });
});

/** Every section of `src` a hidden word could come back into, and a source of its own in each. */
function sections() {
  const groups = ['src/features', 'src/entities', 'src/ui', 'src/app', 'src/shared', 'src/editions', 'src/pages'];
  return groups.filter((group) => existsSync(path.join(root, group))).flatMap((group) => readdirSync(path.join(root, group))
    .filter((name) => statSync(path.join(root, group, name)).isDirectory() && !notTheAuthors.includes(`${group}/${name}/`))
    .map((name) => `${group}/${name}/Returned.tsx`));
}

describe('the guard of the dictionary (DC2.1, DC2.4)', () => {
  it('finds a hidden word returned into any section, a letter, the table of the pages and the first page', () => {
    const places = [...sections(), ...writingAndDrawings()];
    expect(places.length).toBeGreaterThan(30);
    for (const word of hiddenWords) {
      for (const file of places.filter((place) => readsAt(word, place))) {
        const sayings = [
          ...sayingsOfSource(`export const Returned = () => <p title=${JSON.stringify(word.sample)}>${word.sample}</p>;`, file),
          ...sayingsOfSource(`export const returned = \`\${0}${word.sample}\`;`, file),
        ];
        expect(offences(file, sayings).filter((offence) => offence.says === word.says), `${file}: ${word.says}`).toHaveLength(3);
      }
      if (!word.where) {
        expect(offences(letters[0]!, sayingsOfGo(`// ${word.sample}\nconst body = "${word.sample}"`)).map((offence) => offence.says)).toContain(word.says);
        const page = JSON.stringify([{ path: '/x', title: 'x', purpose: word.sample, words: word.sample }]);
        expect(offences(pages, sayingsOfPages(page)).filter((offence) => offence.says === word.says)).toHaveLength(1);
        const html = `<title>${word.sample}</title><meta name="description" content="${word.sample}">`;
        expect(offences(firstPage, sayingsOfHtml(html)).filter((offence) => offence.says === word.says)).toHaveLength(2);
        const config = JSON.stringify({ productName: 'CanonKeeper', bundle: { shortDescription: word.sample, longDescription: word.sample } });
        expect(offences(shell.config, sayingsOfShellConfig(config)).filter((offence) => offence.says === word.says)).toHaveLength(2);
        const markup = `<title>${word.sample}</title><p>${word.sample}</p>`;
        expect(offences(shell.page, sayingsOfMarkup(markup)).filter((offence) => offence.says === word.says)).toHaveLength(2);
        const rust = `fn said() -> String { format!("${word.sample}") }\n#[cfg(test)]\nmod tests { const X: &str = "${word.sample}"; }`;
        expect(offences(shell.words, sayingsOfShellWords(rust)).filter((offence) => offence.says === word.says)).toHaveLength(1);
      }
    }
  });

  it('reads no comment, no name of the code and no words the author may say to the voice', () => {
    expect(sayingsOfSource('// Подробнее на полке\n/** очередь */ const queue = "queue"; type K = \'проект\';', 'src/x.ts')).toEqual([]);
    expect(sayingsOfGo('// очередь\nconst queue = "queue" // проект')).toEqual([]);
    expect(sayingsOfPages(JSON.stringify([{ path: '/x', title: 'Книги', purpose: 'Книги саги', words: 'полка импорт' }]))).toEqual(['Книги', 'Книги саги']);
    expect(sayingsOfMarkup('<!-- очередь --><main class="проект"><p>Книги</p></main>')).toEqual(['Книги']);
    expect(sayingsOfShellWords('// очередь\nfn x() -> String { "Книги".into() }\n#[cfg(test)]\nconst X: &str = "проект";')).toEqual(['Книги']);
  });

  it('leaves no hidden word in a saying of the author but the places of the list', () => {
    const found = everyOffence();
    const unexcused = found.filter((offence) => !exceptions.some((exception) => excuses(exception, offence)));
    expect(unexcused.map(({ file, says, saying }) => `${file}: ${says} — «${saying.slice(0, 160)}»`)).toEqual([]);
  });

  it('keeps an exception only with its reason and only while its place has the word', () => {
    const found = everyOffence();
    // «до правки» was the reason of the list before the sections came to the table (checklist step 1); it is no reason now.
    expect(exceptions.filter((exception) => !exception.reason.trim() || exception.reason === 'до правки').map(({ file, says }) => `${file}: ${says}`)).toEqual([]);
    // The open code has no closed files of the site: an exception counts where its file is.
    const stale = exceptions.filter((exception) => existsSync(path.join(root, exception.file)) && !found.some((offence) => excuses(exception, offence)));
    expect(stale.map(({ file, says, saying }) => `${file}: ${says}${saying ? ` — «${saying}»` : ''}`)).toEqual([]);
    expect(hiddenWords.map((word) => word.says)).toEqual(expect.arrayContaining(exceptions.map((exception) => exception.says)));
  });
});

/** A place in each section a scoped word is read in. */
function writingAndDrawings() {
  return [...new Set(hiddenWords.flatMap((word) => word.where ?? []))].map((place) => `${place}Returned.tsx`);
}
