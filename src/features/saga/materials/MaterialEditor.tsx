import { useState, type FormEvent } from 'react';
import { isApiStatusFailure, publicApiErrorMessage } from '../../../entities/api-errors';
import {
  authorMaterialKinds, type AuthorMaterialKind, type AuthorMaterialsApiClient, type UpdateAuthorMaterial,
} from '../../../entities/author-materials/api';
import type { SagaMapNodeCard } from '../../../entities/saga/api';
import { Button } from '../../../ui/controls/Button';
import { Field } from '../../../ui/controls/Field';
import { Select } from '../../../ui/controls/Select';
import { Textarea } from '../../../ui/controls/Textarea';
import { Alert } from '../../../ui/feedback/Alert';
import { Dialog } from '../../../ui/overlays/Dialog';
import { propertiesToText, textToProperties } from './properties-text';
import { useMaterialsMutation } from './use-materials-mutation';
import styles from './Materials.module.css';

/** What the dialogs need of a record: from the list or from the card of a node. */
export type EditedMaterial = NonNullable<SagaMapNodeCard['material']>;

const saveMessages = {
  forbidden: 'Менять материалы может редактор саги.',
  conflict: 'Запись уже изменили или это имя занято. Закройте окно и откройте её снова.',
};

interface DialogProps {
  api: AuthorMaterialsApiClient;
  material: EditedMaterial;
  onClose: () => void;
  projectId: string;
}

/** «Править» a record (R9.6): everything it says, the properties as lines «Ключ: значение». */
export function MaterialEditor({ api, material, onClose, projectId }: DialogProps) {
  const [name, setName] = useState(material.name);
  const [kind, setKind] = useState<AuthorMaterialKind>(material.kind);
  const [description, setDescription] = useState(material.description);
  const [properties, setProperties] = useState(propertiesToText(material.properties));
  const save = useMaterialsMutation(projectId, (body: UpdateAuthorMaterial) => api.updateAuthorMaterial(projectId, material.id, body), (card, updated) => ({
    ...card,
    material: card.material?.id === updated.id ? updated : card.material,
    namedIn: card.namedIn.map((record) => (record.id === updated.id ? { id: updated.id, name: updated.name, kind: updated.kind } : record)),
  }));
  const submit = (event: FormEvent) => {
    event.preventDefault();
    save.mutate({
      name: name.trim(), kind, description: description.trim(), properties: textToProperties(properties), section: material.section,
      expectedRevision: material.revision,
    }, { onSuccess: onClose });
  };
  return (
    <Dialog canDismiss={!save.isPending} label="Изменить запись" onDismiss={onClose} width={560}>
      <form className={styles.form} onSubmit={submit}>
        <h2>Изменить запись</h2>
        <Field label="Имя" maxLength={200} onChange={(event) => setName(event.target.value)} required value={name} />
        <Select<AuthorMaterialKind> label="Что это" onValueChange={setKind} options={[...authorMaterialKinds]} value={kind} />
        <Textarea label="Описание" maxLength={8000} onChange={(event) => setDescription(event.target.value)} rows={6} value={description} />
        <Textarea hint="По одному на строку: «Ранг: Уникальный»." label="Свойства" onChange={(event) => setProperties(event.target.value)} rows={4} value={properties} />
        {save.isError ? <Alert tone="error">{publicApiErrorMessage(save.error, 'Не удалось сохранить запись.', saveMessages)}</Alert> : null}
        <div className={styles.actions}>
          <Button disabled={save.isPending} onClick={onClose} variant="ghost">Отмена</Button>
          <Button disabled={save.isPending || !name.trim()} type="submit" variant="primary">Сохранить</Button>
        </div>
      </form>
    </Dialog>
  );
}

/** «Удалить» a record (R9.6); onDeleted is for the card a record was all of. */
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
    <Dialog canDismiss={!remove.isPending} label="Удалить запись" onDismiss={onClose} width={560}>
      <div className={styles.form}>
        <h2>Удалить запись</h2>
        <p>«{material.name}» уйдёт из ваших материалов. В книге ничего не изменится.</p>
        {remove.isError ? <Alert tone="error">{gone ? 'Этой записи уже нет.' : publicApiErrorMessage(remove.error, 'Не удалось удалить запись.', saveMessages)}</Alert> : null}
        <div className={styles.actions}>
          <Button disabled={remove.isPending} onClick={onClose} variant="ghost">Отмена</Button>
          <Button disabled={remove.isPending || gone} onClick={confirm} variant="danger">Удалить</Button>
        </div>
      </div>
    </Dialog>
  );
}
