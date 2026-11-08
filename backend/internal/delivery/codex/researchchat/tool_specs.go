package researchchat

import (
	"encoding/json"

	"canonkeeper/backend/internal/domain/agent"
)

// readToolSpecs are the closed schemas and Russian descriptions of the reading tools of the saga
// memory every chat turn offers.
var readToolSpecs = []agent.ToolSpec{
	{
		Namespace: toolNamespace, Name: "search",
		Description: "Поиск по абзацам саги. Возвращает общее число совпадений, распределение по главам (все главы с совпадениями по порядку книги) и сами совпадения со сниппетами. " +
			"Учитывает словоформы; ударения и «ё» не мешают. Можно передать несколько запросов сразу (синонимы, варианты имён).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"queries":{"type":"array","minItems":1,"maxItems":8,"items":{"type":"string","minLength":1,"maxLength":300},"description":"Запросы. В режиме any слова объединяются через ИЛИ; фразу можно взять в кавычки; слово* ищет по началу слова."},` +
			`"match":{"type":"string","enum":["any","all","phrase","substring"],"description":"any (по умолчанию): хотя бы одно слово, выше — абзацы, где совпало больше слов; all: все слова в одном абзаце; phrase: слова подряд; substring: часть слова или точная строка (например «контроллер» найдёт «миниконтроллер»)."},` +
			`"order":{"type":"string","enum":["relevance","story"],"description":"relevance (по умолчанию) или story — по порядку книги, от ранних глав к поздним."},` +
			`"from":{"type":"string","description":"Первая глава диапазона, например \"530\"."},` +
			`"to":{"type":"string","description":"Последняя глава диапазона, например \"617\"."},` +
			`"books":{"type":"array","items":{"type":"integer","minimum":1},"description":"Ограничить томами."},` +
			`"limit":{"type":"integer","minimum":1,"maximum":150,"description":"Сколько совпадений показать на запрос (по умолчанию 30)."},` +
			`"offset":{"type":"integer","minimum":0,"description":"Сдвиг для следующей страницы совпадений."}` +
			`},"required":["queries"],"additionalProperties":false}`),
	},
	{
		Namespace: toolNamespace, Name: "read",
		Description: "Прочитать главу целиком или её часть. Абзацы пронумерованы (¶). Для контекста вокруг найденного абзаца используйте around и radius.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"chapter":{"type":"string","description":"Ключ главы: сквозной номер, например \"481\", или ключ из результатов search/chapters."},` +
			`"from":{"type":"integer","minimum":1,"description":"Первый абзац."},` +
			`"to":{"type":"integer","minimum":1,"description":"Последний абзац."},` +
			`"around":{"type":"integer","minimum":1,"description":"Абзац, вокруг которого читать."},` +
			`"radius":{"type":"integer","minimum":0,"maximum":40,"description":"Сколько абзацев до и после around (по умолчанию 4)."}` +
			`},"required":["chapter"],"additionalProperties":false}`),
	},
	{
		Namespace: toolNamespace, Name: "chapters",
		Description: "Список глав саги по порядку: ключ, том, название, число абзацев. detail=summary добавляет подробный пересказ главы; detail=card — пересказ, указания на время (сколько прошло, даты, сроки) и изменения состояний. " +
			"Пересказы построены моделью и служат навигацией; факты проверяйте в тексте через read.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"from":{"type":"string","description":"Первая глава диапазона."},` +
			`"to":{"type":"string","description":"Последняя глава диапазона."},` +
			`"books":{"type":"array","items":{"type":"integer","minimum":1},"description":"Ограничить томами."},` +
			`"title":{"type":"string","description":"Подстрока названия главы."},` +
			`"detail":{"type":"string","enum":["titles","summary","card"],"description":"titles (по умолчанию), summary или card."}` +
			`},"additionalProperties":false}`),
	},
	{
		Namespace: toolNamespace, Name: "glossary",
		Description: "Словарь саги, собранный из всех глав: персонажи и сущности (места, организации, семьи, народы, предметы, события, понятия и роды вещей, свойственные самой книге) — с синонимами, кратким описанием и списком глав, где они встречаются. " +
			"Без аргументов показывает типы и их количество. Для перечислений («все города», «все организации») выбирайте types; для конкретного имени — query.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"query":{"type":"string","description":"Подстрока имени, синонима или описания."},` +
			`"types":{"type":"array","maxItems":20,"items":{"type":"string"},"description":"Типы из списка, который даёт вызов без аргументов, например [\"город\",\"место\"]."},` +
			`"limit":{"type":"integer","minimum":1,"maximum":1000,"description":"Сколько записей показать (по умолчанию 300)."}` +
			`},"additionalProperties":false}`),
	},
	{
		Namespace: toolNamespace, Name: "entity",
		Description: "Досье персонажа или сущности из фактов всех глав под всеми её именами: возраст, раса, класс, уровень, ранг, титул, должность, статус, местонахождение, здоровье — значение на конец диапазона и история смен; навыки, предметы, снаряжение, отношения, знания и прочие свойства — различные значения с главами. " +
			"Отвечает одним вызовом на «сколько лет», «какой уровень к главе N», «что у него есть», «с кем в каких отношениях». at — значение на главу. Факты выписаны моделью по главам: важное проверяйте в тексте через read по ссылке.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"name":{"type":"string","minLength":1,"description":"Имя или синоним, как в glossary."},` +
			`"at":{"type":"string","description":"Глава, на которую нужны значения (включительно); по умолчанию — вся сага."},` +
			`"attributes":{"type":"array","maxItems":20,"items":{"type":"string"},"description":"Только эти свойства, с полной историей, например [\"возраст\",\"уровень\"]."}` +
			`},"required":["name"],"additionalProperties":false}`),
	},
	{
		Namespace: toolNamespace, Name: "timeline",
		Description: "Шкала времени сюжета: для каждой главы — день сюжета от начала саги (сумма промежутков между главами и длительностей глав из карточек), дата мира, фраза, с которой глава начинается во времени, и события главы с абзацами. " +
			"from и to вместе дают, сколько прошло между главами. Неизвестные промежутки считаются нулём и названы счётчиком — это нижняя оценка. query отбирает события («турнир», «день рождения»); события показываются для диапазона до 40 глав или по query.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"from":{"type":"string","description":"Первая глава диапазона."},` +
			`"to":{"type":"string","description":"Последняя глава диапазона."},` +
			`"query":{"type":"string","description":"Слова события, участника или времени."}` +
			`},"additionalProperties":false}`),
	},
	{
		Namespace: toolNamespace, Name: "threads",
		Description: "Сюжетные нити и арки саги: открытые вопросы и тайны, обещания, долги, конфликты, планы, подвешенные детали. Без level — реестр открытых нитей всей саги, важные первыми; level=all добавляет нити отдельных книг, не вошедшие в реестр, с пометкой «по книге, могло закрыться» (могли закрыться позже, проверяйте); part=arcs — арки по книгам. " +
			"who находит нити героя под всеми его именами, query — по словам, kind — по виду (тайна, обещание, конфликт, план, задание, ружьё, отношения, долг). Ответ называет, по какую главу собран слой и сколько глав новее. Нить — навигация, а не факт: важное проверяйте в тексте через read по ссылке.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"query":{"type":"string","maxLength":300,"description":"Слова, которые есть в нити или арке."},` +
			`"who":{"type":"string","maxLength":300,"description":"Имя героя или синоним, как в glossary."},` +
			`"kind":{"type":"string","maxLength":300,"description":"Вид нити, например \"тайна\"."},` +
			`"level":{"type":"string","enum":["all"],"description":"all — вместе с нитями книг, не вошедшими в реестр: для поиска подвешенной детали."},` +
			`"part":{"type":"string","enum":["arcs"],"description":"arcs — арки по книгам вместо нитей."}` +
			`},"additionalProperties":false}`),
	},
	{
		Namespace: toolNamespace, Name: "changes",
		Description: "Хронология изменений по персонажу, предмету, месту или чему угодно: что и в какой главе менялось (положение, здоровье, отношения, знания и тайны, владение, статус и звание, способности, договорённости и сроки), по порядку книги. Имя из словаря находит изменения и под другими именами записи.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"subject":{"type":"string","minLength":1,"description":"Имя или ключевое слово."},` +
			`"from":{"type":"string","description":"Первая глава диапазона."},` +
			`"to":{"type":"string","description":"Последняя глава диапазона."}` +
			`},"required":["subject"],"additionalProperties":false}`),
	},
	{
		Namespace: toolNamespace, Name: "scan",
		Description: "Исчерпывающий просмотр текста: модель читает каждую главу диапазона (по умолчанию всю сагу) и выписывает все места, относящиеся к задаче, включая упоминания мимоходом и описания без ключевых слов. " +
			"Один вызов за минуту просматривает часть саги и называет главу, с которой продолжить (from) с тем же task. Тоже может пропускать места. Используйте для «найди все…», для смысловых поисков и когда search не даёт уверенности.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"task":{"type":"string","minLength":3,"maxLength":500,"description":"Что искать, своими словами, например «все места, где герой упоминает отца»."},` +
			`"from":{"type":"string","description":"Первая глава диапазона."},` +
			`"to":{"type":"string","description":"Последняя глава диапазона."},` +
			`"books":{"type":"array","items":{"type":"integer","minimum":1},"description":"Ограничить томами."}` +
			`},"required":["task"],"additionalProperties":false}`),
	},
}

// memoryToolSpecs are the memory of the author every chat turn offers (specs/2026-11-01-writer-assistant-index.md, UC4).
var memoryToolSpecs = []agent.ToolSpec{
	{
		Namespace: toolNamespace, Name: "remember",
		Description: "Сохранить в память автора то, что автор сказал или одобрил и чего нет в тексте саги. Только его слова, не ваши выводы и не догадки. Действие видно автору в ленте («Запомнил: …», там его можно отменить), запись он правит на карте саги, «Что помнит помощник». Записывать может редактор проекта.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"kind":{"type":"string","enum":["plan","correction","style_rule","rejected"],"description":"plan — что автор задумал, в книге этого ещё нет; correction — поправка автора к фактам; style_rule — правило стиля; rejected — что автор отверг."},` +
			`"text":{"type":"string","minLength":1,"maxLength":8000,"description":"Что сказал автор, близко к его словам."},` +
			`"chapter":{"type":"string","description":"Ключ главы, если запись о конкретной главе."},` +
			`"name":{"type":"string","maxLength":200,"description":"Имя героя или название, если запись о нём."}` +
			`},"required":["kind","text"],"additionalProperties":false}`),
	},
	{
		Namespace: toolNamespace, Name: "memory",
		Description: "Прочитать память автора по этой саге: планы, поправки, правила стиля и отвергнутое, новые первыми, каждая запись подписана видом. Читайте перед тем, как писать или планировать главу. Планы — не события книги.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"query":{"type":"string","maxLength":200,"description":"Подстрока текста записи или имени."},` +
			`"kind":{"type":"string","enum":["plan","correction","style_rule","rejected"],"description":"Только записи этого вида."}` +
			`},"additionalProperties":false}`),
	},
}

// attachmentToolSpec reads the chapter of the author attached to a question of this chat, by paragraphs
// (specs/2026-11-07-chat-attach-chapter.md CA3.2): only the chat is offered it.
var attachmentToolSpec = agent.ToolSpec{
	Namespace: toolNamespace, Name: "attachment",
	Description: "Прочитать главу автора, приложенную к вопросу этого чата: текст автора, в книге его ещё нет; текст книги важнее. " +
		"Абзацы пронумерованы (¶), ссылка на место — [прил. ¶N](attachment:<номер>/N). За вызов — до 6 000 слов; длинную главу читайте дальше с from, который называет ответ.",
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"attachment":{"type":"integer","minimum":1,"maximum":999,"description":"Номер приложения в этом чате (из шапки вопроса); по умолчанию — последнее."},` +
		`"from":{"type":"integer","minimum":1,"description":"Первый абзац."},` +
		`"to":{"type":"integer","minimum":1,"description":"Последний абзац."}` +
		`},"additionalProperties":false}`),
}
