// SPDX-License-Identifier: AGPL-3.0-only
import { KeyRound, Trash2 } from 'lucide-react';
import { useId, useState } from 'react';
import { publicApiErrorMessage } from '../../../../entities/api-errors';
import { Button } from '../../../../ui/controls/Button';
import { Field } from '../../../../ui/controls/Field';
import { Confirm, deletionLines } from '../../../../ui/overlays/Confirm';
import { helperWords, keyStoreWords, presetOf, type Preset } from './helper-words';
import type { AiSettingsState } from './helper-api';
import styles from './Helper.module.css';

/** Why the key did not go to the shell, at the field (board 44·36; specs/2026-11-13-desk-secrets.md SE2.5). */
export function keyErrorWords(error: unknown) {
  return publicApiErrorMessage(error, helperWords.keyFailed, {
    codes: { 'ai.key_not_saved': helperWords.keyNotKept, 'ai.key_invalid': helperWords.keyInvalid },
  });
}

/** The shell did not keep the key: its button says «Сохранить ещё раз» (44·36). */
function notKept(error: unknown) {
  return (error as { problem?: { code?: string } } | null)?.problem?.code === 'ai.key_not_saved';
}

export interface KeyFieldProps {
  state: AiSettingsState;
  /** The provider of the form. */
  preset: Preset;
  /** What the author typed and has not saved yet: it stays here until the shell keeps it, and nowhere else (HW1.3). */
  typed: string;
  onType: (key: string) => void;
  /** The author asked to type another key over the kept one. */
  replacing: boolean;
  onReplace: (replacing: boolean) => void;
  saving: boolean;
  error: unknown;
  onSave: () => void;
  onRemove: () => Promise<void>;
  removing: boolean;
}

/**
 * «Ключ» (HW1.3; board 44·25, 44·36): a hidden field and its own button; a kept key is never shown — «Ключ сохранён» with
 * «Заменить» and «Удалить», the latter with a question; the line under it says where the shell keeps it. The local model
 * takes none: «Ключ не нужен».
 */
export function KeyField({ error, onRemove, onReplace, onSave, onType, preset, removing, replacing, saving, state, typed }: KeyFieldProps) {
  const id = useId();
  const [asking, setAsking] = useState(false);
  const [removeError, setRemoveError] = useState<unknown>(null);
  const entry = presetOf(preset);
  if (entry.key === 'none') return <p className={styles.text}>{helperWords.keyNone}</p>;
  const savedPreset = state.settings.own?.preset;
  const store = keyStoreWords(state.secretsStore, state.keySaved);
  if (state.keySaved && !replacing) {
    const another = savedPreset && savedPreset !== preset && entry.key === 'needed'
      ? helperWords.keyOfAnother(presetOf(savedPreset).label, entry.label) : null;
    return (
      <div className={styles.keyRow}>
        <Field autoComplete="off" hint={another ?? store} id={id} label={helperWords.key} readOnly type="password" value={helperWords.keyHidden} />
        <div className={styles.keyActions}>
          <Button onClick={() => onReplace(true)} size="sm" variant="outline">{helperWords.keyReplace}</Button>
          <Button onClick={() => { setRemoveError(null); setAsking(true); }} size="sm" variant="ghost">{helperWords.keyRemove}</Button>
        </div>
        {asking ? (
          <Confirm
            action={helperWords.keyRemove}
            actionIcon={<Trash2 aria-hidden="true" size={16} />}
            also={helperWords.keyRemoveAlso}
            busy={removing}
            error={removeError ? keyErrorWords(removeError) : undefined}
            irreversible
            onCancel={() => setAsking(false)}
            onConfirm={() => {
              onRemove().then(() => setAsking(false), (failure: unknown) => setRemoveError(failure));
            }}
            title={helperWords.keyRemoveTitle}
          >
            {deletionLines.gone}
          </Confirm>
        ) : null}
      </div>
    );
  }
  const written = typed.trim() !== '';
  const hint = entry.key === 'optional' ? `${helperWords.keyOptional}. ${store}` : store;
  return (
    <div className={styles.keyRow}>
      <Field
        autoComplete="off"
        error={error ? keyErrorWords(error) : undefined}
        hint={error ? undefined : hint}
        id={id}
        label={helperWords.key}
        onChange={(event) => onType(event.target.value)}
        spellCheck={false}
        type="password"
        value={typed}
      />
      <div className={styles.keyActions}>
        <Button disabledReason={written ? null : helperWords.keyEmpty} loading={saving} onClick={onSave} size="sm" variant="primary">
          <KeyRound aria-hidden="true" size={14} />{notKept(error) ? helperWords.keySaveAgain : helperWords.keySave}
        </Button>
        {replacing ? <Button onClick={() => { onType(''); onReplace(false); }} size="sm" variant="ghost">{helperWords.keyCancel}</Button> : null}
      </div>
    </div>
  );
}
