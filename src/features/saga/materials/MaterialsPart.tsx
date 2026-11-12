import { Pencil, Plus } from 'lucide-react';
import { useEffect, useState, type MouseEvent } from 'react';
import {
  materialOriginLine, useAuthorMaterialsQuery, type AuthorMaterial, type AuthorMaterialsApiClient,
} from '../../../entities/author-materials/api';
import { loadFailureWords } from '../../../entities/api-errors';
import { toHref, type RouteNavigate } from '../../../shared/navigation/app-route';
import { Button } from '../../../ui/controls/Button';
import { MoreMenu } from '../../../ui/overlays/MoreMenu';
import { SearchField } from '../../../ui/controls/SearchField';
import { pluralize } from '../../../ui/copy';
import { EmptyState } from '../../../ui/feedback/EmptyState';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { SkeletonArea } from '../../../ui/feedback/Skeleton';
import { useSagaSlot, type MaterialsImport } from '../saga-slot';
import { MaterialDeletion, MaterialEditor } from './MaterialEditor';
import styles from './Materials.module.css';

export interface MaterialsPartProps {
  api: AuthorMaterialsApiClient;
  canEdit: boolean;
  navigate: RouteNavigate;
  projectId: string;
  /** The search the address gives, «открыть её» of a taken name (specs/2026-11-11-ux8-map.md MP3.6). */
  query?: string;
}

const searchDelay = 300;

const noImport: MaterialsImport = { step: null, saved: null, add: null };

/** The part without the import from a file of the site. */
function useNoImport(_options: { canEdit: boolean; navigate: RouteNavigate; projectId: string }) {
  return noImport;
}

/**
 * «Ваши материалы», the part of «Что помнит помощник» with the records of the author (specs/2026-11-10-ux2-memory-page.md
 * MP1.3; specs/2026-11-04-saga-map.md, R9.2, R9.6): the records with a search and the way to add a file or a text; a step of
 * an import stands in the part instead of the list until the author answers it.
 */
export function MaterialsPart({ api, canEdit, navigate, projectId, query }: MaterialsPartProps) {
  const useImport = useSagaSlot()?.materialsImport?.useImport ?? useNoImport;
  const materialsImport = useImport({ canEdit, navigate, projectId });
  const [adding, setAdding] = useState(false);
  if (materialsImport.step) return materialsImport.step;
  return (
    <div className={styles.part}>
      <p className={styles.note}>Ваши навыки, места и герои. Помощник видит их при письме и не выдаёт за то, что уже случилось в книге.</p>
      {materialsImport.saved}
      {canEdit ? (
        <div className={styles.addRow}>
          {materialsImport.add}
          {/* MR2.2: one record by hand, the same form as «Изменить запись». */}
          <Button onClick={() => setAdding(true)} variant={materialsImport.add ? 'outline' : 'primary'}><Plus aria-hidden="true" size={16} />Записать самому</Button>
        </div>
      ) : null}
      <MaterialList api={api} canEdit={canEdit} fromFiles={Boolean(materialsImport.add)} navigate={navigate} projectId={projectId} query={query} />
      {adding ? <MaterialEditor api={api} onClose={() => setAdding(false)} projectId={projectId} /> : null}
    </div>
  );
}

function MaterialList({ api, canEdit, fromFiles, navigate, projectId, query: asked = '' }: MaterialsPartProps & { fromFiles: boolean }) {
  const [draft, setDraft] = useState(asked);
  const [query, setQuery] = useState(asked.trim());
  const [dialog, setDialog] = useState<{ type: 'edit' | 'delete'; material: AuthorMaterial } | null>(null);
  useEffect(() => {
    const timer = window.setTimeout(() => setQuery(draft.trim()), searchDelay);
    return () => window.clearTimeout(timer);
  }, [draft]);
  const list = useAuthorMaterialsQuery(api, projectId, query ? { q: query } : {});
  const records = list.data?.pages.flatMap((page) => page.data) ?? [];
  const total = list.data?.pages[0]?.meta.totalCount ?? 0;
  if (list.isPending) return <SkeletonArea count={3} shape="rows" />;
  if (list.isError) return <ErrorBlock {...loadFailureWords(list.error)} onRetry={() => void list.refetch()} retrying={list.isRefetching} title="Не открылись ваши материалы" />;
  if (!records.length && !query) {
    return (
      <EmptyState
        // To a reader — the word of the line of the role under the top bar (specs/2026-11-12-ux12-shell.md SH4.3, W-82).
        why={!canEdit ? 'Записи появятся, когда владелец саги их добавит.'
          : fromFiles ? 'Добавьте запись сами или файл — Excel, Word, таблицу .csv, текст .txt или .md: навыки, места, герои.'
            : 'Добавьте запись сами: навык, место, героя.'}
        title="Пока записей нет" />
    );
  }
  return (
    <>
      <SearchField label="Найти в материалах" labelHidden onChange={(event) => setDraft(event.target.value)} onClear={() => setDraft('')} placeholder="Найти в материалах" value={draft} />
      <p className={styles.meta} role="status">{total} {pluralize(total, 'запись', 'записи', 'записей')}</p>
      <ul aria-label="Записи" className={styles.records}>
        {records.map((material) => {
          // The name opens the card of the record on the map: one of the book, or its own (R9.5).
          const card = { projectId, node: material.name };
          const open = (event: MouseEvent<HTMLAnchorElement>) => {
            if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
            event.preventDefault();
            navigate('/saga', card);
          };
          return (
            <li key={material.id}>
              <div className={styles.recordText}>
                <a className={styles.recordName} href={toHref('/saga', card)} onClick={open}>{material.name}</a>
                <span className={styles.meta}>{materialOriginLine(material)}</span>
                {material.description ? <span className={styles.excerpt}>{material.description}</span> : null}
              </div>
              {canEdit ? (
                <span className={styles.recordActions}>
                  {/* «Изменить» on top, «Удалить запись…» last and apart (specs/2026-11-10-ux2-patterns-actions.md PA3.2). */}
                  <MoreMenu items={[{ label: 'Изменить', icon: Pencil, run: () => setDialog({ type: 'edit', material }) }]} label={`Действия записи «${material.name}»`}
                    remove={{ label: 'Удалить запись…', run: () => setDialog({ type: 'delete', material }) }} />
                </span>
              ) : null}
            </li>
          );
        })}
      </ul>
      {!records.length ? <p className={styles.note}>Ничего не нашлось.</p> : null}
      {list.hasNextPage ? <Button disabled={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()} size="sm" variant="ghost">Показать ещё</Button> : null}
      {dialog?.type === 'edit' ? (
        <MaterialEditor api={api} material={dialog.material} onClose={() => setDialog(null)} projectId={projectId} />
      ) : null}
      {dialog?.type === 'delete' ? <MaterialDeletion api={api} material={dialog.material} onClose={() => setDialog(null)} projectId={projectId} /> : null}
    </>
  );
}
