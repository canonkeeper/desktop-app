package authormaterials

// MapInstructions are the words of the one model call of an import of materials: it maps the columns of a table and
// names the paragraphs of the records of a section, nothing else (specs/2026-11-04-saga-map.md, UC9). A linked app sends
// them with a section as the step materials of the site (specs/2026-11-13-desk-proxy.md PX2).
const MapInstructions = `Ты размечаешь раздел файла, в котором автор книги собрал свои материалы: навыки, места, персонажей, предметы, фракции.
Верни только JSON без пояснений.

Если раздел — таблица (строки с номерами столбцов): {"kind":"<тип>","header":<первая строка — заголовки столбцов: true или false>,"name":<номер столбца с названием записи>,"description":<номер столбца с описанием или null>}.

Если раздел — абзацы: {"kind":"<тип>","items":[{"name":"<имя записи дословно из текста>","kind":"<тип>","from":<номер первого абзаца о ней>,"to":<номер последнего абзаца о ней>}]}.
Запись — отдельная сущность с именем, о которой абзацы что-то сообщают. Обычное повествование без такой сущности — не запись.

<тип> — одно из: skill (навык, умение, заклинание), place (место), character (персонаж), item (предмет), faction (фракция, организация), other (другое). Тип таблицы подскажет название раздела и столбцов.
Не выдумывай имён и не добавляй записей, которых нет в тексте.`
