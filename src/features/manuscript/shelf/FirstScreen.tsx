import type { ReactNode } from 'react';
import { EntryCard, EntryLine } from '../../../ui/feedback/EntryCard';
import styles from './FirstScreen.module.css';

/** The saga «Поговорить» and «Вычитать мою главу» found for an author without one; the screen names it and offers to rename it. */
export const firstSagaTitle = 'Моя сага';

/**
 * Before the first saga, one screen with three ways in (specs/2026-11-10-ux3-first-screen.md FS1.2; board 30·23): name the
 * saga — the one main button —, look at the example of a build that has it, proofread a chapter of one's own. The shell
 * stands without the sections around it (S1.4), and every page of a saga leads here while there is none (FS1.1).
 */
export function FirstScreen({ createSaga, example, proofread }: {
  /** The form of the name with «Создать сагу». */
  createSaga: ReactNode;
  /** «Посмотреть на примере» of a build with the demo saga (specs/2026-11-09-demo-saga.md DS1.1); none without. */
  example: ReactNode;
  /** «Вычитать мою главу» of a build with «Письмо» (FS2.1); none without. */
  proofread: ReactNode;
}) {
  return (
    <section aria-labelledby="first-screen-title" className={styles.screen}>
      <div className={styles.header}>
        <p className={styles.welcome}>Добро пожаловать в CanonKeeper</p>
        <h1 data-page-heading="" id="first-screen-title" tabIndex={-1}>С чего начнём?</h1>
      </div>
      <div className={styles.entries}>
        <EntryCard title="Назовите сагу">
          {createSaga}
          <EntryLine>Сага — это все ваши книги одной истории. Дальше — загрузите первую книгу: через пару минут по ней можно спрашивать</EntryLine>
        </EntryCard>
        {example}
        {proofread}
      </div>
    </section>
  );
}
