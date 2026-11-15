// SPDX-License-Identifier: AGPL-3.0-only
import { installingUpdateWords } from '../../../features/chat/research-chat-presentation';
import type { OperationResponse } from '../../../shared/api';
import { hiddenStripKey } from './hidden-strips';

export type AppUpdate = OperationResponse<'getAppUpdate', '200'>;

/**
 * The permanent page of the releases of the public repository (specs/2026-11-15-desk-updates-web.md UW2.3): the same one
 * the page of the start opens (`RELEASES_URL` of desktop/src-tauri/crates/core/src/words.rs; scripts/desktop-app.test.mjs
 * compares the two).
 */
export const releasesUrl = 'https://github.com/canonkeeper/desktop-app/releases';

/** The words of the slice that stand alone (Data of specs/2026-11-15-desk-updates-web.md). */
export const updateWords = {
  part: 'Обновления',
  switchLabel: 'Проверять новые версии при запуске и раз в день',
  dailyOff: 'Приложение само не узнаёт о новых версиях. Проверить можно кнопкой',
  checkNow: 'Проверить сейчас',
  checking: 'Проверка уже идёт',
  latest: 'У вас последняя версия',
  never: 'Ещё не проверялось',
  offline: 'Не удалось проверить, есть ли новая версия. Попробуйте позже',
  whatsNew: 'Что нового',
  install: 'Обновить и перезапустить',
  adminAsk: 'Система спросит пароль администратора',
  cancel: 'Отменить обновление',
  waiting: 'Идёт ответ помощника — обновлю, когда он закончится; ответ сохранится',
  manual: 'Эта сборка обновляется вручную: новая версия — на странице выпусков',
  releases: 'Открыть страницу выпусков',
  failed: 'Не получилось — попробуйте ещё раз',
  close: 'Закрыть',
  strip: 'Обновление CanonKeeper',
  stripWaiting: 'Идёт ответ помощника — обновлю, когда он закончится',
  stripAdmin: 'спросит пароль администратора',
  openUpdates: 'Открыть обновления',
  hide: 'Скрыть до следующей версии',
  hideFailure: 'Скрыть сообщение',
  welcomeTitle: 'CanonKeeper на вашем компьютере',
  welcomeData: 'Книги и всё, что вы создадите, лежат на этом компьютере',
  welcomeOut: 'Из ваших книг и чатов наружу уходит только то, что нужно помощнику, — когда вы спрашиваете или он читает книги',
  // specs/2026-11-14-desk-journal.md JR3.5: the second line of what goes out.
  welcomeLog: 'Журнал в «Настройках» показывает каждую отправку вашего текста',
  welcomeCheck: 'При запуске и раз в день приложение спрашивает, есть ли новая версия',
  welcomeStart: 'Начать',
  welcomeFailed: 'Не получилось сохранить выбор — попробуйте ещё раз',
} as const;

/**
 * A version in the words of a line, in the case the line needs; without one — «новую версию» (UW2.3), as the page of the
 * start says «Устанавливаю новую версию».
 */
const versionCase = {
  nominative: (version: string | null) => (version ? `версия ${version}` : 'новая версия'),
  genitive: (version: string | null) => (version ? `версии ${version}` : 'новой версии'),
  accusative: (version: string | null) => (version ? `версию ${version}` : 'новую версию'),
};

/** The version the update is about: the later one the shell proved, while it holds it. */
function releaseOf(update: AppUpdate) {
  return update.available?.version || null;
}

/** «Последняя проверка — сегодня в 14:05» by the one function of the time of a check (UW2.5). */
export function lastCheckLine(at: string, now = Date.now()) {
  return `Последняя проверка — ${checkedWhen(at, now)}`;
}

const clock = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit' });
const date = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long' });
const dateOfAYear = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', year: 'numeric' });

/**
 * «сегодня в 14:05», «вчера в 14:05», older — «13 ноября в 14:05», of another year — «13 ноября 2025 г. в 14:05» (UW2.5):
 * on the clock of the computer of the author; «вчера» is the day before on the calendar, whatever its length in hours.
 */
export function checkedWhen(value: string, now = Date.now()) {
  const at = new Date(value);
  if (Number.isNaN(at.valueOf())) return '';
  const today = new Date(now);
  today.setHours(0, 0, 0, 0);
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);
  const time = clock.format(at);
  if (at.getTime() >= today.getTime()) return `сегодня в ${time}`;
  if (at.getTime() >= yesterday.getTime()) return `вчера в ${time}`;
  return `${(at.getFullYear() === today.getFullYear() ? date : dateOfAYear).format(at)} в ${time}`;
}

/** The items of the notes of a release (UW2.2): each line that is not empty, without a leading «- », «* », «• ». */
export function releaseNoteItems(notes: string) {
  return notes.split('\n').map((line) => line.trim().replace(/^[-*•]\s+/u, '').trim()).filter(Boolean);
}

/** The title of the window «Что нового» of a version; without one — «Что нового». */
export function whatsNewTitle(version: string | null) {
  return version ? `Что нового в версии ${version}` : updateWords.whatsNew;
}

/** A Linux package: the system asks the administrator when it installs (UB2.6). */
export function asksAdmin(update: Pick<AppUpdate, 'kind'>) {
  return update.kind === 'deb' || update.kind === 'rpm';
}

/** A button of a state: it stands, it stands closed with its reason, or it is not there (the table «Состояния части»). */
export type StateButton = 'shown' | { reason: string } | null;

/** The card «Доступна версия» of the part. */
export interface PartCard {
  title: string;
  notes: string;
  version: string | null;
  install: Exclude<StateButton, null>;
  adminAsk: boolean;
}

/** One state of the part «Обновления» (UW2.1): its words and its buttons, all from the fields of the server. */
export interface PartState {
  /** «Версия 1.2.0» of the program that runs; none for a build without a version. */
  version: string | null;
  /** The switch of the daily check: the value of the server; null — no switch (a build updated by hand). */
  daily: boolean | null;
  /** The line under the switch while the check is off, in any idle. */
  dailyOff: boolean;
  check: StateButton;
  /** The line of the state and how it looks. */
  status: { tone: 'latest' | 'never' | 'checked' | 'offline' | 'checking' | 'downloading' | 'waiting' | 'installing' | 'failure' | 'manual'; lines: string[] } | null;
  /** The last check, its own line beside the state. */
  lastCheck: string | null;
  card: PartCard | null;
  /** The part of the download, 0..1, by `percent`. */
  progress: number | null;
  cancel: boolean;
  /** «Обновить и перезапустить» of a failure, under its words. */
  retry: boolean;
}

/** The state of the part «Обновления» by the table «Состояния части» (UW2.1): the page counts and remembers nothing. */
export function partStateOf(update: AppUpdate, now = Date.now()): PartState {
  const release = releaseOf(update);
  const base: PartState = {
    version: update.version, daily: update.daily, dailyOff: false, check: 'shown', status: null, lastCheck: null, card: null,
    progress: null, cancel: false, retry: false,
  };
  const card = (install: PartCard['install']): PartCard | null => (update.available ? {
    title: `Доступна ${versionCase.nominative(release)}`, notes: update.available.notes, version: release, install, adminAsk: asksAdmin(update),
  } : null);
  const lastCheck = update.checkedAt ? lastCheckLine(update.checkedAt, now) : null;
  if (update.kind === 'none') {
    return { ...base, daily: null, check: null, status: { tone: 'manual', lines: [updateWords.manual] } };
  }
  const failure = (lines: string[], retry = true): PartState => ({ ...base, status: { tone: 'failure', lines }, retry });
  const unchanged = 'Приложение и данные не изменились';
  switch (update.phase) {
    case 'idle': {
      const idle = { ...base, dailyOff: !update.daily };
      switch (update.found) {
        case 'latest':
          return { ...idle, status: { tone: 'latest', lines: [updateWords.latest] }, lastCheck };
        case 'available':
          return { ...idle, card: card('shown') };
        case 'offline':
          return { ...idle, status: { tone: 'offline', lines: [updateWords.offline] }, lastCheck };
        default:
          return lastCheck
            ? { ...idle, status: { tone: 'checked', lines: [lastCheck] } }
            : { ...idle, status: { tone: 'never', lines: [updateWords.never] } };
      }
    }
    case 'checking':
      return {
        ...base, check: { reason: updateWords.checking }, status: { tone: 'checking', lines: ['Проверяю, есть ли новая версия…'] },
        card: card({ reason: updateWords.checking }),
      };
    case 'downloading':
      return {
        ...base, check: null, cancel: true, progress: (update.percent ?? 0) / 100,
        status: { tone: 'downloading', lines: [`Скачиваю ${versionCase.accusative(release)}: ${update.percent ?? 0} %`] },
      };
    case 'waiting_turn':
      return { ...base, check: null, cancel: true, status: { tone: 'waiting', lines: [updateWords.waiting] } };
    case 'installing':
      return {
        ...base, check: null,
        status: { tone: 'installing', lines: [`Устанавливаю ${versionCase.accusative(release)}: сохраняю данные и останавливаю помощника. Приложение закроется и откроется снова`] },
      };
    case 'download_failed':
      return failure([`Не удалось скачать ${versionCase.accusative(release)}. ${unchanged}`]);
    case 'verify_failed':
      return failure([`Скачанный файл ${versionCase.genitive(release)} не прошёл проверку и не установлен. ${unchanged}`]);
    case 'install_failed':
      return failure([
        `Не удалось установить ${versionCase.accusative(release)}. ${unchanged}`,
        ...(update.package ? [`Пакет новой версии сохранён: ${update.package} — его можно установить и самому`] : []),
      ]);
    case 'install_refused':
      if (update.refusal === 'unavailable') {
        return failure([[
          'Система не может спросить пароль администратора.',
          ...(update.package ? [`Пакет новой версии сохранён: ${update.package} — установите его сами`] : []),
        ].join(' ')], false);
      }
      return failure([[
        'Обновление не установлено: окно пароля администратора закрыто.',
        ...(update.package ? [`Пакет новой версии сохранён: ${update.package} — его можно установить и самому`] : []),
      ].join(' ')]);
  }
}

/** The strip under the top bar (UW3.1): what it says, its buttons and what its cross hides. */
export interface StripState {
  kind: 'available' | 'downloading' | 'waiting' | 'failure';
  /** The words; the per cent is in them while a download goes. */
  message: string;
  /** What a screen reader hears: the state, without the per cent (UW3.2). */
  announcement: string;
  progress: number | null;
  whatsNew: { version: string | null; notes: string } | null;
  install: boolean;
  adminAsk: boolean;
  cancel: boolean;
  openUpdates: boolean;
  /** What the cross hides: this version until the next one, or this failure until a new attempt; null — no cross (a work going on). */
  hides: string | null;
}

/** The strip of an update by the Data of UW3.1; none in the states it has no words for. */
export function stripStateOf(update: AppUpdate): StripState | null {
  if (update.kind === 'none') return null;
  const release = releaseOf(update);
  const none = { progress: null, whatsNew: null, install: false, adminAsk: false, cancel: false, openUpdates: false } as const;
  const failure = (phase: string, message: string): StripState => ({
    ...none, kind: 'failure', message, announcement: message, openUpdates: true, hides: hiddenStripKey.failure(phase, release),
  });
  switch (update.phase) {
    case 'idle': {
      if (update.found !== 'available' || !update.available) return null;
      const message = `Доступна ${versionCase.nominative(release)}`;
      return {
        ...none, kind: 'available', message, announcement: message, install: true, adminAsk: asksAdmin(update),
        whatsNew: update.available.notes.trim() ? { version: release, notes: update.available.notes } : null, hides: hiddenStripKey.version(release),
      };
    }
    case 'downloading': {
      const announcement = `Скачиваю ${versionCase.accusative(release)}`;
      return { ...none, kind: 'downloading', message: `${announcement}: ${update.percent ?? 0} %`, announcement, progress: (update.percent ?? 0) / 100, cancel: true, hides: null };
    }
    case 'waiting_turn':
      return { ...none, kind: 'waiting', message: updateWords.stripWaiting, announcement: updateWords.stripWaiting, cancel: true, hides: null };
    case 'download_failed':
      return failure(update.phase, `Не удалось скачать ${versionCase.accusative(release)}`);
    case 'verify_failed':
      return failure(update.phase, `Файл ${versionCase.genitive(release)} не прошёл проверку`);
    case 'install_failed':
      return failure(update.phase, `Не удалось установить ${versionCase.accusative(release)}`);
    case 'install_refused':
      return failure(`${update.phase}:${update.refusal ?? ''}`, update.refusal === 'unavailable'
        ? `Обновление до ${versionCase.genitive(release)} не установлено: система не может спросить пароль администратора`
        : `Обновление до ${versionCase.genitive(release)} не установлено: окно пароля администратора закрыто`);
    default:
      return null;
  }
}

/**
 * Why «Спросить» is closed while the app installs an update (UW3.4): the words of the chat for a question refused then
 * (`desktop.updating`), one owner; none in any other phase.
 */
export function askReasonOf(update: Pick<AppUpdate, 'phase'> | undefined) {
  return update?.phase === 'installing' ? installingUpdateWords : null;
}

/** «CanonKeeper обновлён до версии 1.3.0» after a new version started (UW3.3); the version is the one that runs. */
export function updatedLine(version: string | null, bridgesStopped = false) {
  const updated = `CanonKeeper обновлён до ${versionCase.genitive(version)}`;
  // The second sentence when the installer stopped the bridges of the programs of the author (specs/2026-11-15-desk-mcp.md
  // MC1.8): one line «обновлён», not two.
  return bridgesStopped ? `${updated}. Чтобы Claude Code и Codex снова читали саги, начните в них новую сессию` : updated;
}

/** The installation of a Linux package did not finish (UB2.6): with the package kept, or the way to the releases. */
export function unfinishedLine(unfinished: { version: string; package?: string }) {
  return unfinished.package
    ? `Установка версии ${unfinished.version} не закончилась. Пакет сохранён: ${unfinished.package} — установите его сами`
    : `Установка версии ${unfinished.version} не закончилась. Скачайте её на странице выпусков`;
}
