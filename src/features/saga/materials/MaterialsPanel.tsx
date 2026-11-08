import { Pencil, Trash2 } from 'lucide-react';
import { useEffect, useState } from 'react';
import {
  authorMaterialKindLabel, useAuthorMaterialsQuery, type AuthorMaterial, type AuthorMaterialsApiClient,
} from '../../../entities/author-materials/api';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { SearchField } from '../../../ui/controls/SearchField';
import { pluralize } from '../../../ui/copy';
import { Alert } from '../../../ui/feedback/Alert';
import { Skeleton } from '../../../ui/feedback/Skeleton';
import { useSagaSlot, type MaterialsImport } from '../saga-slot';
import { MaterialDeletion, MaterialEditor } from './MaterialEditor';
import { StepHeading } from './StepHeading';
import styles from './Materials.module.css';

export interface MaterialsPanelProps {
  api: AuthorMaterialsApiClient;
  canEdit: boolean;
  projectId: string;
  /** Opens the card of a record on the map. */
  onOpen: (name: string) => void;
}

const searchDelay = 300;

const noImport: MaterialsImport = { step: null, saved: null, add: null };

/** The panel without the import from a file of the site. */
function useNoImport(_options: { canEdit: boolean; projectId: string }) {
  return noImport;
}

/** «Материалы автора» (specs/2026-11-04-saga-map.md, R9.2, R9.6): the records with a search, and the way to add from a file. */
export function MaterialsPanel({ api, canEdit, onOpen, projectId }: MaterialsPanelProps) {
  const useImport = useSagaSlot()?.materialsImport?.useImport ?? useNoImport;
  const materialsImport = useImport({ canEdit, projectId });
  if (materialsImport.step) return materialsImport.step;
  return (
    <section aria-label="Материалы автора" className={styles.panel}>
      <StepHeading>Материалы автора</StepHeading>
      <p className={styles.note}>Ваши навыки, места и персонажи. Помощник видит их при письме и не выдаёт за то, что уже случилось в книге.</p>
      {materialsImport.saved}
      {materialsImport.add}
      <MaterialList api={api} canEdit={canEdit} onOpen={onOpen} projectId={projectId} />
    </section>
  );
}

function MaterialList({ api, canEdit, onOpen, projectId }: MaterialsPanelProps) {
  const [draft, setDraft] = useState('');
  const [query, setQuery] = useState('');
  const [dialog, setDialog] = useState<{ type: 'edit' | 'delete'; material: AuthorMaterial } | null>(null);
  useEffect(() => {
    const timer = window.setTimeout(() => setQuery(draft.trim()), searchDelay);
    return () => window.clearTimeout(timer);
  }, [draft]);
  const list = useAuthorMaterialsQuery(api, projectId, query ? { q: query } : {});
  const records = list.data?.pages.flatMap((page) => page.data) ?? [];
  const total = list.data?.pages[0]?.meta.totalCount ?? 0;
  if (list.isPending) return <Skeleton style={{ height: 120 }} />;
  if (list.isError) return <Alert tone="error">Не удалось загрузить материалы.</Alert>;
  if (!records.length && !query) return <p className={styles.note}>Пока записей нет. Загрузите свой Excel или Word.</p>;
  return (
    <>
      <SearchField label="Найти в материалах" labelHidden onChange={(event) => setDraft(event.target.value)} onClear={() => setDraft('')} placeholder="Найти в материалах" value={draft} />
      <p className={styles.meta}>{total} {pluralize(total, 'запись', 'записи', 'записей')}</p>
      <ul aria-label="Записи" className={styles.records}>
        {records.map((material) => (
          <li key={material.id}>
            <button className={styles.recordOpen} onClick={() => onOpen(material.name)} type="button">
              <strong>{material.name}</strong>
              <span className={styles.meta}>{authorMaterialKindLabel(material.kind)}{material.section ? ` · ${material.section}` : ''}</span>
              {material.description ? <span className={styles.excerpt}>{material.description}</span> : null}
            </button>
            {canEdit ? (
              <span className={styles.recordActions}>
                <IconButton aria-label={`Изменить: ${material.name}`} onClick={() => setDialog({ type: 'edit', material })} size="sm"><Pencil aria-hidden="true" size={15} /></IconButton>
                <IconButton aria-label={`Удалить: ${material.name}`} onClick={() => setDialog({ type: 'delete', material })} size="sm"><Trash2 aria-hidden="true" size={15} /></IconButton>
              </span>
            ) : null}
          </li>
        ))}
      </ul>
      {list.hasNextPage ? <Button disabled={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()} size="sm" variant="ghost">Показать ещё</Button> : null}
      {dialog?.type === 'edit' ? <MaterialEditor api={api} material={dialog.material} onClose={() => setDialog(null)} projectId={projectId} /> : null}
      {dialog?.type === 'delete' ? <MaterialDeletion api={api} material={dialog.material} onClose={() => setDialog(null)} projectId={projectId} /> : null}
    </>
  );
}
