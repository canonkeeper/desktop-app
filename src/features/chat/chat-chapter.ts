import { useEffect, useState } from 'react';
import { foreignFileOf, foreignFileWords } from '../../shared/text/foreign-file';
import { formatCount } from '../../ui/copy';
import { forgetChapterDraft, keepChapterDraft, readChapterDraft, storedChapterDraft, type ChatChapter } from './chapter-draft';
import { chapterFileWords, roundWords, wordsOf } from './chapter-words';

/**
 * The chapter of the author attached to a question of the chat while he writes it
 * (specs/2026-11-07-chat-attach-chapter.md CA7): a form draft of the field, not server state — the server reads and keeps
 * it only when the question goes. It is kept in this browser by chat until then, as the question is
 * (specs/2026-11-11-ux4-after-answer.md AA2.4).
 */

/** The limits the server holds (CA0.1): a file of 10 MB, a chapter of 400 000 signs, a question of 8 000. */
export const chapterFileLimitBytes = 10 * 1024 * 1024;
export const chapterTextLimit = 400_000;
export const questionLimit = 8_000;

/** What the window of the browser offers: the files the reader of a chapter knows. */
export const chatChapterAccept = '.docx,.txt,.md,.markdown,application/vnd.openxmlformats-officedocument.wordprocessingml.document,text/plain,text/markdown';

const known = /\.(docx|txt|md|markdown)$/iu;

export type { ChatChapter };

/** Why a file cannot be a chapter; `books` — it is a book, and «Книги» is where it goes (AA4.3). */
export interface ChapterProblem {
  text: string;
  books?: boolean;
}

/** Signs as the server counts them: code points, not UTF-16 units. */
export const signs = (text: string) => [...text].length;

/** What the strip at the field and the card say of a chapter: its name and its size in words — «≈ 2 400 слов». */
export function chapterLabel(chapter: ChatChapter) {
  return chapter.name ? `«${chapter.name}»` : 'Вставленный текст';
}

export function chapterSize(chapter: ChatChapter) {
  if (chapter.words === undefined) return chapter.name ? 'считаю слова…' : '';
  return `≈ ${formatCount(roundWords(chapter.words), 'слово', 'слова', 'слов')}`;
}

/** A file of the author, or why it cannot be a chapter: a book, a PDF and a picture in the words of the materials (AA4.3). */
export function chapterFromFile(file: File): ChatChapter | ChapterProblem {
  const foreign = foreignFileOf(file.name);
  if (foreign) return { text: foreignFileWords[foreign], ...(foreign === 'book' ? { books: true } : {}) };
  if (!known.test(file.name)) return { text: 'Главу можно приложить файлом .docx, .txt или .md.' };
  if (file.size > chapterFileLimitBytes) return { text: 'Файл больше 10 МБ. Сохраните главу без картинок или как .txt и приложите снова.' };
  return { input: { file }, name: file.name, key: `file:${file.name}:${file.size}:${file.lastModified}` };
}

/** A pasted text, or why it cannot be a chapter. */
export function chapterFromText(text: string): ChatChapter | ChapterProblem {
  if (signs(text) > chapterTextLimit) return { text: 'Это больше главы: приложить можно до 400 000 знаков. Разделите текст на главы.' };
  return { input: { text }, words: wordsOf(text), key: `text:${signs(text)}:${hash(text)}` };
}

function hash(text: string) {
  let value = 0;
  for (let index = 0; index < text.length; index += 1) value = (Math.imul(value, 31) + text.charCodeAt(index)) | 0;
  return (value >>> 0).toString(36);
}

/** A second chapter takes the place of the first: a question takes one (AA4.5). */
export const replacedChapter = 'Заменил приложенную главу: к вопросу идёт одна.';

/**
 * The attachment of the field of the chat `slot` (the place of the question draft): a file or a text, the paste waiting
 * for the author's answer, why a file was refused and that a second one took the place of the first.
 */
export function useChatChapter(slot: string) {
  const [state, setState] = useState(() => ({ slot, chapter: storedChapterDraft(slot) }));
  const [paste, setPaste] = useState<string | null>(null);
  const [problem, setProblem] = useState<ChapterProblem | null>(null);
  const [notice, setNotice] = useState('');
  // Another chat: its own chapter stands at the field, kept until its question goes (AA2.4, CA7).
  if (state.slot !== slot) {
    setState({ slot, chapter: storedChapterDraft(slot) });
    setPaste(null);
    setProblem(null);
    setNotice('');
  }
  const chapter = state.slot === slot ? state.chapter : storedChapterDraft(slot);
  // After a reload the chapter comes back from the storage of the browser.
  useEffect(() => {
    let live = true;
    void readChapterDraft(slot).then((stored) => {
      if (live && stored) setState((current) => current.slot === slot && !current.chapter ? { slot, chapter: stored } : current);
    });
    return () => { live = false; };
  }, [slot]);

  const keep = (next: ChatChapter | null) => {
    setState({ slot, chapter: next });
    if (next) keepChapterDraft(slot, next);
    else forgetChapterDraft(slot);
  };
  const take = (next: ChatChapter | ChapterProblem) => {
    if (!('key' in next)) {
      setProblem(next);
      return;
    }
    setProblem(null);
    setNotice(chapter && chapter.key !== next.key ? replacedChapter : '');
    keep(next);
    if (next.words === undefined && 'file' in next.input && next.input.file) {
      const file = next.input.file;
      void chapterFileWords(file).then((words) => {
        if (words === undefined) return;
        setState((current) => {
          if (current.slot !== slot || current.chapter?.key !== next.key) return current;
          const counted = { ...current.chapter, words };
          keepChapterDraft(slot, counted);
          return { slot, chapter: counted };
        });
      });
    }
  };
  return {
    chapter,
    paste,
    problem,
    notice,
    attachFile: (file: File) => take(chapterFromFile(file)),
    /** A paste the field cannot hold waits for «Приложить главу» or «Не вставлять» (CA7.2). */
    offerPaste: (text: string) => {
      setPaste(text);
      setProblem(null);
    },
    acceptPaste: () => {
      if (paste !== null) take(chapterFromText(paste));
      setPaste(null);
    },
    dismissPaste: () => setPaste(null),
    remove: () => {
      keep(null);
      setProblem(null);
      setNotice('');
    },
    /** The first question got its chat: its chapter is the chapter of that chat now (CH1.4). */
    moved: (nextSlot: string) => {
      if (!chapter) return;
      keepChapterDraft(nextSlot, chapter);
      forgetChapterDraft(slot);
    },
    /** The question went: the field forgets its chapter. */
    sent: (key: string) => {
      setState((current) => {
        if (current.chapter?.key !== key) return current;
        forgetChapterDraft(current.slot);
        return { slot: current.slot, chapter: null };
      });
      setNotice('');
    },
  };
}

export type ChatChapterState = ReturnType<typeof useChatChapter>;
