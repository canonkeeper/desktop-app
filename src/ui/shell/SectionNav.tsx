import { CircleHelp } from 'lucide-react';
import { useId } from 'react';
import { formatCount } from '../copy';
import { Badge } from '../feedback/Badge';
import { sectionGroups, sectionTable, type SagaSection } from '../saga-sections';
import styles from './Shell.module.css';

/**
 * What the chat of the saga has while the author is elsewhere (specs/2026-11-10-ux2-shell.md S2.5,
 * specs/2026-11-11-ux4-after-answer.md AA2.2): an answer on its way, and the answers ready and not opened.
 */
export type ChatMark = { preparing: boolean; ready: number } | null;

/** The mark in words, for the screen reader: «ответ готовится», «2 ответа готовы». */
export function chatMarkWords(mark: NonNullable<ChatMark>) {
  const ready = mark.ready === 1 ? 'ответ готов' : mark.ready > 1 ? `${formatCount(mark.ready, 'ответ готов', 'ответа готовы', 'ответов готовы')}` : '';
  const preparing = mark.preparing ? (ready ? 'ещё один готовится' : 'ответ готовится') : '';
  return [ready, preparing].filter(Boolean).join(', ');
}

/** The mark of «Рисунки» in words (specs/2026-11-11-ux6-drawing-flow.md DF2.2): «рисунок рисуется», «2 рисунка готовы». */
export function drawingMarkWords(mark: NonNullable<ChatMark>) {
  const ready = mark.ready === 1 ? 'рисунок готов' : mark.ready > 1 ? `${formatCount(mark.ready, 'рисунок готов', 'рисунка готовы', 'рисунков готовы')}` : '';
  const preparing = mark.preparing ? (ready ? 'ещё один рисуется' : 'рисунок рисуется') : '';
  return [ready, preparing].filter(Boolean).join(', ');
}

/**
 * The mark of «Что помнит помощник» in words (specs/2026-11-11-ux9-shelf-chat.md SC3.6, М-04): «файл материалов
 * разбирается», «3 записи на проверку».
 */
export function memoryMarkWords(mark: NonNullable<ChatMark>) {
  const ready = mark.ready > 0 ? `${formatCount(mark.ready, 'запись', 'записи', 'записей')} на проверку` : '';
  const preparing = mark.preparing ? 'файл материалов разбирается' : '';
  return [ready, preparing].filter(Boolean).join(', ');
}

/** The sections with a mark of the one mechanism, and its words. */
const markWords: Partial<Record<SagaSection, (mark: NonNullable<ChatMark>) => string>> = {
  chat: chatMarkWords, drawing: drawingMarkWords, memory: memoryMarkWords,
};

export interface SectionNavProps {
  /** The column from 1100 px, the narrow column, the drawer of a phone (S1.3). */
  variant: 'list' | 'rail' | 'drawer';
  /** The sections of the edition, in the order of the table. */
  sections: readonly SagaSection[];
  active: SagaSection | null;
  onOpen: (section: SagaSection, trigger: HTMLElement) => void;
  chat: ChatMark;
  /** What «Рисунки» has while the author is elsewhere: a drawing made, the drawings ready and not opened (DF2.2). */
  drawing?: ChatMark;
  /** What «Что помнит помощник» has: a file of the materials read, its records waiting for the check (SC3.6). */
  memory?: ChatMark;
  /** «Помощь» under the sections (S5.2); none where the edition has no window to write to. */
  onHelp?: (() => void) | null;
  helpReplies?: number;
}

/**
 * The sections of the saga by the one table (SC2.1): three signed groups, the current section `aria-current`, the chat with
 * its mark, «Помощь» at the bottom. The same list is the column, the narrow column — the word under
 * the icon, in two lines when long — and the drawer of a phone.
 */
export function SectionNav({ active, chat, drawing = null, helpReplies = 0, memory = null, onHelp, onOpen, sections, variant }: SectionNavProps) {
  const id = useId();
  const rail = variant === 'rail';
  const iconSize = rail ? 20 : 16;
  const groups = sectionGroups
    .map(({ group, label }) => ({ group, label, entries: sectionTable.filter((entry) => entry.group === group && sections.includes(entry.section)) }))
    .filter(({ entries }) => entries.length > 0);
  return (
    <div className={styles.sectionsColumn} data-variant={variant}>
      {groups.length ? <nav aria-label="Разделы" className={styles.sections}>
        {groups.map(({ entries, group, label }) => {
          return (
            <div className={styles.group} key={group}>
              <p className={styles.groupLabel} id={`${id}-${group}`}>{label}</p>
              <ul aria-labelledby={`${id}-${group}`} className={styles.groupList}>
                {entries.map(({ Icon, label: word, section }) => {
                  const here = active === section;
                  // The chat and «Рисунки» by one mechanism (AA2.2; specs/2026-11-11-ux6-drawing-flow.md DF2.2).
                  const words = markWords[section];
                  const raw = section === 'chat' ? chat : section === 'drawing' ? drawing : section === 'memory' ? memory : null;
                  const mark = words && !here && raw && (raw.preparing || raw.ready > 0) ? raw : null;
                  const markAttribute = section === 'chat' ? 'data-chat-mark' : 'data-section-mark';
                  return (
                    <li key={section}>
                      <button aria-current={here ? 'page' : undefined} aria-describedby={mark ? `${id}-${section}-mark` : undefined}
                        className={styles.item} data-section={section}
                        onClick={(event) => onOpen(section, event.currentTarget)} type="button">
                        <span className={styles.itemIcon}>
                          <Icon aria-hidden="true" size={iconSize} />
                          {mark && rail ? <span aria-hidden="true" className={styles.dot} /> : null}
                        </span>
                        <span className={styles.itemWord}>{word}</span>
                        {/* The number of what is ready and not opened; a dot while one is on its way (AA2.2). */}
                        {mark && !rail && mark.ready > 0 ? <Badge aria-hidden="true" className={styles.chatCount} {...{ [markAttribute]: 'ready' }} tone="info">{mark.ready}</Badge> : null}
                        {mark && !rail && mark.ready === 0 ? <span aria-hidden="true" className={styles.dot} {...{ [markAttribute]: 'preparing' }} /> : null}
                      </button>
                      {mark && words ? <span className={styles.visuallyHidden} id={`${id}-${section}-mark`}>{words(mark)}</span> : null}
                    </li>
                  );
                })}
              </ul>
            </div>
          );
        })}
      </nav> : null}
      {onHelp ? (
        <button aria-describedby={`${id}-help`} className={`${styles.item} ${styles.help}`} onClick={onHelp} type="button">
          <span className={styles.itemIcon}>
            <CircleHelp aria-hidden="true" size={iconSize} />
            {helpReplies > 0 && rail ? <span aria-hidden="true" className={styles.dot} /> : null}
          </span>
          <span className={styles.itemWord}>Помощь</span>
          {helpReplies > 0 && !rail ? <span aria-hidden="true" className={styles.replies}>{formatCount(helpReplies, 'ответ', 'ответа', 'ответов')}</span> : null}
        </button>
      ) : null}
      {/* «Помощь» opens the window «Написать нам» (specs/2026-11-10-ux2-contact.md CT1.6), on «Ответы» while one waits (CT2.1). */}
      {onHelp ? (
        <span className={styles.visuallyHidden} id={`${id}-help`}>
          {helpReplies > 0 ? `Написать нам · ${formatCount(helpReplies, 'новый ответ поддержки', 'новых ответа поддержки', 'новых ответов поддержки')}` : 'Написать нам'}
        </span>
      ) : null}
    </div>
  );
}
