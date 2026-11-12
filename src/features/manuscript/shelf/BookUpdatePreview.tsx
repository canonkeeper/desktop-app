import { ChevronDown, ChevronRight } from 'lucide-react';
import { useState } from 'react';
import type { ProjectJob } from '../../../entities/manuscript/api';
import { formatCount, pluralize } from '../../../ui/copy';
import { Alert } from '../../../ui/feedback/Alert';
import { useSagaSections } from '../../../ui/saga-sections';
import styles from './BookUpdatePreview.module.css';

type ImportResult = Extract<NonNullable<ProjectJob['result']>, { type: 'import' }>;
export type BookUpdatePreviewResult = ImportResult & { preview: NonNullable<ImportResult['preview']> };
type PreviewChapter = BookUpdatePreviewResult['preview']['chapters'][number];

/** A preview names a change when a chapter changes, comes or goes (specs/2026-10-27-update-book-from-file.md, R6.5). */
export function previewHasChanges(result: BookUpdatePreviewResult) {
  return result.preview.chapters.length > 0 || (result.preview.moreChapters ?? 0) > 0;
}

/** Up to three chapters named, the rest counted: «„Утро“, „Река“ и ещё 2». */
function chapterNames(chapters: readonly PreviewChapter[]) {
  const named = chapters.slice(0, 3).map((chapter) => `«${chapter.title}»`).join(', ');
  return chapters.length > 3 ? `${named} и ещё ${chapters.length - 3}` : named;
}

/**
 * «Что изменится»: the counters of the plan, the changed chapters with their places «было → стало» on a press, what
 * becomes of the notes and the drawings of the chapters the file changes or removes, a warning when the file replaces what
 * was written on the site (specs/2026-11-11-ux9-shelf-chat.md SC1.4), and how long the work after the update takes (R6.5).
 * The notes are copies with the words they cite (`personal_notes`): words the new text has keep their place, others open the
 * start of its chapter; a drawing stands after the paragraph of its number, a removed chapter keeps both until it returns.
 */
export function BookUpdatePreview({ done = false, result }: { result: BookUpdatePreviewResult; done?: boolean }) {
  const { preview } = result;
  const drawings = useSagaSections().includes('drawing');
  const words = result.changedChapters ?? 0;
  const punctuation = result.punctuationChapters ?? 0;
  const renamed = result.renamedChapters ?? 0;
  const added = result.addedChapters ?? 0;
  const removed = result.removedChapters ?? 0;
  const unchanged = result.unchangedChapters ?? 0;
  // After the update the same lines say what it did (specs/2026-11-11-ux7-trash.md TB4.1).
  const lines = (done ? [
    words > 0 ? `Слова изменились — в ${words} ${pluralize(words, 'главе', 'главах', 'главах')}` : '',
    punctuation > 0 ? `Только знаки, регистр или «ё» — ${formatCount(punctuation, 'глава', 'главы', 'глав')}` : '',
    renamed > 0 ? `${pluralize(renamed, 'Переименована', 'Переименованы', 'Переименованы')} — ${renamed}` : '',
    added > 0 ? `${pluralize(added, 'Добавлена', 'Добавлены', 'Добавлены')} — ${added}` : '',
    removed > 0 ? `${pluralize(removed, 'Ушла', 'Ушли', 'Ушли')} в «Недавно удалённые» — ${removed}, вернуть можно 7 дней` : '',
    unchanged > 0 ? `Без изменений — ${unchanged}` : '',
  ] : [
    words > 0 ? `Слова изменятся — в ${words} ${pluralize(words, 'главе', 'главах', 'главах')}` : '',
    punctuation > 0 ? `Только знаки, регистр или «ё» — ${formatCount(punctuation, 'глава', 'главы', 'глав')}, перечитывать их не нужно` : '',
    renamed > 0 ? `${pluralize(renamed, 'Переименуется', 'Переименуются', 'Переименуются')} — ${renamed}` : '',
    added > 0 ? `${pluralize(added, 'Добавится', 'Добавятся', 'Добавятся')} — ${added}` : '',
    removed > 0 ? `${pluralize(removed, 'Уйдёт', 'Уйдут', 'Уйдут')} в «Недавно удалённые» — ${removed}, вернуть можно 7 дней` : '',
    unchanged > 0 ? `Без изменений — ${unchanged}` : '',
  ]).filter(Boolean);
  // What becomes of the notes and the drawings, said before the update only (SC1.4).
  const kept = done ? [] : [
    words + punctuation > 0 ? 'Заметки останутся у своих мест; если цитаты заметки в новом тексте нет, заметка сохранит её и откроет начало главы.' : '',
    words + punctuation > 0 && drawings ? 'Рисунки останутся: в изменённой главе — после того же по счёту абзаца.' : '',
    removed > 0 ? `${drawings ? 'Заметки и рисунки' : 'Заметки'} уходящих глав сохранятся и вернутся вместе с главой из «Недавно удалённых».` : '',
    // The update can be undone (specs/2026-11-12-ux10-book-revert.md BR3.1): said before the author decides.
    'Если файл окажется не тот, прежнюю версию книги можно вернуть 7 дней — в «Что изменилось» у книги.',
  ].filter(Boolean);
  const edited = done ? [] : preview.chapters.filter((chapter) => chapter.editedOnSite);
  return (
    <>
      <ul aria-label={done ? 'Что изменилось' : 'Что изменится'} className={styles.summary}>
        {lines.map((line) => <li key={line}>{line}</li>)}
      </ul>
      {edited.length ? (
        <Alert tone="warning">Правки, сделанные на сайте в этих главах, заменятся текстом файла: {chapterNames(edited)}.</Alert>
      ) : null}
      {kept.map((line) => <p className={styles.kept} key={line}>{line}</p>)}
      <section aria-label="Главы с изменениями" className={styles.chapters}>
        <h3 className={styles.label}>Главы с изменениями</h3>
        <ul className={styles.chapterList}>
          {preview.chapters.map((chapter) => <PreviewChapterRow chapter={chapter} done={done} key={`${chapter.change}-${chapter.order}-${chapter.title}`} />)}
        </ul>
        {preview.moreChapters ? (
          <p className={styles.more}>И ещё {formatCount(preview.moreChapters, 'глава', 'главы', 'глав')} с изменениями.</p>
        ) : null}
      </section>
      {done ? null : <p className={styles.estimate}>{estimate(preview.newCards, preview.threads, removed)}</p>}
    </>
  );
}

function PreviewChapterRow({ chapter, done }: { chapter: PreviewChapter; done: boolean }) {
  const [open, setOpen] = useState(false);
  const label = `${chapter.order}. ${chapter.title} — ${changeLabel(chapter, done)}${!done && chapter.editedOnSite ? ', есть правки с сайта' : ''}`;
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
              <dd>{place.after || (done ? '(этого абзаца больше нет)' : '(этого абзаца не будет)')}</dd>
            </div>
          ))}
        </dl>
      ) : null}
    </li>
  );
}

function changeLabel(chapter: PreviewChapter, done: boolean) {
  const renamed = chapter.previousTitle ? `новое название: было «${chapter.previousTitle}»` : '';
  switch (chapter.change) {
    case 'words':
      return renamed ? `слова, ${renamed}` : 'слова';
    case 'punctuation':
      return renamed ? `знаки, ${renamed}` : 'знаки';
    case 'renamed':
      return renamed || 'новое название';
    case 'added':
      return done ? 'добавлена' : 'добавится';
    case 'removed':
      return done ? 'удалена' : 'удалится';
  }
}

/**
 * A guide, not a promise, in the words of the line of the shelf (specs/2026-11-10-ux3-saga-state.md SS2.2): the helper
 * reads a chapter in about a minute for every ten, the story lines of a big saga take up to 90 minutes.
 */
function estimate(newCards: number, threads: boolean, removed: number) {
  const cards = newCards === 0
    ? 'Перечитывать главы помощнику не нужно — это займёт около минуты.'
    : `Помощник перечитает ${newCards} ${pluralize(newCards, 'главу', 'главы', 'глав')} — примерно ${Math.max(1, Math.ceil(newCards / 10))} мин.`;
  if (!threads) return `${cards} Сюжетные линии собирать заново не нужно.`;
  const reason = removed > 0 ? 'Из книги уходит глава, поэтому сюжетные линии соберутся заново' : 'Потом сюжетные линии соберутся заново';
  return `${cards} ${reason} — на большой саге это до полутора часов. Спрашивать в это время можно.`;
}
