type ProjectCounts = {
  bookCount: number;
  chapterCount: number;
  wordCount: number;
};

const integerFormatter = new Intl.NumberFormat('ru-RU');
const compactFormatter = new Intl.NumberFormat('ru-RU', { maximumFractionDigits: 1 });

export function formatBookLabel(book: { displayNumber?: string | null; title: string }): string {
  return book.displayNumber ? `Книга ${book.displayNumber}` : book.title;
}

export function formatReaderChapterTitle(displayNumber: string, title: string) {
  const ordinal = `Глава ${displayNumber}.`;
  const prefix = `${ordinal} `;
  if (title === ordinal) return '';
  return title.startsWith(prefix) ? title.slice(prefix.length) : title;
}

/** The counts of a saga; nothing before the saga is known (no saga, a load or its error). */
export function formatProjectMeta(project: ProjectCounts | null | undefined): string {
  if (!project) {
    return '';
  }
  return [
    formatCount(project.bookCount, 'книга', 'книги', 'книг'),
    formatCount(project.chapterCount, 'глава', 'главы', 'глав'),
    formatWordCount(project.wordCount),
  ].join(' · ');
}

export function formatWordCount(value: number): string {
  if (value >= 1_000_000) {
    return `${compactFormatter.format(value / 1_000_000)} млн слов`;
  }
  if (value >= 1_000) {
    return `${compactFormatter.format(value / 1_000)} тыс. слов`;
  }
  return formatCount(value, 'слово', 'слова', 'слов');
}

/** «1 глава», «2 главы», «5 глав»: the number and the noun agreeing with it. */
export function formatCount(value: number, one: string, few: string, many: string): string {
  return `${integerFormatter.format(value)} ${pluralize(value, one, few, many)}`;
}

const sizeFormatter = new Intl.NumberFormat('ru-RU', { maximumFractionDigits: 1, minimumFractionDigits: 1 });

/** A file size in megabytes with a decimal comma: «1,2 МБ». */
export function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} Б`;
  return `${sizeFormatter.format(bytes / 1024 / 1024)} МБ`;
}

const timeFormatter = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit' });
const dayFormatter = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'short' });

export function formatRelativeTime(value: string | number | Date | null | undefined, now: number = Date.now()): string {
  if (value === null || value === undefined) {
    return '';
  }
  const timestamp = value instanceof Date ? value.getTime() : new Date(value).getTime();
  if (Number.isNaN(timestamp)) {
    return '';
  }
  const diffSeconds = Math.round((now - timestamp) / 1000);
  if (diffSeconds < 45) {
    return 'только что';
  }
  const diffMinutes = Math.round(diffSeconds / 60);
  if (diffMinutes < 60) {
    return `${formatCount(diffMinutes, 'минуту', 'минуты', 'минут')} назад`;
  }
  if (diffMinutes < 24 * 60) {
    return `${formatCount(Math.round(diffMinutes / 60), 'час', 'часа', 'часов')} назад`;
  }
  // Older than a day: «вчера в 14:20», «12 окт. в 14:20», another year — «12 окт. 2025».
  const date = new Date(timestamp);
  const today = new Date(now);
  const yesterday = new Date(today.getFullYear(), today.getMonth(), today.getDate() - 1);
  if (date.getTime() >= yesterday.getTime()) {
    return `вчера в ${timeFormatter.format(date)}`;
  }
  if (date.getFullYear() !== today.getFullYear()) {
    return `${dayFormatter.format(date)} ${date.getFullYear()}`;
  }
  return `${dayFormatter.format(date)} в ${timeFormatter.format(date)}`;
}

export function pluralize(value: number, one: string, few: string, many: string): string {
  const lastTwo = Math.abs(value) % 100;
  const lastOne = lastTwo % 10;
  if (lastTwo >= 11 && lastTwo <= 14) {
    return many;
  }
  if (lastOne === 1) {
    return one;
  }
  if (lastOne >= 2 && lastOne <= 4) {
    return few;
  }
  return many;
}
