import { useState } from 'react';
import type { ChapterFileInput } from '../../entities/chat/api';
import { formatCount, formatFileSize } from '../../ui/copy';

/**
 * The chapter of the author attached to a question of the chat while he writes it
 * (specs/2026-11-07-chat-attach-chapter.md CA7): a form draft of the field, not server state — the server reads and keeps
 * it only when the question goes.
 */

/** The limits the server holds (CA0.1): a file of 10 MB, a chapter of 400 000 signs, a question of 8 000. */
export const chapterFileLimitBytes = 10 * 1024 * 1024;
export const chapterTextLimit = 400_000;
export const questionLimit = 8_000;

/** What the window of the browser offers: the files the reader of a chapter knows. */
export const chatChapterAccept = '.docx,.txt,.md,.markdown,application/vnd.openxmlformats-officedocument.wordprocessingml.document,text/plain,text/markdown';

const known = /\.(docx|txt|md|markdown)$/iu;

export interface ChatChapter {
  input: ChapterFileInput;
  /** What the strip at the field says of it. */
  label: string;
  /** The intent: the same file or text again is the same request until it went through (CA7.5). */
  key: string;
}

/** Signs as the server counts them: code points, not UTF-16 units. */
export const signs = (text: string) => [...text].length;

const wordsOf = (text: string) => text.split(/[^\p{L}\p{N}\p{M}]+/u).filter(Boolean).length;

/** A file of the author, or why it cannot be a chapter. */
export function chapterFromFile(file: File): ChatChapter | string {
  if (!known.test(file.name)) return 'Главу можно приложить файлом .docx, .txt или .md.';
  if (file.size > chapterFileLimitBytes) return 'Файл больше 10 МБ. Сохраните главу без картинок или как .txt и приложите снова.';
  return { input: { file }, label: `«${file.name}» · ${formatFileSize(file.size)}`, key: `file:${file.name}:${file.size}:${file.lastModified}` };
}

/** A pasted text, or why it cannot be a chapter. */
export function chapterFromText(text: string): ChatChapter | string {
  if (signs(text) > chapterTextLimit) return 'Это больше главы: приложить можно до 400 000 знаков. Разделите текст на главы.';
  return { input: { text }, label: `Вставленный текст · ${formatCount(wordsOf(text), 'слово', 'слова', 'слов')}`, key: `text:${signs(text)}:${hash(text)}` };
}

function hash(text: string) {
  let value = 0;
  for (let index = 0; index < text.length; index += 1) value = (Math.imul(value, 31) + text.charCodeAt(index)) | 0;
  return (value >>> 0).toString(36);
}

/** The attachment of the field: a file or a text, the paste waiting for the author's answer and why a file was refused. */
export function useChatChapter() {
  const [chapter, setChapter] = useState<ChatChapter | null>(null);
  const [paste, setPaste] = useState<string | null>(null);
  const [problem, setProblem] = useState('');
  const take = (next: ChatChapter | string) => {
    if (typeof next === 'string') {
      setProblem(next);
      return;
    }
    setProblem('');
    setChapter(next);
  };
  return {
    chapter,
    paste,
    problem,
    attachFile: (file: File) => take(chapterFromFile(file)),
    /** A paste the field cannot hold waits for «Приложить главу» or «Не вставлять» (CA7.2). */
    offerPaste: (text: string) => {
      setPaste(text);
      setProblem('');
    },
    acceptPaste: () => {
      if (paste !== null) take(chapterFromText(paste));
      setPaste(null);
    },
    dismissPaste: () => setPaste(null),
    remove: () => {
      setChapter(null);
      setProblem('');
    },
    /** The question went: the field forgets its chapter. */
    sent: (key: string) => setChapter((current) => current?.key === key ? null : current),
  };
}

export type ChatChapterState = ReturnType<typeof useChatChapter>;
