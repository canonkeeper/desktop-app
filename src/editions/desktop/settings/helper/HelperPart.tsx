// SPDX-License-Identifier: AGPL-3.0-only
import { useMutation } from '@tanstack/react-query';
import { useBlocker } from '@tanstack/react-router';
import { PlugZap, Save } from 'lucide-react';
import { useEffect, useId, useRef, useState } from 'react';
import { loadFailureWords, publicApiErrorMessage } from '../../../../entities/api-errors';
import { Button } from '../../../../ui/controls/Button';
import { Field } from '../../../../ui/controls/Field';
import { Select } from '../../../../ui/controls/Select';
import { ErrorBlock } from '../../../../ui/feedback/ErrorBlock';
import { SkeletonArea } from '../../../../ui/feedback/Skeleton';
import { useToasts } from '../../../../ui/feedback/useToastQueue';
import { SegmentedControl } from '../../../../ui/navigation/SegmentedControl';
import { Confirm } from '../../../../ui/overlays/Confirm';
import { helperPartKey, type SettingsPartProps } from '../part-props';
import { AdvancedFields, advancedPaths } from './AdvancedFields';
import { CheckBlock } from './CheckBlock';
import { checkWords } from './check-words';
import {
  useAfterKey, useAiCheck, useAiModels, useAiSettings, useCancelCheck, useSaveSettings, useStartCheck, type AiSettingsState, type HelperClient,
} from './helper-api';
import { changeOf, checkedChanged, draftOf, DraftFieldError, keyOf, pausesMoney, providerSaved, sameAsSaved, type HelperDraft } from './helper-draft';
import { fieldWords, helperWords, pauseWords, presetEntries, presetOf, protocolOptions, type Preset } from './helper-words';
import { KeyField } from './KeyField';
import { ModelFields, type ModelRole, type NoList } from './ModelFields';
import { ProgramsBlock } from './ProgramsBlock';
import { SourceChoice } from './SourceChoice';
import styles from './Helper.module.css';

/** Both models are named: the check asks them. */
function chosenModels(draft: HelperDraft) {
  const chosen = draft.preset ? draft.providers[draft.preset] : null;
  return Boolean(chosen?.chatModel.trim() && chosen.cardsModel.trim());
}

/** The JSON pointer of the field a refusal of the server names (400 `ai.settings_invalid`). */
function refusedField(error: unknown) {
  const path = (error as { problem?: { errors?: { path?: string }[] } } | null)?.problem?.errors?.[0]?.path;
  return path && Object.hasOwn(fieldWords, path) ? path : null;
}

/**
 * «Помощник» of «Настройки» (specs/2026-11-13-desk-helper-web.md HW1; board 44·9, 44·25, 44·36): the choice of the source
 * first — «Аккаунт CanonKeeper» or «Свой ИИ» (specs/2026-11-15-desk-account-link-web.md AW3.1) —; «Свой ИИ» — the provider,
 * its address and protocol, the key with its own button, the models of the chat and of the reading of the books from the
 * list of the provider, «Дополнительно» and one «Сохранить»; «Проверить связь» and its verdict (HW2).
 */
export function HelperPart({ api }: SettingsPartProps) {
  const settings = useAiSettings(api);
  // «Свой Claude или Codex» is a section of its own after the choice of the helper (specs/2026-11-15-desk-mcp.md MC3.1): it stands
  // while the settings load or fail, with any source, outside the form of «Свой ИИ».
  let helper;
  if (settings.isPending) helper = <SkeletonArea count={5} shape="lines" />;
  else if (settings.isError) {
    helper = <ErrorBlock {...loadFailureWords(settings.error)} onRetry={() => void settings.refetch()} retrying={settings.isRefetching} title={helperWords.loadFailed} />;
  } else helper = <SourceChoice api={api} renderOwn={(onUnsaved) => <OwnAi api={api} onUnsaved={onUnsaved} state={settings.data} />} state={settings.data} />;
  return (
    <div className={styles.helper}>
      {helper}
      <ProgramsBlock api={api} />
    </div>
  );
}

function OwnAi({ api, onUnsaved, state }: { api: HelperClient; onUnsaved: (held: boolean) => void; state: AiSettingsState }) {
  const toasts = useToasts();
  // Only what the author changed is the page's: the rest is what the server keeps (React Query owns it).
  const [edits, setEdits] = useState<HelperDraft | null>(null);
  const draft = edits ?? draftOf(state.settings);
  const [refused, setRefused] = useState<string | null>(null);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  // The write that waits for the answer to «Чтение книг встанет на паузу…» (RW3.1): «Сохранить» or «Проверить связь».
  const [asking, setAsking] = useState<'save' | 'check' | null>(null);
  // What the author typed in the field of the key, and whether he asked to replace the kept one, by the provider it was for:
  // switching keeps it (HW1.2), and only the key of the provider on the screen is saved, checked or asked about (HW1.3).
  const [typedKeys, setTypedKeys] = useState<Partial<Record<Preset, string>>>({});
  const [replacingOf, setReplacingOf] = useState<Partial<Record<Preset, boolean>>>({});
  const addressId = useId();
  const save = useSaveSettings(api);
  const afterKey = useAfterKey();
  // The key goes to the shell from the field only while it is written: it is not a variable of the write, so no state of
  // React Query holds it (HW1.3).
  const sendingKey = useRef('');
  const keyWrite = useMutation({ mutationFn: () => api.setAiKey({ key: sendingKey.current }) });
  const keyRemove = useMutation({ mutationFn: () => api.deleteAiKey() });
  const preset = draft.preset;
  const own = state.settings.source === 'own' ? state.settings.own : null;
  const dirty = !sameAsSaved(draft, state.settings);
  const typedKey = preset && keyOf(draft) !== 'none' ? typedKeys[preset] ?? '' : '';
  const keyTyped = typedKey.trim() !== '';
  const keyInPlace = state.keySaved || keyOf(draft) !== 'needed';
  const listedProvider = providerSaved(draft, own) && keyInPlace ? `${own!.preset} ${own!.baseUrl} ${own!.protocol}` : null;
  const models = useAiModels(api, listedProvider);
  const check = useAiCheck(api);
  const startCheck = useStartCheck();
  const stopCheck = useCancelCheck(api);
  const running = check.data?.state === 'running';

  // The choice of the source asks the same question before the account takes the place of the form.
  useEffect(() => {
    onUnsaved(dirty || keyTyped);
    return () => onUnsaved(false);
  }, [dirty, keyTyped, onUnsaved]);

  // A way out with unsaved changes asks «Не сохранять?» (HW1.6; specs/2026-11-10-ux2-patterns-actions.md PA2.2); another part
  // of «Настройки» is a way out too.
  const leaving = useBlocker({
    disabled: !dirty && !keyTyped,
    enableBeforeUnload: dirty || keyTyped,
    shouldBlockFn: ({ next }) => !(next.pathname === '/settings' && (next.search as { part?: string }).part === helperPartKey),
    withResolver: true,
  });

  function change(update: (current: HelperDraft) => HelperDraft, path?: string) {
    setEdits(update(draft));
    if (path && refused === path) setRefused(null);
  }

  function choosePreset(value: Preset) {
    change((current) => ({ ...current, preset: value }));
    // A refusal of the key stands at the key it refused, not at the field of another provider.
    if (keyWrite.isError) keyWrite.reset();
  }

  function changeProvider(field: 'baseUrl' | 'protocol' | ModelRole, value: string, path: string) {
    if (!preset) return;
    change((current) => ({ ...current, providers: { ...current.providers, [preset]: { ...current.providers[preset], [field]: value } } }), path);
  }

  /** The refusal of a field stands at it; one of «Дополнительно» opens it. */
  function refuse(path: string) {
    setRefused(path);
    if (Object.values(advancedPaths).includes(path)) setAdvancedOpen(true);
  }

  /**
   * «Сохранить»: the settings whole, or the refusal at its field; what the author wrote stays (HW1.6) — an edit made while
   * the write was on its way too: the form goes back to what the server keeps only if nothing changed since it was sent.
   */
  async function saveDraft() {
    const sent = edits;
    let change;
    try {
      change = changeOf(draft);
    } catch (error) {
      if (error instanceof DraftFieldError) {
        refuse(error.path);
        return false;
      }
      throw error;
    }
    try {
      await save.mutateAsync(change);
    } catch (error) {
      const path = refusedField(error);
      if (path) refuse(path);
      else toasts.push({ message: publicApiErrorMessage(error, helperWords.saveFailed), tone: 'error' });
      return false;
    }
    setEdits((current) => (current === sent ? null : current));
    setRefused(null);
    return true;
  }

  /**
   * «Сохранить ключ»: the key of the provider of the form — a provider not saved yet is saved first, so the list of its models
   * is asked of it. The key stays in the field until the shell keeps it, and nowhere else (HW1.3).
   */
  async function saveKey() {
    if (!keyTyped) return true;
    if (!providerSaved(draft, own) && !(await saveDraft())) return false;
    sendingKey.current = typedKey.trim();
    try {
      await keyWrite.mutateAsync();
    } catch {
      return false;
    } finally {
      sendingKey.current = '';
    }
    // Kept by the shell: the page forgets every key it was given.
    setTypedKeys({});
    setReplacingOf({});
    afterKey();
    return true;
  }

  /**
   * «Проверить связь» (HW2.1): what the author sees is checked — the unsaved settings and a typed key are saved first; a
   * refusal at a field stops it there. The check runs on its own and is read until it ends.
   */
  const checkStart = useMutation({
    mutationFn: async () => {
      if (dirty && !(await saveDraft())) return null;
      if (keyTyped && !(await saveKey())) return null;
      return api.startAiCheck();
    },
    onSuccess: (started) => { if (started) startCheck(started); },
    onError: (error) => {
      if ((error as { problem?: { code?: string } }).problem?.code === 'ai.check_running') {
        void check.refetch();
        return;
      }
      toasts.push({
        message: publicApiErrorMessage(error, checkWords.notStarted, { codes: { 'ai.check_unavailable': checkWords.unavailable, 'helper.not_connected': checkWords.noModels } }),
        tone: 'error',
      });
    },
  });
  const checkClosed = running || checkStart.isPending ? checkWords.busy
    : !chosenModels(draft) ? checkWords.noModels
      : keyOf(draft) === 'needed' && !state.keySaved && !keyTyped ? helperWords.keyEmpty : null;

  const noList: NoList | null = !preset ? null : !providerSaved(draft, own) ? 'provider' : keyInPlace ? null : 'key';
  // A change of the currency or the removal of the prices pauses the sagas read under a sum in money: the server counts them,
  // and the page asks first (specs/2026-11-13-desk-consent-web.md RW3.1).
  const asks = dirty && state.moneyStopSagas > 0 && pausesMoney(draft, own);
  const saveNow = () => {
    void saveDraft().then((saved) => { if (saved) toasts.push({ message: helperWords.saved, tone: 'success' }); });
  };
  const checkNow = () => checkStart.mutate();
  const chosen = preset ? draft.providers[preset] : null;

  return (
    <div className={styles.helper}>
      {state.settings.own === null ? (
        <div className={styles.intro}>
          <p className={styles.text}>{helperWords.intro}</p>
          <p className={styles.note}>{helperWords.introSends}</p>
        </div>
      ) : null}
      <Select
        label={helperWords.provider}
        onValueChange={(value) => { if (value) choosePreset(value); }}
        options={[...(preset ? [] : [{ value: '' as const, label: helperWords.providerChoose, disabled: true }]), ...presetEntries.map(({ label, value }) => ({ label, value }))]}
        value={preset ?? ''}
      />
      {preset && chosen ? (
        <>
          {preset === 'custom' ? (
            <>
              <Field
                autoComplete="url"
                error={refused === '/own/baseUrl' ? fieldWords['/own/baseUrl'] : undefined}
                id={addressId}
                inputMode="url"
                label={helperWords.address}
                onChange={(event) => changeProvider('baseUrl', event.target.value, '/own/baseUrl')}
                placeholder="https://llm.example.com/v1"
                spellCheck={false}
                value={chosen.baseUrl}
              />
              <div className={styles.choice}>
                <span aria-hidden="true" className={styles.choiceLabel}>{helperWords.protocol}</span>
                <SegmentedControl
                  label={helperWords.protocol}
                  onValueChange={(value) => changeProvider('protocol', value, '/own/protocol')}
                  options={protocolOptions}
                  value={chosen.protocol}
                />
                <span className={styles.note}>{helperWords.protocolHint}</span>
              </div>
            </>
          ) : (
            <Field id={addressId} label={helperWords.address} readOnly value={presetOf(preset).baseUrl ?? ''} />
          )}
          <KeyField
            error={keyWrite.error}
            onRemove={async () => {
              await keyRemove.mutateAsync();
              afterKey();
              toasts.push({ message: helperWords.keyRemoved, tone: 'success' });
            }}
            onReplace={(next) => { setReplacingOf((current) => ({ ...current, [preset]: next })); keyWrite.reset(); }}
            onSave={() => {
              void saveKey().then((kept) => { if (kept) toasts.push({ message: helperWords.keySaved, tone: 'success' }); });
            }}
            onType={(key) => { setTypedKeys((current) => ({ ...current, [preset]: key })); if (keyWrite.isError) keyWrite.reset(); }}
            preset={preset}
            removing={keyRemove.isPending}
            replacing={replacingOf[preset] === true}
            saving={keyWrite.isPending || (save.isPending && keyTyped)}
            state={state}
            typed={typedKey}
          />
          <ModelFields
            models={listedProvider ? {
              data: models.data, pending: models.isPending, refetch: () => void models.refetch(), refetching: models.isRefetching,
            } : null}
            noList={noList}
            onChange={(role, value) => changeProvider(role, value, role === 'chatModel' ? '/own/chatModel' : '/own/cardsModel')}
            preset={preset}
            refused={refused}
            values={{ chatModel: chosen.chatModel, cardsModel: chosen.cardsModel }}
          />
          <AdvancedFields
            onChange={(field, value) => change((current) => ({ ...current, advanced: { ...current.advanced, [field]: value } }), advancedPaths[field])}
            onToggle={() => setAdvancedOpen((open) => !open)}
            open={advancedOpen}
            refused={refused}
            values={draft.advanced}
          />
          <div className={styles.actions}>
            <Button
              disabledReason={dirty ? null : helperWords.nothingToSave}
              loading={save.isPending && !keyTyped}
              onClick={() => (asks ? setAsking('save') : saveNow())}
              variant="primary"
            >
              <Save aria-hidden="true" size={16} />{helperWords.save}
            </Button>
            <Button disabledReason={checkClosed} loading={checkStart.isPending} onClick={() => (asks ? setAsking('check') : checkNow())} variant="outline">
              <PlugZap aria-hidden="true" size={16} />{checkWords.check}
            </Button>
          </div>
          <CheckBlock
            changed={keyTyped || checkedChanged(draft, own)}
            check={check.data}
            onAgain={() => (asks ? setAsking('check') : checkNow())}
            onStop={() => stopCheck.mutate(undefined, { onSuccess: () => toasts.push({ message: checkWords.stopped, tone: 'info' }) })}
            preset={preset}
            report={state.check}
            stopping={stopCheck.isPending}
          />
        </>
      ) : null}
      {asking ? (
        <Confirm
          action={pauseWords.action}
          onCancel={() => setAsking(null)}
          onConfirm={() => { setAsking(null); if (asking === 'save') saveNow(); else checkNow(); }}
          title={pauseWords.title}
        >
          {pauseWords.line(state.moneyStopSagas)}
        </Confirm>
      ) : null}
      {leaving.status === 'blocked' ? (
        <Confirm action={helperWords.leaveAction} irreversible onCancel={() => leaving.reset?.()} onConfirm={() => leaving.proceed?.()} title={helperWords.leaveTitle}>
          {helperWords.leaveLine}
        </Confirm>
      ) : null}
    </div>
  );
}
