// SPDX-License-Identifier: AGPL-3.0-only
import type { Schema } from '../../../../shared/api';

export type Preset = Schema<'OwnAi'>['preset'];
export type Protocol = Schema<'OwnAi'>['protocol'];
export type Reasoning = Schema<'OwnAiAdvanced'>['reasoning'];
export type Currency = NonNullable<Schema<'OwnAiAdvanced'>['currency']>;
export type Reason = Schema<'AiReason'>;
export type Probe = Schema<'AiProbe'>;
export type Verdict = Schema<'AiVerdict'>;

/**
 * The words of the part «Помощник» (specs/2026-11-13-desk-helper-web.md; board 44·9, 44·25, 44·26, 44·36): the board's,
 * then the table Data of the spec; a word in neither is in its Decision log for the next batch of the boards.
 */
export const helperWords = {
  intro: 'Помощник будет отвечать в чате и читать книги саги через вашего провайдера ИИ — по вашему ключу.',
  introSends: 'Вопросы чата и текст глав, которые он читает, уйдут этому провайдеру. Учится ли он на ваших текстах и хранит ли их, решают его правила.',
  provider: 'Провайдер',
  providerChoose: 'Выберите провайдера',
  address: 'Адрес провайдера',
  // specs/2026-11-19-desk-trust.md TW4.2: an address of http to another computer.
  addressPlain: 'Адрес без https: текст пойдёт по сети без шифрования. Так можно только в своей домашней сети',
  protocol: 'Протокол',
  protocolHint: '«Определить» — приложение само узнает, как говорить с провайдером',
  key: 'Ключ',
  keyNone: 'Ключ не нужен',
  keySave: 'Сохранить ключ',
  keySaveAgain: 'Сохранить ещё раз',
  keyReplace: 'Заменить',
  keyRemove: 'Удалить',
  keyCancel: 'Отмена',
  keyHidden: '••••••••••••••••••••',
  keyOptional: 'Нужен, если провайдер его просит',
  keyRemoveTitle: 'Удалить ключ?',
  keyRemoveAlso: 'Помощник перестанет отвечать, пока вы не введёте ключ снова.',
  keyRemoved: 'Ключ удалён',
  keySaved: 'Ключ сохранён',
  keyNotKept: 'Ключ не сохранён: хранилище паролей системы не ответило. Если система спрашивала пароль — введите его и сохраните ключ ещё раз',
  keyInvalid: 'Ключ не подходит: в нём пробел, перенос строки или он длиннее 1 280 байт. Скопируйте его у провайдера заново',
  keyFailed: 'Ключ не сохранился. Попробуйте ещё раз',
  keyEmpty: 'Сначала вставьте ключ провайдера',
  keyOfAnother: (saved: string, chosen: string) => `Это ключ «${saved}». Для «${chosen}» нажмите «Заменить»`,
  chatModel: 'Модель для чата',
  cardsModel: 'Модель для чтения книг',
  modelChoose: 'Выберите модель',
  modelWrite: 'Другая — вписать название',
  modelFromList: 'Выбрать из списка',
  modelsUnlisted: 'Провайдер не отдаёт список моделей — впишите название модели',
  modelsAfterSave: 'Список моделей провайдера появится после «Сохранить» — или впишите название модели',
  modelsAfterKey: 'Список моделей провайдера появится, когда ключ будет сохранён',
  modelsAgain: 'Спросить список ещё раз',
  modelsWriteAnyway: 'Название модели можно вписать',
  advanced: 'Дополнительно',
  save: 'Сохранить',
  saved: 'Настройки помощника сохранены',
  nothingToSave: 'Всё сохранено',
  saveFailed: 'Настройки не сохранились. Попробуйте ещё раз',
  appliesAfterAnswer: 'Применится после ответа помощника',
  /**
   * The way from a refusal of the tier of the account to «Свой ИИ» of this part (specs/2026-11-15-desk-account-words-web.md AW4.1):
   * its name is here, where the author reads «ИИ».
   */
  connectOwn: 'Подключить свой ИИ',
  leaveTitle: 'Не сохранять?',
  leaveAction: 'Не сохранять',
  leaveLine: 'Изменения настроек помощника не будут сохранены.',
  loadFailed: 'Не открылись настройки помощника',
} as const;

export interface PresetEntry {
  value: Preset;
  label: string;
  /** The address the variant gives (backend/internal/domain/aisettings `presets`); null — the author writes it. */
  baseUrl: string | null;
  /** The variant answers only with the key of the author (NeedsKey); the local model takes none. */
  key: 'needed' | 'none' | 'optional';
}

/** The ready variants in the order of the list of the board 44·25. */
export const presetEntries: readonly PresetEntry[] = [
  { value: 'deepseek', label: 'DeepSeek', baseUrl: 'https://api.deepseek.com/v1', key: 'needed' },
  { value: 'openai', label: 'OpenAI', baseUrl: 'https://api.openai.com/v1', key: 'needed' },
  { value: 'ollama', label: 'Локальная модель (Ollama)', baseUrl: 'http://127.0.0.1:11434/v1', key: 'none' },
  { value: 'custom', label: 'Другой, совместимый с OpenAI', baseUrl: null, key: 'optional' },
];

export function presetOf(preset: Preset): PresetEntry {
  return presetEntries.find((entry) => entry.value === preset)!;
}

/** The protocol of «Другой»: «Определить · Responses · Chat Completions» (44·25). */
export const protocolOptions: readonly { value: Protocol; label: string }[] = [
  { value: 'auto', label: 'Определить' },
  { value: 'responses', label: 'Responses' },
  { value: 'chat', label: 'Chat Completions' },
];

/** «Рассуждения: Нет · Низкие · Средние · Высокие» (44·25). */
export const reasoningOptions: readonly { value: Reasoning; label: string }[] = [
  { value: 'none', label: 'Нет' },
  { value: 'low', label: 'Низкие' },
  { value: 'medium', label: 'Средние' },
  { value: 'high', label: 'Высокие' },
];

/** «Дополнительно» (44·25): the label of each field and the one line that says what it is for (HW1.5). */
export const advancedWords = {
  contextWindow: { label: 'Окно контекста, токенов', hint: 'Сколько текста модель видит за раз — как пишет провайдер. Для чата нужно не меньше 64 000' },
  maxOutput: { label: 'Предел ответа, токенов', hint: 'Самый длинный ответ модели. Для чтения книг нужно не меньше 16 000' },
  reasoning: { label: 'Рассуждения', hint: 'Чем больше модель рассуждает, тем точнее ответ, но дольше и дороже' },
  parallel: { label: 'Запросов одновременно', hint: 'Сколько запросов уходит провайдеру разом: больше — книги читаются быстрее, если провайдер это позволяет' },
  priceIn: { label: 'Цена за миллион токенов: вход', hint: '' },
  priceCache: { label: 'кэшированный вход', hint: 'Необязательно: если провайдер берёт за повторный вход меньше' },
  priceOut: { label: 'выход', hint: '' },
  currency: { label: 'Валюта', hint: '' },
  pricesHint: 'Цены нужны только для оценки стоимости чтения книг',
  priceNone: 'не указана',
} as const;

/** «Валюта»: «₽ — рубль · $ — доллар · ¥ — юань · € — евро» (board 44·25, «„Дополнительно“ · список валют»). */
export const currencyOptions: readonly { value: Currency; label: string }[] = [
  { value: 'RUB', label: '₽ — рубль' },
  { value: 'USD', label: '$ — доллар' },
  { value: 'CNY', label: '¥ — юань' },
  { value: 'EUR', label: '€ — евро' },
];

/**
 * The question before a change of the currency or the removal of the prices while a saga is read under a sum in money
 * (board 44·25, «Валюта меняется при согласии»; specs/2026-11-13-desk-consent-web.md RW3.1): how many by the server.
 */
export const pauseWords = {
  title: 'Чтение книг встанет на паузу: сумму остановки нужно будет назвать заново',
  line: (sagas: number) => `Это коснётся чтения ${sagas} ${sagas % 10 === 1 && sagas % 100 !== 11 ? 'саги' : 'саг'}.`,
  action: 'Сохранить',
} as const;

/** What a field the server or the page refused says, at the field (HW1.6). The paths are the JSON pointers of the server. */
export const fieldWords: Record<string, string> = {
  '/own/baseUrl': 'Адрес — http:// или https:// и имя сервера, без пробелов, имени и пароля, до 300 знаков',
  '/own/protocol': 'Выберите протокол',
  '/own/chatModel': 'Название модели — до 120 знаков, без переносов строки',
  '/own/cardsModel': 'Название модели — до 120 знаков, без переносов строки',
  '/own/advanced/contextWindow': 'Число от 8 000 до 2 000 000',
  '/own/advanced/maxOutput': 'Число от 1 000 до 200 000',
  '/own/advanced/reasoning': 'Выберите, сколько модель рассуждает',
  '/own/advanced/parallel': 'Число от 1 до 32',
  '/own/advanced/priceInPerMillion': 'Число от 0 до 1 000 000, не больше четырёх знаков после запятой',
  '/own/advanced/priceOutPerMillion': 'Число от 0 до 1 000 000, не больше четырёх знаков после запятой',
  '/own/advanced/priceCachedInPerMillion': 'Число от 0 до 1 000 000, не больше четырёх знаков после запятой',
  '/own/advanced/currency': 'Выберите валюту',
};

/** «Хранится в хранилище паролей системы» and the file of the data directory (44·25, «Ключ в файле»); before the key is kept — where it goes. */
export function keyStoreWords(store: Schema<'AiSettingsState'>['secretsStore'], kept: boolean) {
  if (store === 'file') {
    return kept ? 'Хранится в файле в папке данных без шифрования: в системе нет хранилища паролей' : 'Сохранится в файле в папке данных без шифрования: в системе нет хранилища паролей';
  }
  return kept ? 'Хранится в хранилище паролей системы' : 'Сохранится в хранилище паролей системы';
}
