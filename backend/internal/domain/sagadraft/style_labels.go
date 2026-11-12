package sagadraft

// styleLabels name the categories of style of the check of a chapter as the author reads them
// (specs/2026-11-11-ux9-writing-b.md WB2.1; J-60): the one table the page, the voice and the helper read — a note keeps its
// category, the words are said by its label. The categories are those of layers A and B of the check
// (domain/writingagent: check.go, check_prompts.go), whose test holds that each of them is named here.
var styleLabels = map[string]string{
	"стоп-лист": "Штамп", "штамп": "Штамп", "канцелярит": "Канцелярит", "калька": "Не по-русски",
	"не X, а Y": "Оборот «не…, а…»", "итог": "Мораль в конце", "названо": "Чувство названо, а не показано",
	"объяснение": "Лишнее объяснение", "вычурность": "Вычурно", "неловкое слово": "Неловкое слово", "конкретика": "Общие слова",
	"диалог": "Диалог-пересказ", "Он/Она подряд": "«Он» в начале фраз подряд", "повтор": "Повтор слова",
	"образ": "Повтор образа", "ритм": "Ритм фраз", "время": "Время глаголов", "объём": "Объём сцены",
}

// StyleLabel is the name of a category of style in the author's words; empty for a category the table does not know.
func StyleLabel(category string) string {
	return styleLabels[category]
}
