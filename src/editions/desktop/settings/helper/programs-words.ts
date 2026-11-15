// SPDX-License-Identifier: AGPL-3.0-only

/**
 * The words of the block «Свой Claude или Codex» of «Помощник» (specs/2026-11-15-desk-mcp.md Data «блок части «Помощник»»,
 * «Claude Desktop», «Claude Code и Codex», «состояние»): the reading of the sagas by the programs of the author.
 */
export const programsWords = {
  title: 'Свой Claude или Codex',
  what: 'Ваш Claude или Codex сможет искать по сагам и читать главы прямо из CanonKeeper, пока CanonKeeper открыт. Приложение при этом ничего не тратит.',
  border: 'Программа получит прочитанное и отправит своему провайдеру — как всё, о чём вы её спрашиваете. Читать сможет любая ваша программа на этом компьютере: все ваши саги, и новые тоже. Каждое чтение записано в журнале.',
  allow: 'Разрешить читать мои саги',
  whenOff: 'Если выключить, запущенные программы получат отказ при следующем чтении',
  notChanged: 'Не удалось изменить чтение для программ. Попробуйте ещё раз',
  copy: 'Скопировать',
  copied: 'Скопировано',
  notCopied: 'Не скопировалось. Выделите текст и скопируйте его сами',
  remove: 'Убрать',
  noProgram: 'Если Claude Desktop у вас нет, этот шаг не нужен.',
  desktopWindows: {
    title: 'Claude Desktop',
    save: 'Сохранить файл расширения',
    onlyHere: 'Файл подходит только для этого компьютера: в нём записан путь к данным CanonKeeper.',
    how: (file: string) =>
      `Файл ${file} сохранится в «Загрузки». В Claude Desktop откройте Settings → Extensions (или Settings → Desktop app → Extensions) → Advanced settings → Install Extension… и выберите этот файл. Или перетащите файл в окно Extensions.`,
    blocked: 'Если в окне установки нет кнопки Install, расширения запретила ваша организация.',
    always: 'Чтобы Claude не спрашивал разрешение на каждое чтение, выберите «Always allow».',
    then: 'Затем спросите Claude: «Какие саги есть в CanonKeeper?». В ответе будут названия ваших саг.',
    uninstall: 'Убрать: Settings → Extensions → CanonKeeper → Uninstall.',
    saved: (file: string) => `Файл расширения сохранён: Загрузки → ${file}`,
    saving: 'Файл расширения уже сохраняется',
    notSaved: 'Файл расширения не сохранился',
    retry: 'Повторить',
  },
  desktopLinux: {
    title: 'Claude Desktop (Ubuntu и Debian)',
    entry: 'Закройте Claude Desktop полностью. Откройте файл ~/.config/Claude/claude_desktop_config.json и добавьте в раздел "mcpServers" эту запись:',
    whole: 'Если файла или раздела нет, вставьте в файл это целиком:',
    then: 'Откройте Claude Desktop снова и спросите: «Какие саги есть в CanonKeeper?». В ответе будут названия ваших саг.',
    uninstall: 'Убрать: удалите запись "canonkeeper" из этого файла.',
  },
  commands: {
    linux: 'Выполните команду в терминале один раз.',
    windows: 'Откройте «Пуск», наберите PowerShell и откройте его. Вставьте команду правой кнопкой мыши и нажмите Enter — один раз.',
    then: (program: string) => `Затем закройте ${program} и откройте снова — и спросите: «Какие саги есть в CanonKeeper?». В ответе будут названия ваших саг.`,
    exists: 'Если в ответ на команду написано «already exists», сначала выполните команду «Убрать».',
  },
  seen: (programs: string) => `Обращались с запуска CanonKeeper: ${programs}`,
  seenOne: (name: string, time: string) => `${name} — в ${time}`,
  lastRead: (name: string, when: string) => `Последний раз читал: ${name}, ${when}`,
  noReads: 'В журнале нет чтений',
  oldExtension: 'Claude Desktop работает с расширением от прежней версии CanonKeeper: сохраните файл расширения и установите его заново',
  oldSession: (name: string) => `${name} работает с прежней версией CanonKeeper — начните в нём новую сессию`,
  failed: 'Не удалось включить чтение для программ на этом компьютере. Перезапустите CanonKeeper; если не помогло — напишите нам',
  noCommands: 'Команды подключения недоступны: переустановите CanonKeeper',
  loadFailed: 'Настройка чтения для программ не открылась',
  /** The window «Стереть все данные» when the reading was ever on (MC3.4): what stays in the settings of the programs. */
  erase: {
    commands: 'В Claude Code и Codex останется запись canonkeeper — уберите её командами:',
    /** The commands to take the entry away hold no path: they stand whether or not the app has the program of the bridge. */
    claudeRemove: 'claude mcp remove --scope user canonkeeper',
    codexRemove: 'codex mcp remove canonkeeper',
    windows: 'В Claude Desktop удалите расширение CanonKeeper: Settings → Extensions',
    linux: 'В Claude Desktop удалите запись "canonkeeper" из файла ~/.config/Claude/claude_desktop_config.json',
  },
} as const;

const clock = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit' });
const dayMonth = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long' });

/** «14:02». */
export function timeOf(at: string): string {
  return clock.format(new Date(at));
}

/** «сегодня в 14:05», «вчера в 14:05», «13 ноября в 14:05». */
export function whenOf(at: string, now = new Date()): string {
  const moment = new Date(at);
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  const same = (a: Date, b: Date) => a.toDateString() === b.toDateString();
  if (same(moment, now)) return `сегодня в ${timeOf(at)}`;
  if (same(moment, yesterday)) return `вчера в ${timeOf(at)}`;
  return `${dayMonth.format(moment).replace(/\s/gu, ' ')} в ${timeOf(at)}`;
}
