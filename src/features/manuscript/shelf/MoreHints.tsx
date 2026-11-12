import { FileCheck2, Mic, X } from 'lucide-react';
import { useId, useState, type ComponentType } from 'react';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { useTalkAction } from '../../../ui/navigation/TopBarStrip';
import { sectionEntry, useSagaSections } from '../../../ui/saga-sections';
import { hideHints, hintsHidden } from './more-hints';
import styles from './MoreHints.module.css';

interface Hint {
  key: string;
  Icon: ComponentType<{ 'aria-hidden'?: boolean | 'true'; size?: number }>;
  text: string;
  open: () => void;
}

/**
 * «Что ещё можно» of the shelf once the saga has its first answer, in place of the steps that are gone
 * (specs/2026-11-10-ux3-first-screen.md FS3.3; board 30·26): each line is a way into a section, the voice is «Поговорить»
 * of the top bar. «Скрыть подсказки» closes the row for good for this author in this browser.
 */
export function MoreHints({ authorId, onOpen }: {
  authorId: string;
  /** A page of the saga: the map, «Рисование», «Письмо». */
  onOpen: (path: '/saga' | '/drawing' | '/writing') => void;
}) {
  const titleId = useId();
  const sections = useSagaSections();
  const talk = useTalkAction();
  const [hidden, setHidden] = useState(() => hintsHidden(authorId));
  if (hidden) return null;
  const hints: Hint[] = [
    { key: 'map', Icon: sectionEntry('saga').Icon, text: 'Карта — кто с кем связан', open: () => onOpen('/saga') },
    ...sections.includes('drawing') ? [{ key: 'drawing', Icon: sectionEntry('drawing').Icon, text: 'Рисунки — портрет героя по книге', open: () => onOpen('/drawing') }] : [],
    ...sections.includes('writing') ? [
      { key: 'writing', Icon: sectionEntry('writing').Icon, text: 'Письмо — следующая глава с помощником', open: () => onOpen('/writing') },
      { key: 'proofread', Icon: FileCheck2, text: 'Вычитка — опечатки и неловкие места в вашей главе', open: () => onOpen('/writing') },
    ] : [],
    ...talk ? [{ key: 'talk', Icon: Mic, text: 'Поговорить — то же голосом', open: talk }] : [],
  ];
  return (
    <section aria-labelledby={titleId} className={styles.hints}>
      <div className={styles.head}>
        <h2 id={titleId}>Что ещё можно</h2>
        <IconButton aria-label="Скрыть подсказки" onClick={() => {
          hideHints(authorId);
          setHidden(true);
        }} title="Скрыть подсказки">
          <X aria-hidden="true" size={16} />
        </IconButton>
      </div>
      <ul className={styles.rows}>
        {hints.map(({ Icon, key, open, text }) => (
          <li key={key}>
            <Button className={styles.row} onClick={open} variant="ghost"><Icon aria-hidden="true" size={16} />{text}</Button>
          </li>
        ))}
      </ul>
    </section>
  );
}
