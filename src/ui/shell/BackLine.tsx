import { ArrowLeft } from 'lucide-react';
import type { BackFrom } from '../../shared/navigation/app-route';
import { Button } from '../controls/Button';
import styles from './BackLine.module.css';

/** The beginning of a long name, by whole words: the line keeps one row (PS4.2). */
function short(name: string) {
  const words = name.replace(/\s+/gu, ' ').trim();
  const letters = Array.from(words);
  if (letters.length <= 40) return words;
  const cut = letters.slice(0, 40).join('');
  const space = cut.lastIndexOf(' ');
  return `${(space > 10 ? cut.slice(0, space) : cut).replace(/[\s,.;:—-]+$/u, '')}…`;
}

/**
 * The words of the way back (specs/2026-11-10-ux2-patterns-states.md PS4.2): the word of the section and, when there is
 * one, the name of its object — «К чату «Кто такой Сава…»», «К карте: Мара», «К главе «Встреча у ворот»».
 */
export function backWords(from: BackFrom) {
  const name = from.name?.trim() ? short(from.name) : undefined;
  switch (from.section) {
    case 'chat': return name ? `К чату «${name}»` : 'К чату';
    case 'saga': return name ? `К карте: ${name}` : 'К карте';
    case 'writing': return name ? `К главе «${name}»` : 'К главе в работе';
    case 'drawing': return name ? `К рисунку «${name}»` : 'К рисунку';
    case 'notes': return name ? `К заметке «${name}»` : 'К заметкам';
    // The chapter read in the book (specs/2026-11-10-ux7-reader.md RD3.2): «Нарисовать» and «Спросить» of a selection.
    // «Править главу» of the reader names no chapter: the way back is to the book (specs/2026-11-11-ux9-writing-a.md WA4.2).
    case 'manuscript': return name ? `К главе «${name}»` : 'К главе в книге';
    // A list of a section, by the word of its menu (specs/2026-11-11-ux9-account.md AC2.6).
    case 'books': return 'К книгам';
    case 'writings': return 'К «Письму»';
    case 'drawings': return 'К рисункам';
    case 'memory': return 'К «Что помнит помощник»';
  }
}

export interface BackLineProps {
  /** Where the line leads: «К чату «…»», «К вопросу в чате». */
  words: string;
  /** A step back in the history of the tab: the place and what the author typed there stay (PS4.1). */
  onBack: () => void;
  /** The place of the line in a page that sets it itself, as «Тариф» does. */
  className?: string;
}

/**
 * «← К <откуда>» over the content (specs/2026-11-10-ux2-patterns-states.md PS4.1; master `Pattern/Common/Back Line`, board
 * 15·10): the author came by a link of another section and goes back to the place he left, in the same tab.
 */
export function BackLine({ className, onBack, words }: BackLineProps) {
  return (
    <nav aria-label="Возврат" className={`${styles.line}${className ? ` ${className}` : ''}`}>
      <Button className={styles.back} onClick={onBack} size="sm" variant="ghost">
        <ArrowLeft aria-hidden="true" size={16} />{words}
      </Button>
    </nav>
  );
}
