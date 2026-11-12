import { useRef, useState, type FormEvent } from 'react';
import { isApiStatusFailure, publicApiErrorMessage } from '../../../entities/api-errors';
import {
  authorMaterialKinds, createIdempotencyKey, type AuthorMaterialKind, type AuthorMaterialsApiClient, type CreateAuthorMaterial,
} from '../../../entities/author-materials/api';
import type { SagaMapNodeCard } from '../../../entities/saga/api';
import { toHref } from '../../../shared/navigation/app-route';
import { Button } from '../../../ui/controls/Button';
import { Field } from '../../../ui/controls/Field';
import { Select } from '../../../ui/controls/Select';
import { Textarea } from '../../../ui/controls/Textarea';
import { Alert } from '../../../ui/feedback/Alert';
import { Confirm, deletionLines } from '../../../ui/overlays/Confirm';
import { Dialog } from '../../../ui/overlays/Dialog';
import { propertiesToText, textToProperties } from './properties-text';
import { useMaterialsMutation } from './use-materials-mutation';
import styles from './Materials.module.css';

/** What the dialogs need of a record: from the list or from the card of a node. */
export type EditedMaterial = NonNullable<SagaMapNodeCard['material']>;

const saveMessages = {
  forbidden: 'Менять материалы может редактор саги.',
  // MR2.3: what the author typed stays in the fields.
  conflict: 'Запись изменили в другом окне. Ваш текст сохранён в поле — откройте запись заново и вставьте его.',
};

/** The record of a name another record has (MR2.3): the problem names it. */
function takenName(error: unknown) {
  return isApiStatusFailure(error) && error.problem.code === 'author_materials.name_taken';
}

interface DialogProps {
  api: AuthorMaterialsApiClient;
  material: EditedMaterial;
  onClose: () => void;
  projectId: string;
}

/**
 * «Изменить» a record (R9.6), or «Записать самому» — a new one by hand (specs/2026-11-10-ux8-memory-records.md MR2.2): everything
 * it says, the properties as lines «Ключ: значение». A name another record has is said at the field «Имя» with the way to that
 * record in a tab of its own; the dialog stays with what the author typed.
 */
export function MaterialEditor({ api, material, onClose, projectId }: Omit<DialogProps, 'material'> & { material?: EditedMaterial }) {
  const [name, setName] = useState(material?.name ?? '');
  const [kind, setKind] = useState<AuthorMaterialKind>(material?.kind ?? 'character');
  const [description, setDescription] = useState(material?.description ?? '');
  const [properties, setProperties] = useState(propertiesToText(material?.properties ?? []));
  const attempt = useRef<{ fingerprint: string; key: string } | null>(null);
  const save = useMaterialsMutation(projectId, (body: CreateAuthorMaterial) => {
    if (material) return api.updateAuthorMaterial(projectId, material.id, { ...body, expectedRevision: material.revision });
    // The same record sent again goes under the same replay key.
    const fingerprint = JSON.stringify(body);
    if (attempt.current?.fingerprint !== fingerprint) attempt.current = { fingerprint, key: createIdempotencyKey('createAuthorMaterial') };
    return api.createAuthorMaterial(projectId, body, { idempotencyKey: attempt.current.key });
  }, (card, updated) => ({
    ...card,
    material: card.material?.id === updated.id ? updated : card.material,
    namedIn: card.namedIn.map((record) => (record.id === updated.id ? { id: updated.id, name: updated.name, kind: updated.kind } : record)),
  }));
  const taken = takenName(save.error);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    save.mutate({
      name: name.trim(), kind, description: description.trim(), properties: textToProperties(properties),
      ...(material ? { section: material.section } : {}),
    }, { onSuccess: onClose });
  };
  const title = material ? 'Изменить запись' : 'Новая запись';
  return (
    <Dialog canDismiss={!save.isPending} label={title} onDismiss={onClose} width={560}>
      <form className={styles.form} onSubmit={submit}>
        <h2>{title}</h2>
        <Field error={taken ? (
          // specs/2026-11-11-ux8-map.md MP3.6: the record on the page of the memory, in a tab of its own — this form keeps its text.
          <>Запись с таким именем уже есть — <a href={toHref('/memory', { projectId, part: 'materials', q: name.trim() })} rel="noopener" target="_blank"
            title="Откроется в новой вкладке — набранное здесь останется">открыть её</a></>
        ) : undefined} label="Имя" maxLength={200} onChange={(event) => setName(event.target.value)} required value={name} />
        <Select<AuthorMaterialKind> label="Что это" onValueChange={setKind} options={[...authorMaterialKinds]} value={kind} />
        <Textarea label="Описание" maxLength={8000} onChange={(event) => setDescription(event.target.value)} rows={6} value={description} />
        <Textarea hint="По одному на строку: «Ранг: Уникальный»." label="Свойства" onChange={(event) => setProperties(event.target.value)} rows={4} value={properties} />
        {save.isError && !taken ? <Alert tone="error">{publicApiErrorMessage(save.error, 'Не удалось сохранить запись.', saveMessages)}</Alert> : null}
        <div className={styles.actions}>
          <Button disabled={save.isPending} onClick={onClose} variant="ghost">Отмена</Button>
          <Button disabled={save.isPending} disabledReason={save.isPending || name.trim() ? null : 'Сначала напишите имя.'} type="submit" variant="primary">Сохранить</Button>
        </div>
      </form>
    </Dialog>
  );
}

/** «Удалить запись…» of a record (R9.6, PA3.1): it does not come back; onDeleted is for the card a record was all of. */
export function MaterialDeletion({ api, material, onClose, onDeleted, projectId }: DialogProps & { onDeleted?: () => void }) {
  const remove = useMaterialsMutation(projectId, () => api.deleteAuthorMaterial(projectId, material.id, material.revision), (card) => ({
    ...card,
    material: card.material?.id === material.id ? null : card.material,
    namedIn: card.namedIn.filter((record) => record.id !== material.id),
  }));
  // The dialog stands in the card this write changes, and may be gone when it ends.
  const confirm = () => void remove.mutateAsync(undefined).then(() => {
    onClose();
    onDeleted?.();
  }, () => undefined);
  const gone = isApiStatusFailure(remove.error) && remove.error.status === 404;
  return (
    <Confirm action="Удалить запись" also="В книге ничего не изменится." busy={remove.isPending}
      error={remove.isError ? (gone ? 'Этой записи уже нет.' : publicApiErrorMessage(remove.error, 'Не удалось удалить запись.', saveMessages)) : null}
      irreversible onCancel={onClose} onConfirm={gone ? onClose : confirm} title={`Удалить запись «${material.name}»?`}>
      {deletionLines.gone}
    </Confirm>
  );
}
