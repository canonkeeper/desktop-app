// SPDX-License-Identifier: AGPL-3.0-only
import { Copy, Download } from 'lucide-react';
import { loadFailureWords } from '../../../../entities/api-errors';
import { Button } from '../../../../ui/controls/Button';
import { Alert } from '../../../../ui/feedback/Alert';
import { ErrorBlock } from '../../../../ui/feedback/ErrorBlock';
import { SkeletonArea } from '../../../../ui/feedback/Skeleton';
import { useToasts } from '../../../../ui/feedback/useToastQueue';
import { Switch } from '../../Switch';
import { useProgramAccess, useSaveExtension, useUpdateProgramAccess, type ProgramAccess, type ProgramsClient } from './programs-api';
import { programsWords, timeOf, whenOf } from './programs-words';
import styles from './Helper.module.css';

/**
 * The block «Свой Claude или Codex» of «Помощник» (specs/2026-11-15-desk-mcp.md MC3.1–MC3.3, MC4.1; board 44·9): a section of
 * its own after «Откуда помощник», with its own query — it stands with any helper or none, while the settings of the helper
 * load or fail, and the form of «Свой ИИ» and its «Сохранить» do not take it. The words of the border stand at the switch
 * before it is on; the switch — the switch of the edition, as the daily check — acts at once and keeps its focus while the
 * request goes. On, it gives Claude Desktop first, then Claude Code and Codex, and under them who turned to the door since the
 * start and the last reading of the journal (board 44·46).
 */
export function ProgramsBlock({ api }: { api: ProgramsClient }) {
  const access = useProgramAccess(api);
  const update = useUpdateProgramAccess(api);
  const toasts = useToasts();
  return (
    <section aria-labelledby="programs-title" className={styles.programs}>
      <h3 className={styles.programsTitle} id="programs-title">{programsWords.title}</h3>
      <p className={styles.text}>{programsWords.what}</p>
      <p className={styles.note}>{programsWords.border}</p>
      {access.isPending ? <SkeletonArea count={2} shape="lines" /> : null}
      {access.isError ? (
        <ErrorBlock {...loadFailureWords(access.error)} onRetry={() => void access.refetch()} retrying={access.isRefetching} title={programsWords.loadFailed} />
      ) : null}
      {access.data ? (
        <>
          <Switch
            busy={update.isPending}
            checked={access.data.enabled}
            label={programsWords.allow}
            onChange={(enabled) => update.mutate(enabled, {
              onError: () => toasts.push({ message: programsWords.notChanged, tone: 'error' }),
            })}
          />
          {access.data.enabled ? <p className={styles.note}>{programsWords.whenOff}</p> : null}
          {access.data.enabled ? <Connected api={api} state={access.data} /> : null}
        </>
      ) : null}
    </section>
  );
}

function Connected({ api, state }: { api: ProgramsClient; state: ProgramAccess }) {
  if (state.failed) return <Alert tone="error">{programsWords.failed}</Alert>;
  return (
    <div className={styles.programsSteps}>
      {state.commands ? (
        <>
          {state.system === 'windows' ? <DesktopWindows api={api} file={state.extensionFile} /> : <DesktopLinux commands={state.commands} />}
          <ProgramCommands add={state.commands.claudeAdd} program="Claude Code" remove={state.commands.claudeRemove} windows={state.system === 'windows'} />
          <ProgramCommands add={state.commands.codexAdd} program="Codex" remove={state.commands.codexRemove} windows={state.system === 'windows'} />
        </>
      ) : <Alert tone="warning">{programsWords.noCommands}</Alert>}
      <StateLines state={state} />
    </div>
  );
}

/** Who turned to the door since the start, whose bridge is of an earlier version, and the last reading of the journal. */
function StateLines({ state }: { state: ProgramAccess }) {
  const old = state.programs.filter((program) => !program.matches);
  return (
    <div className={styles.intro}>
      {state.programs.length > 0
        ? <p className={styles.note}>{programsWords.seen(state.programs.map((program) => programsWords.seenOne(program.name, timeOf(program.at))).join(', '))}</p>
        : null}
      {old.map((program) => (
        <Alert key={program.name} tone="warning">{program.extension ? programsWords.oldExtension : programsWords.oldSession(program.name)}</Alert>
      ))}
      <p className={styles.note}>{state.lastRead ? programsWords.lastRead(state.lastRead.name, whenOf(state.lastRead.at)) : programsWords.noReads}</p>
    </div>
  );
}

function CopyText({ label, text }: { label: string; text: string }) {
  const toasts = useToasts();
  const copy = () => {
    void navigator.clipboard.writeText(text).then(
      () => toasts.push({ message: programsWords.copied, tone: 'success' }),
      () => toasts.push({ message: programsWords.notCopied, tone: 'error' }),
    );
  };
  return (
    <div className={styles.programsCopy}>
      <pre aria-label={label} className={styles.programsCode} tabIndex={0}>{text}</pre>
      <Button aria-label={`${programsWords.copy}: ${label}`} onClick={copy} size="sm" variant="outline"><Copy aria-hidden="true" size={14} />{programsWords.copy}</Button>
    </div>
  );
}

function DesktopWindows({ api, file }: { api: ProgramsClient; file: string | null }) {
  const save = useSaveExtension(api);
  const toasts = useToasts();
  const words = programsWords.desktopWindows;
  if (!file) return null;
  const saveFile = () => {
    save.mutate(undefined, {
      onSuccess: (name) => toasts.push({ message: words.saved(name), tone: 'success' }),
      onError: () => toasts.push({ action: <Button onClick={saveFile} size="sm" variant="ghost">{words.retry}</Button>, message: words.notSaved, tone: 'error' }),
    });
  };
  return (
    <div className={styles.program}>
      <h4 className={styles.programTitle}>{words.title}</h4>
      <p className={styles.note}>{programsWords.noProgram}</p>
      {/* One save at a time: while one goes — from this part or one left and come back to — the button keeps its place and its
          focus and says «уже сохраняется», as the file of a saga does. */}
      <div><Button disabledReason={save.going ? words.saving : null} onClick={saveFile} variant="outline"><Download aria-hidden="true" size={16} />{words.save}</Button></div>
      <p className={styles.note}>{words.onlyHere}</p>
      <p className={styles.text}>{words.how(file)}</p>
      <p className={styles.note}>{words.blocked}</p>
      <p className={styles.note}>{words.always}</p>
      <p className={styles.text}>{words.then}</p>
      <p className={styles.note}>{words.uninstall}</p>
    </div>
  );
}

function DesktopLinux({ commands }: { commands: NonNullable<ProgramAccess['commands']> }) {
  const words = programsWords.desktopLinux;
  if (!commands.desktopEntry || !commands.desktopFile) return null;
  return (
    <div className={styles.program}>
      <h4 className={styles.programTitle}>{words.title}</h4>
      <p className={styles.note}>{programsWords.noProgram}</p>
      <p className={styles.text}>{words.entry}</p>
      <CopyText label={`${words.title}: запись`} text={commands.desktopEntry} />
      <p className={styles.text}>{words.whole}</p>
      <CopyText label={`${words.title}: файл целиком`} text={commands.desktopFile} />
      <p className={styles.text}>{words.then}</p>
      <p className={styles.note}>{words.uninstall}</p>
    </div>
  );
}

function ProgramCommands({ add, program, remove, windows }: { add: string; program: 'Claude Code' | 'Codex'; remove: string; windows: boolean }) {
  return (
    <div className={styles.program}>
      <h4 className={styles.programTitle}>{program}</h4>
      <p className={styles.note}>{windows ? programsWords.commands.windows : programsWords.commands.linux}</p>
      <CopyText label={program} text={add} />
      <p className={styles.text}>{programsWords.commands.then(program)}</p>
      {program === 'Claude Code' ? <p className={styles.note}>{programsWords.commands.exists}</p> : null}
      <p className={styles.note}>{programsWords.remove}</p>
      <CopyText label={`${program}: ${programsWords.remove}`} text={remove} />
    </div>
  );
}
