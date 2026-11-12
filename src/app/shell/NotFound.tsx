import type { RouteNavigate } from '../../shared/navigation/app-route';
import { ErrorBlock } from '../../ui/feedback/ErrorBlock';
import { PageHead } from '../../ui/shell/PageHead';
import styles from './NotFound.module.css';

/**
 * «Страница не найдена» inside the shell (specs/2026-11-10-ux2-shell.md S1.1): the sections stay beside it; the one block
 * of an error says it with one way on (specs/2026-11-10-ux2-patterns-refusals.md PR3.5). The top is the one top of a page,
 * the width of reading is the shell's (specs/2026-11-10-ux2-patterns-states.md PS1.1, PS1.2).
 */
export function NotFound({ navigate }: { navigate: RouteNavigate }) {
  return (
    <section aria-labelledby="route-not-found-title" className={styles.page}>
      <PageHead headingId="route-not-found-title" surface="unknown" />
      <ErrorBlock action={{ label: 'К книгам', onClick: () => navigate('/manuscript/books') }} className={styles.block}
        cause="Адрес устарел или в нём опечатка." title="Такой страницы нет" />
    </section>
  );
}
