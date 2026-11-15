// SPDX-License-Identifier: AGPL-3.0-only
import { RotateCw } from 'lucide-react';
import { useState } from 'react';
import { Button } from '../../../../ui/controls/Button';
import { Field } from '../../../../ui/controls/Field';
import { Select, type SelectOption } from '../../../../ui/controls/Select';
import type { AiModels } from './helper-api';
import { fieldWords, helperWords, type Preset } from './helper-words';
import { wordsOfReason } from './reason-words';
import styles from './Helper.module.css';

export type ModelRole = 'chatModel' | 'cardsModel';

/** Why there is no list of the provider yet: its provider is not saved, or its key is not. */
export type NoList = 'provider' | 'key';

export interface ModelFieldsProps {
  preset: Preset;
  values: Record<ModelRole, string>;
  onChange: (role: ModelRole, value: string) => void;
  /** What the provider said of its models; null — not asked, and `noList` says why. */
  models: { data: AiModels | undefined; pending: boolean; refetch: () => void; refetching: boolean } | null;
  noList: NoList | null;
  /** The field the server refused, by its JSON pointer. */
  refused: string | null;
}

/** The choice that turns the list into a field for a name the list does not have. */
const writeChoice = '\u0000write';

const roles: readonly { role: ModelRole; label: string; path: string }[] = [
  { role: 'chatModel', label: helperWords.chatModel, path: '/own/chatModel' },
  { role: 'cardsModel', label: helperWords.cardsModel, path: '/own/cardsModel' },
];

/**
 * «Модель для чата», «Модель для чтения книг» (HW1.4; board 44·25): chosen from the list of the provider; a provider that
 * gives no list — the name is written; a list that failed says why in words, and the name may be written all the same.
 */
export function ModelFields({ models, noList, onChange, preset, refused, values }: ModelFieldsProps) {
  // A name the list does not have is written by hand: the field of the role stays a field until the author goes back.
  const [writing, setWriting] = useState<Record<ModelRole, boolean>>({ chatModel: false, cardsModel: false });
  const listed = models?.data?.outcome === 'listed' && models.data.models.length > 0 ? models.data.models : null;
  const pending = Boolean(models?.pending);
  const failed = models?.data?.outcome === 'failed' ? wordsOfReason(models.data.reason ?? 'other', preset) : null;
  const line = noList === 'provider' ? helperWords.modelsAfterSave
    : noList === 'key' ? helperWords.modelsAfterKey
      : models?.data?.outcome === 'unlisted' || (models?.data?.outcome === 'listed' && !listed) ? helperWords.modelsUnlisted
        : failed ? `${failed.line}. ${helperWords.modelsWriteAnyway}` : null;
  return (
    <div className={styles.models}>
      <div className={styles.modelRow}>
        {roles.map(({ label, path, role }) => {
          const error = refused === path ? fieldWords[path] : undefined;
          const value = values[role];
          if ((listed || pending) && !writing[role]) {
            const names = listed ?? [];
            const options: SelectOption<string>[] = [
              ...(value === '' ? [{ value: '', label: helperWords.modelChoose, disabled: true }] : []),
              ...(value !== '' && !names.includes(value) ? [{ value, label: value }] : []),
              ...names.map((name) => ({ value: name, label: name })),
              ...(listed ? [{ value: writeChoice, label: helperWords.modelWrite }] : []),
            ];
            return (
              <div className={styles.modelField} key={role}>
                <Select
                  label={label}
                  loading={pending}
                  onValueChange={(chosen) => (chosen === writeChoice ? setWriting((current) => ({ ...current, [role]: true })) : onChange(role, chosen))}
                  options={options}
                  value={value}
                />
                {error ? <span className={styles.error} role="alert">{error}</span> : null}
              </div>
            );
          }
          return (
            <div className={styles.modelField} key={role}>
              <Field
                autoComplete="off"
                error={error}
                label={label}
                onChange={(event) => onChange(role, event.target.value)}
                spellCheck={false}
                value={value}
              />
              {listed ? (
                <Button className={styles.fromList} onClick={() => setWriting((current) => ({ ...current, [role]: false }))} size="sm" variant="ghost">
                  {helperWords.modelFromList}
                </Button>
              ) : null}
            </div>
          );
        })}
      </div>
      {line ? (
        <div className={styles.modelsLine}>
          <p className={failed ? styles.refusal : styles.note}>{line}</p>
          {failed && models ? (
            <Button loading={models.refetching} onClick={models.refetch} size="sm" variant="ghost">
              <RotateCw aria-hidden="true" size={14} />{helperWords.modelsAgain}
            </Button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
