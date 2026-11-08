import { ChevronDown, ChevronRight } from 'lucide-react';
import { useState } from 'react';
import type { ProjectJob } from '../../../entities/manuscript/api';
import { formatCount, pluralize } from '../../../ui/copy';
import styles from './BookUpdatePreview.module.css';

type ImportResult = Extract<NonNullable<ProjectJob['result']>, { type: 'import' }>;
export type BookUpdatePreviewResult = ImportResult & { preview: NonNullable<ImportResult['preview']> };
type PreviewChapter = BookUpdatePreviewResult['preview']['chapters'][number];

/** A preview names a change when a chapter changes, comes or goes (specs/2026-10-27-update-book-from-file.md, R6.5). */
export function previewHasChanges(result: BookUpdatePreviewResult) {
  return result.preview.chapters.length > 0 || (result.preview.moreChapters ?? 0) > 0;
}

/**
 * «Что изменится»: the counters of the plan, the changed chapters with their places «было → стало» on a press, and
 * how long the work after the update takes (R6.5).
 */
export function BookUpdatePreview({ result }: { result: BookUpdatePreviewResult }) {
  const { preview } = result;
  const words = result.changedChapters ?? 0;
  const punctuation = result.punctuationChapters ?? 0;
  const renamed = result.renamedChapters ?? 0;
  const added = result.addedChapters ?? 0;
  const removed = result.removedChapters ?? 0;
  const unchanged = result.unchangedChapters ?? 0;
  const lines = [
    words > 0 ? `Слова изменятся — в ${words} ${pluralize(words, 'главе', 'главах', 'главах')}` : '',
    punctuation > 0 ? `Только знаки, регистр или «ё» — ${formatCount(punctuation, 'глава', 'главы', 'глав')}, карточки останутся` : '',
    renamed > 0 ? `${pluralize(renamed, 'Переименуется', 'Переименуются', 'Переименуются')} — ${renamed}` : '',
    added > 0 ? `${pluralize(added, 'Добавится', 'Добавятся', 'Добавятся')} — ${added}` : '',
    removed > 0 ? `${pluralize(removed, 'Уйдёт', 'Уйдут', 'Уйдут')} в «Недавно удалённые» — ${removed}, вернуть можно 7 дней` : '',
    unchanged > 0 ? `Без изменений — ${unchanged}` : '',
  ].filter(Boolean);
  return (
    <>
      <ul aria-label="Что изменится" className={styles.summary}>
        {lines.map((line) => <li key={line}>{line}</li>)}
      </ul>
      <section aria-label="Главы с изменениями" className={styles.chapters}>
        <h3 className={styles.label}>Главы с изменениями</h3>
        <ul className={styles.chapterList}>
          {preview.chapters.map((chapter) => <PreviewChapterRow chapter={chapter} key={`${chapter.change}-${chapter.order}-${chapter.title}`} />)}
        </ul>
        {preview.moreChapters ? (
          <p className={styles.more}>И ещё {formatCount(preview.moreChapters, 'глава', 'главы', 'глав')} с изменениями.</p>
        ) : null}
      </section>
      <p className={styles.estimate}>{estimate(preview.newCards, preview.threads, removed)}</p>
    </>
  );
}

function PreviewChapterRow({ chapter }: { chapter: PreviewChapter }) {
  const [open, setOpen] = useState(false);
  const label = `${chapter.order}. ${chapter.title} — ${changeLabel(chapter)}`;
  if (!chapter.places?.length) {
    return <li className={styles.chapter}>{label}</li>;
  }
  return (
    <li className={styles.chapter}>
      <button aria-expanded={open} className={styles.toggle} onClick={() => setOpen((value) => !value)} type="button">
        {open ? <ChevronDown aria-hidden="true" size={16} /> : <ChevronRight aria-hidden="true" size={16} />}
        <span>{label}</span>
      </button>
      {open ? (
        <dl className={styles.places}>
          {chapter.places.map((place, index) => (
            <div className={styles.place} key={index}>
              <dt>Было:</dt>
              <dd>{place.before || '(этого абзаца не было)'}</dd>
              <dt>Стало:</dt>
              <dd>{place.after || '(этого абзаца не будет)'}</dd>
            </div>
          ))}
        </dl>
      ) : null}
    </li>
  );
}

function changeLabel(chapter: PreviewChapter) {
  const renamed = chapter.previousTitle ? `новое название: было «${chapter.previousTitle}»` : '';
  switch (chapter.change) {
    case 'words':
      return renamed ? `слова, ${renamed}` : 'слова';
    case 'punctuation':
      return renamed ? `знаки, ${renamed}` : 'знаки';
    case 'renamed':
      return renamed || 'новое название';
    case 'added':
      return 'добавится';
    case 'removed':
      return 'удалится';
  }
}

/** A guide, not a promise: a card takes about a minute for every ten chapters, the threads of a big saga up to 90 minutes. */
function estimate(newCards: number, threads: boolean, removed: number) {
  const cards = newCards === 0
    ? 'Новые карточки глав не нужны: обработка займёт около минуты.'
    : `Новые карточки нужны ${newCards} ${pluralize(newCards, 'главе', 'главам', 'главам')} — примерно ${Math.max(1, Math.ceil(newCards / 10))} мин.`;
  if (!threads) return `${cards} Нити саги пересобирать не нужно.`;
  const reason = removed > 0 ? 'Из книги уходит глава, поэтому нити саги соберутся заново' : 'Потом нити саги соберутся заново';
  return `${cards} ${reason} — на большой саге это до полутора часов. Чат в это время работает.`;
}
