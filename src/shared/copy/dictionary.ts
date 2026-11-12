/**
 * One thing — one word (specs/2026-11-11-ux2-dictionary.md DC1.1; table 4.4 of docs/diagnostics/ux-2026-11-10/01-whole.md
 * and the dictionaries of the fronts): what the author calls a thing, and the words the author never reads for it. The
 * guard `dictionary.test.ts` reads every saying of the author by this table — the strings of `src`, the letters of the
 * site and the table of the pages of the voice — and the e2e guard `no-device-words` reads the pages by it. The names of
 * the code, the fields of the API and the comments are not the author's reading and keep their words.
 */

/** A word the author does not read for a thing. */
export interface Hidden {
  /** The word as the table says it. */
  readonly says: string;
  /** Finds the word in a saying; never global, so it keeps no state between sayings. */
  readonly pattern: RegExp;
  /** A saying the word stands in: the guard proves by it that the word is still found (DC2.4). */
  readonly sample: string;
  /** Sayings with the same letters that are not the word: the guard keeps them allowed. */
  readonly allowed?: readonly string[];
  /** Only the places of these paths of the repository read it so; everywhere when absent. */
  readonly where?: readonly string[];
}

/** A row of the table: a thing, the word of the author for it, the words he does not read for it. */
export interface Word {
  readonly thing: string;
  readonly word: string;
  readonly hidden: readonly Hidden[];
}

/** The section of the chapter in work: its page and its data (specs/2026-11-07-writing-screen.md). */
const writing = ['src/features/writing/', 'src/entities/chapter-drafts/'];

/** The section «Рисунки» (specs/2026-11-10-ux6-drawings-section.md) and the drawings of the reader. */
const drawings = [
  'src/features/drawing/', 'src/entities/illustrations/', 'src/entities/illustration-access/', 'src/entities/drawings/',
  'src/features/manuscript/reader/illustrations/',
];

/** The rows of the plan (DC1.1) and of the slices of the waves 5–7, then the words of the guards they took over. */
export const dictionary: readonly Word[] = [
  {
    thing: 'имя продукта', word: 'CanonKeeper', hidden: [
      // One word in any case: «CANONKEEPER_…» is a name of the code.
      {
        says: '«Canon Keeper», «CANON KEEPER»', pattern: /Canon\s+Keeper/iu, sample: 'CANON KEEPER',
        allowed: ['CanonKeeper', 'CANONKEEPER_TEST_DATABASE_URL'],
      },
    ],
  },
  {
    thing: 'все книги одной истории', word: 'сага', hidden: [
      { says: '«проект»', pattern: /(?<!\p{L})проект(?:а|у|ом|е|ы|ов|ам|ами|ах)?(?!\p{L})/iu, sample: '1 книга в 1 проекте', allowed: ['проекция'] },
      { says: '«серия»', pattern: /(?<!\p{L})сери(?:я|и|ю|ей|ях|ям)(?!\p{L})/iu, sample: 'порядок книг в серии' },
    ],
  },
  {
    thing: 'место, где лежат книги', word: 'Книги', hidden: [
      { says: '«Мои книги»', pattern: /Мои книги|К моим книгам/iu, sample: 'К моим книгам' },
      { says: '«полка»', pattern: /(?<!\p{L})полк(?:а|и|е|у|ой|ах|ам|ами)(?!\p{L})/iu, sample: 'Подробнее на полке', allowed: ['полковник'] },
      { says: '«библиотека»', pattern: /(?<!\p{L})библиотек/iu, sample: 'Вся библиотека' },
    ],
  },
  {
    thing: 'открытая книга', word: 'читать, «Открыть в книге»', hidden: [
      { says: '«читалка»', pattern: /читалк/iu, sample: 'Открыть в читалке' },
      { says: '«Открыть в чтении», «Открыть чтение»', pattern: /Открыть (?:в )?чтени/iu, sample: 'Открыть в чтении' },
    ],
  },
  {
    thing: 'книга ещё не готова', word: 'помощник читает книгу → книга прочитана', hidden: [
      {
        says: '«карточки глав»', sample: 'Составляем карточки глав',
        pattern: /карточк\p{L}* глав|глав\p{L}* с карточк|(?<!\p{L})карточки (?:не )?(?:записал|останут|построен|готов)/iu,
        allowed: ['Карточка: Мара', 'Карточка героя'],
      },
      { says: '«этап»', pattern: /(?<!\p{L})этап/iu, sample: 'этап 3 из 4' },
      { says: '«очередь»', pattern: /(?<!\p{L})очеред/iu, sample: 'Отменить очередь' },
      { says: '«импорт»', pattern: /импорт/iu, sample: 'Импорт книги' },
      { says: '«индекс»', pattern: /(?<!\p{L})индекс/iu, sample: 'индексация главы' },
      { says: '«покрытие»', pattern: /(?<!\p{L})покрыти/iu, sample: 'покрытие 2 из 2' },
      {
        says: '«обработка»', pattern: /(?<!\p{L})обработк(?!\p{L}*\s+(?:персональных\s+)?данных)/iu, sample: 'Обработка саги',
        // The names of the legal documents are their names.
        allowed: ['Политика обработки персональных данных', 'согласие на обработку персональных данных', 'Политика обработки данных'],
      },
      {
        says: '«подготовка» как имя процесса', sample: 'Когда книга подготовится, спросите',
        pattern: /(?<!\p{L})[Пп]одготовк|[Пп]одготовит(?:ся|сь)|[Пп]одготовлен|(?:[Кк]ниг\p{L}*|[Сс]аг\p{L}*)(?: саги)? (?:ещё )?готов(?:ит|ят)ся/u,
        allowed: ['помощник подготовит описание', 'Описание можно подготовить уже сейчас.', 'Ответ на ваш вопрос готовится', 'Сага готова', 'Подготовь описание'],
      },
      { says: '«поиск по тексту»', pattern: /поиск по тексту/iu, sample: 'Готовим поиск по тексту' },
      { says: '«нити и арки»', pattern: /нити и арки|(?<!\p{L})арк(?:а|и|у|ой|ам|ами|ах)(?!\p{L})/iu, sample: 'Собираем нити и арки' },
    ],
  },
  {
    thing: 'кто отвечает, пишет, рисует', word: 'помощник', hidden: [
      { says: '«ИИ»', pattern: /(?<!\p{L})ИИ(?!\p{L})/u, sample: 'до действия ИИ' },
      { says: '«модель»', pattern: /(?<!\p{L})модел(?:ь|и|ью|ей|ям|ями|ях)(?!\p{L})/iu, sample: 'Что уйдёт в модель', allowed: ['Модельный ряд'] },
      { says: '«агент»', pattern: /(?<!\p{L})агент/iu, sample: 'Агент письма' },
      // With a capital letter «Художник» is the name of a tier.
      { says: '«художник» как действующее лицо', pattern: /(?<!\p{L})художник/u, sample: 'Так помощник описал рисунок художнику', allowed: ['на тарифах «Художник» и выше'] },
      {
        says: '«чат» как действующее лицо', sample: 'Чат отвечает по готовой части',
        pattern: /(?<!\p{L})[Чч]ату?\s+(?:отвечает|ответит|прочитал|читает|нашёл|найдёт|знает|видит|пишет|скажет|умеет|может|помнит)|доступн\p{L}*\s+чату/u,
        allowed: ['Чат саги', 'Спросить в чате'],
      },
      { says: '«голосовой соавтор»', pattern: /соавтор/iu, sample: 'Голосовой соавтор' },
    ],
  },
  {
    thing: 'кто или что в книге', word: 'герой, место, предмет, фракция', hidden: [
      { says: '«сущность»', pattern: /сущност/iu, sample: 'Все сущности' },
      // «Лист персонажа» is the name of a kind of picture, not the word of a list.
      {
        says: '«персонаж» как общее слово списка', pattern: /(?<![Лл]ист )(?<!\p{L})персонаж(?:и|ей|ам|ами|ах|а|у|ем|е)?(?!\p{L})/iu,
        sample: 'Персонажи', allowed: ['Лист персонажа'],
      },
    ],
  },
  {
    thing: 'к какой главе рисунок', word: 'глава рисунка', hidden: [
      { says: '«привязка»', pattern: /привязк/iu, sample: 'Изменить описание и привязку' },
    ],
  },
  {
    thing: 'рисунок до выбора / после', word: 'вариант / версия', hidden: [
      { says: '«слой»', pattern: /(?<!\p{L})(?:слой|слоя|слою|слоем|слое|слои|слоёв|слоев|слоям|слоями|слоях)(?!\p{L})/iu, sample: 'Сведения и слои' },
      { says: '«ветка»', pattern: /(?<!\p{L})ветк(?:а|и|е|у|ой|ах|ам)(?!\p{L})/iu, sample: 'версии ветки' },
      // «Плитки | Списком» is the way the list lies (specs/2026-11-10-ux6-drawings-section.md), not a name of a picture.
      { says: '«плитка»', pattern: /(?<!\p{L})плитк(?:а|е|у|ой|ам|ами|ах)(?!\p{L})/iu, sample: 'Каждый вариант — отдельной плиткой', allowed: ['Плитки'] },
      { says: '«постановка»', pattern: /постановк/iu, sample: 'Постановки вариантов' },
      { says: '«Версия» рисунка', pattern: /Верси[яиюей](?!\p{L})|Другая версия|версии рисунка/u, sample: 'Версия 2 из 3', allowed: ['Вариант 2 из 3'], where: drawings },
      { says: '«трактовка»', pattern: /[Тт]рактовк/u, sample: 'Трактовка 1', where: drawings },
    ],
  },
  {
    thing: 'что автор сказал помощнику', word: 'Что помнит помощник', hidden: [
      { says: '«Память»', pattern: /^Память$|«Память»/u, sample: 'Память', allowed: ['Что помнит помощник'] },
      { says: '«Память саги»', pattern: /Память саги/iu, sample: 'Память саги' },
      { says: '«Память автора»', pattern: /памят\p{L}* (?:автора|помощника)/iu, sample: 'Читает память автора' },
    ],
  },
  {
    thing: 'файлы автора о мире', word: 'Ваши материалы', hidden: [
      { says: '«Материалы автора»', pattern: /материал\p{L}* автора/iu, sample: 'Материалы автора' },
    ],
  },
  {
    thing: 'разговор текстом', word: 'чат, вопрос, ответ', hidden: [
      { says: '«диалог»', pattern: /диалог/iu, sample: 'История этого диалога' },
      {
        says: '«ход», «ходы помощника»', pattern: /(?<!\p{L})(?<!по )ход(?:а|у|ом|е|ы|ов|ам|ами|ах)?(?!\p{L})/iu,
        sample: 'ходы помощника на сегодня закончились', allowed: ['Облик по ходу саги', 'Уточнить по ходу работы…'],
      },
    ],
  },
  {
    thing: 'место в книге', word: 'абзац N; подпись места', hidden: [
      { says: '«¶»', pattern: /¶/u, sample: 'гл. 6 ¶37–38' },
      { says: '«фрагмент» как адрес', pattern: /фрагмент/iu, sample: 'Фрагмент · Вся глава' },
      { says: 'ключ главы', pattern: /(?<![\p{L}\d])b\d+c\d+(?![\p{L}\d])/u, sample: 'гл. b1c1' },
      // specs/2026-11-10-ux2-shell.md S7.2: the place the answer stands on is the place in the book.
      { says: '«источник»', pattern: /(?<!\p{L})источник/iu, sample: 'Источник ответа' },
    ],
  },
  {
    thing: 'тариф и его границы', word: 'Тариф; «осталось сегодня»', hidden: [
      { says: '«Кабинет»', pattern: /(?<!\p{L})[Кк]абинет/u, sample: 'Кабинет' },
      { says: '«лимит»', pattern: /лимит/iu, sample: 'дневной лимит' },
      { says: '«квота»', pattern: /(?<!\p{L})квот/iu, sample: 'квота исчерпана' },
      { says: '«исчерпан»', pattern: /исчерпан/iu, sample: 'квота исчерпана' },
      { says: '«расход»', pattern: /(?<!\p{L})расход(?:а|у|ом|е|ы|ов)?(?!\p{L})/iu, sample: 'Лимиты и расход', allowed: ['Расходится с карточкой'] },
    ],
  },
  {
    thing: 'разговор голосом', word: 'Поговорить, разговор', hidden: [
      { says: '«звонок»', pattern: /звон(?:ок|ка|ку|ком|ке|ки|ков|кам|ками|ках|ить|ит|ят|или|ил)(?!\p{L})/iu, sample: 'Этот браузер не умеет звонить' },
    ],
  },
  {
    thing: 'поиск', word: 'Поиск по книгам; «Найти» у местного', hidden: [
      { says: '«Глобальный поиск»', pattern: /Глобальн/iu, sample: 'Глобальный поиск' },
    ],
  },
  {
    // specs/2026-11-11-ux9-account.md AC1.2 (А-03, А-04, А-08): one word on sign-in, registration and recovery.
    thing: 'почта автора', word: 'почта (поле «Почта»: «Напишите почту», «В почте ошибка — проверьте адрес», «Проверьте почту и пароль»)', hidden: [
      { says: '«email», «e-mail»', pattern: /(?<![\p{L}\d])e-?mail(?![\p{L}\d])/iu, sample: 'Проверьте email и пароль.' },
      { says: '«адрес почты» в подписях входа', pattern: /адрес\p{L}* почты/iu, sample: 'Проверьте адрес почты', where: ['src/features/auth/', 'src/editions/site/account.tsx'] },
    ],
  },
  {
    thing: 'поддержка', word: 'Написать нам', hidden: [
      { says: '«Связаться с нами», «Написать в поддержку»', pattern: /Связаться с нами|Написать в поддержку/iu, sample: 'Связаться с нами' },
      // specs/2026-11-12-ux12-drawings.md DW4.4 (А-71): a refusal of the drawings has «Написать нам» by it, not words to look for it.
      { says: '«напишите в поддержку»', pattern: /напишите в поддержку/iu, sample: 'Если это ошибка, напишите в поддержку.', where: drawings },
    ],
  },
  {
    thing: 'первый экран и вход', word: '—', hidden: [
      { says: '«канон» в обращении к новому автору', pattern: /вести канон/iu, sample: 'Начните вести канон своей саги.' },
      { says: '«Память вашей саги»', pattern: /Память вашей саги/iu, sample: 'ПАМЯТЬ ВАШЕЙ САГИ' },
    ],
  },
  {
    thing: 'раздел рисунков', word: 'Рисунки', hidden: [
      { says: '«Рисование»', pattern: /Рисовани[еяю]/u, sample: 'Рисование' },
      { says: '«Галерея»', pattern: /(?<!\p{L})[Гг]алере[яиюе]/u, sample: 'все рисунки галереи саги' },
      { says: '«Иллюстрации» как имя раздела', pattern: /Иллюстрации саги|«Иллюстрации»/u, sample: 'на странице «Иллюстрации»', allowed: ['Иллюстрированная книга'] },
      { says: '«мастерская»', pattern: /[Мм]астерск/u, sample: 'Доработать в мастерской' },
    ],
  },
  {
    thing: 'версия рисунка, которую видят в книге', word: 'основной (вариант)', hidden: [
      { says: '«текущая версия»', pattern: /[Тт]екущ\p{L}* верси/u, sample: 'Текущая версия' },
      { says: '«выбранная» как имя', pattern: /^Выбранн(?:ая|ый|ое)$/u, sample: 'Выбранная' },
    ],
  },
  {
    thing: 'изменение части рисунка', word: 'правка', hidden: [
      { says: '«слой»', pattern: /(?<!\p{L})(?:слой|слоя|слою|слоем|слое|слои|слоёв|слоев|слоям|слоями|слоях)(?!\p{L})/iu, sample: 'Слой рисуется', allowed: ['Правки · 2'] },
      { says: '«кандидат»', pattern: /кандидат/iu, sample: 'Кандидаты правки' },
    ],
  },
  {
    thing: 'рисунок с другой точки', word: '«Сменить ракурс», «ракурс: …»', hidden: [
      { says: '«угол обзора», «вид из камеры»', pattern: /угол обзора|вид из камеры/iu, sample: 'Угол обзора' },
    ],
  },
  {
    thing: 'что нашла проверка главы', word: 'Замечания (у главы из файла — Правки)', hidden: [
      { says: '«Сверка» как имя вкладки', pattern: /(?<!\p{L})Сверк[аиу](?!\p{L})/u, sample: 'Вычитка: 5 — во вкладке «Сверка»', where: writing },
      { says: '«находки»', pattern: /(?<!\p{L})находк/iu, sample: 'Все находки', where: writing },
      { says: '«Вопрос к главе»', pattern: /Вопрос к главе/iu, sample: 'Вопрос к главе' },
    ],
  },
  {
    thing: 'правка вычитки / отличие в файле Word', word: 'правка / исправление', hidden: [
      { says: '«.docx с правками»', pattern: /\.docx с правками/iu, sample: 'Скачать .docx с правками' },
      { says: '«бесспорные»', pattern: /бесспорн/iu, sample: 'Принять бесспорные' },
    ],
  },
  {
    // specs/2026-11-12-ux12-writing.md WR3.2 (J-60): what the check says of a sentence and of a name new to the book.
    thing: 'замечание проверки главы', word: '«Казённый оборот: скажите проще, глаголом», «Новое имя «…»: в книге до этого места его нет»', hidden: [
      { says: '«канон»', pattern: /(?<!\p{L})канон/iu, sample: 'станет каноном вместе с главой', where: writing },
      { says: '«калька»', pattern: /(?<!\p{L})кальк(?:а|и|е|у|ой)(?!\p{L})/iu, sample: 'канцелярит или калька', allowed: ['калькулятор'] },
      { says: '«канцелярит»', pattern: /(?<!\p{L})канцелярит/iu, sample: 'Канцелярит: скажите глаголом' },
    ],
  },
  {
    thing: 'совет проверки по ремеслу', word: 'Совет по главе', hidden: [
      { says: '«ремесло», «слой C»', pattern: /ремесл|слой C/iu, sample: 'Вопросы ремесла' },
    ],
  },
  {
    thing: 'удалённое на 7 дней', word: 'Недавно удалённые', hidden: [
      { says: '«Корзина»', pattern: /(?<!\p{L})[Кк]орзин/u, sample: 'Корзина' },
      // A zip with a book is an archive too: «Распакуйте архив» is about the file.
      { says: '«архив»', pattern: /(?<!\p{L})Архив(?!\p{L})|(?<!\p{L})в архив(?!\p{L})/u, sample: 'Убрать в архив', allowed: ['Распакуйте архив', 'В архиве должен быть один файл .fb2'] },
      // specs/2026-11-10-ux2-patterns-actions.md UC3: one phrase of a deletion, `deletionLines`.
      {
        says: 'обещания удаления помимо `deletionLines`', pattern: /без возможности восстановления|Отменить удаление нельзя|удалятся навсегда|уйдёт в «Недавно удалённые»/u,
        sample: 'Чат удалится без возможности восстановления',
      },
    ],
  },
  {
    thing: 'добавление книг', word: 'Добавить книги; «Порядок томов»; «Как разобралась книга»', hidden: [
      { says: '«очередь»', pattern: /(?<!\p{L})очеред/iu, sample: 'Очередь загрузки' },
      { says: '«импорт»', pattern: /импорт/iu, sample: 'Импорт книги' },
      { says: '«передача»', pattern: /(?<!\p{L})передач/iu, sample: 'Передача картинки' },
      { says: '«предпроверка»', pattern: /предпровер/iu, sample: 'Предпроверка файла' },
      { says: '«автозапуск»', pattern: /автозапуск/iu, sample: 'Автозапуск следующего тома' },
    ],
  },
  {
    thing: 'глава, написанная помощником', word: '«Писать: N сцен», «Написана»', hidden: [
      { says: '«генерация», «сцена сгенерирована»', pattern: /генерац|сгенерир/iu, sample: 'Сцена сгенерирована' },
      { says: '«Job»', pattern: /(?<!\p{L})Job(?!\p{L})/u, sample: 'Job письма' },
    ],
  },
  {
    thing: 'положение книги на полке', word: 'Книга N (место среди книг саги)', hidden: [
      { says: '«том №», «порядковый номер»', pattern: /(?<!\p{L})[Тт]ом\p{L}*\s*№|порядков\p{L}* номер/iu, sample: 'Том №2' },
    ],
  },
  {
    thing: 'страница или блок загружается', word: 'форма того, что придёт (`SkeletonArea`); у нажатой кнопки — её `loading`', hidden: [
      // specs/2026-11-10-ux2-patterns-states.md PS3.1, PS3.2.
      { says: '«Загружаем», «Загрузка…», «Проверяем»', pattern: /Загружаем|Загрузка[^\n'"`<]{0,40}…|Проверяем/u, sample: 'Загрузка…', allowed: ['Загрузка книг на сегодня закончилась'] },
    ],
  },
  {
    thing: 'адрес почты не подтверждён', word: 'одна причина словами `src/shared/session/account-session.ts`', hidden: [
      // specs/2026-11-10-ux1-shell.md SH4.1: the old words of each section.
      {
        says: 'прежние причины разделов', sample: 'Подтвердите адрес почты, чтобы загружать книги и задавать вопросы.',
        pattern: /[Чч]тобы загружать книги и задавать вопросы|[Кк]ниги загрузить нельзя|[Зз]адавать вопросы о саге нельзя|[Чч]тобы рисовать, подтвердите|[Чч]тобы ввести ключ|[Ии] пример откроется|Подтвердите адрес почты|[Пп]ока адрес не подтверждён/u,
        allowed: ['После регистрации подтвердите адрес почты — дальше три шага.'],
      },
    ],
  },
  {
    thing: 'устройство сервиса', word: 'дело автора: книга, глава, рисунок, вопрос', hidden: [
      // specs/2026-10-28-writer-first-success.md R5.1.
      { says: '«поручение»', pattern: /поручени/iu, sample: 'Поручение выполнено' },
      { says: '«рабочее место», «рабочая область»', pattern: /рабоч\p{L}* (?:мест|област)/iu, sample: 'Переключение рабочей области' },
      { says: '«ревизия»', pattern: /ревизи/iu, sample: 'Описание записано: ревизия 2.' },
    ],
  },
  {
    thing: 'тон подписи (П-20)', word: 'к автору на «вы», помощник — в третьем лице, без слов отчёта', hidden: [
      { says: '«выдан администратором», «фактические счётчики»', pattern: /выдан\p{L}* администратор|фактическ\p{L}* счётчик/iu, sample: 'Тариф выдан администратором' },
    ],
  },
  {
    thing: 'кто работает в «Письме»', word: 'помощник', hidden: [
      // specs/2026-11-10-ux1-writing.md UW6.1: the work of the section is the helper's, not the section's or the admin's.
      { says: '«администратор»', pattern: /администратор/iu, sample: 'Обратитесь к администратору', where: writing },
      { says: '«переписки выделенного»', pattern: /переписки выделенного/iu, sample: 'Переписки выделенного на сегодня закончились' },
      {
        says: '«письмо» как исполнитель', sample: 'Письмо не настроено',
        pattern: /письмо (?:возьм|заполн|постав|трижды|не настроено|глав)|идёт письмо|решило письмо|следующем письме|после письма/iu, where: writing,
      },
    ],
  },
  {
    thing: 'план главы и вопросы к автору', word: '«шаги сцены», «Чем кончается», «сюжетные линии», «от ответа зависит план», «возьму этот»', hidden: [
      // specs/2026-11-11-ux5-decision-sheet.md DSH1.4 (table 4.6 of 06-writing-voice.md).
      { says: '«биты»', pattern: /(?<!\p{L})бит(?:ы|ов)?(?!\p{L})/iu, sample: 'Биты сцены', where: writing },
      { says: '«итог (да, но)»', pattern: /итог \(|да, но|нет, и вдобавок|да, с ценой/iu, sample: 'Итог (да, но)', where: writing },
      { says: '«держит план»', pattern: /держит план/iu, sample: 'Ответ держит план', where: writing },
      { says: '«версия плана»', pattern: /версия плана/iu, sample: 'Версия плана 1' },
      { says: '«нити — продвигает»', pattern: /продвига|выплачива|(?<!\p{L})нит(?:ь|и)(?!\p{L})/iu, sample: 'Нити: Мара — продвигает', where: writing },
      { says: '«место главы»', pattern: /место главы/iu, sample: 'Место главы не выбрано', where: writing },
      { says: '«по умолчанию», «советую»', pattern: /по умолчанию|советую/iu, sample: 'По умолчанию — первый', where: writing },
      { says: '«Нужно ваше решение»', pattern: /Нужно ваше решение/u, sample: 'Нужно ваше решение', where: writing },
      { says: '«Кто в кадре»', pattern: /Кто в кадре/u, sample: 'Кто в кадре' },
    ],
  },
  {
    thing: 'рисунок, по которому рисуются следующие', word: '«облик героя»; у картинки-примера — «Пример»', hidden: [
      // specs/2026-11-11-ux6-drawing-flow.md DF5.1 (table 4.5 of 05-drawing.md).
      // specs/2026-11-12-ux12-drawings.md DW4.3: the feed of the helper and the cards of drawings in the chat say «пример» too.
      {
        says: '«образец»', pattern: /[Оо]браз(?:ец|ц)/u, sample: 'Сделать образцом', allowed: ['Сохранить облик', 'Образ героя'],
        where: [...drawings, 'src/features/chat/feed/draw-actions.ts', 'src/features/chat/illustrations/'],
      },
    ],
  },
  {
    thing: 'имя и текст рисунка', word: '«Название», «Описание»', hidden: [
      { says: '«Название рисунка», «Подпись»', pattern: /Название рисунка|^Подпись$/u, sample: 'Название рисунка', where: drawings },
    ],
  },
  {
    thing: 'своя картинка автора', word: '«моя картинка», «Загрузить свою картинку»', hidden: [
      { says: '«Своё изображение», «Своя картинка с компьютера»', pattern: /Своё изображение|Своя картинка с компьютера/u, sample: 'Своё изображение', where: drawings },
    ],
  },
  {
    thing: 'форма рисунка', word: '«Формат»; «сам подберёт»', hidden: [
      { says: '«Формат кадра», «подберёт помощник», «По виду»', pattern: /Формат кадра|подберёт помощник|По виду/u, sample: 'Формат кадра', allowed: ['Формат', 'сам подберёт'], where: drawings },
    ],
  },
  {
    thing: 'что из рисунка увидит помощник', word: '«Что увидит помощник»', hidden: [
      { says: '«уйдёт в модель»', pattern: /уйдёт в модель/u, sample: 'Что уйдёт в модель', where: drawings },
    ],
  },
  {
    // specs/2026-11-12-ux11-dark-core.md TH1.2, TH1.3: the theme is the whole site's, the background is the sheet's.
    thing: 'фон листа текста в книге и в «Письме»; тема всего сайта', word: '«Фон текста»; «Тема»', hidden: [
      { says: '«Тема чтения»', pattern: /Тем[аыуе] чтения/iu, sample: 'Тема чтения' },
    ],
  },
  {
    // specs/2026-11-12-desk-window.md WN3.2, WN3.3: the app before its helper is connected — one line of the reading, the
    // same reason at the field of the chat (the words of `waitingLine` of the copy of the reading).
    thing: 'помощник приложения ещё не подключён', word: '«Помощник ещё не подключён. Читать книги и искать по ним можно уже сейчас»', hidden: [
      { says: '«не настроен», «отключён» о помощнике', pattern: /помощник\p{L}*\s+(?:не\s+настроен|отключ)/iu, sample: 'Помощник не настроен' },
    ],
  },
];

/** Every hidden word of the table, once by what it says. */
export const hiddenWords: readonly Hidden[] = [...new Map(dictionary.flatMap((row) => row.hidden).map((word) => [word.says, word])).values()];

/** Whether a hidden word is read so at a path of the repository. */
export function readsAt(word: Hidden, path: string) {
  return !word.where || word.where.some((place) => path.startsWith(place));
}

/** The hidden words a saying of the author has, at a path of the repository. */
export function hiddenIn(saying: string, path: string): Hidden[] {
  return hiddenWords.filter((word) => readsAt(word, path) && word.pattern.test(saying));
}
