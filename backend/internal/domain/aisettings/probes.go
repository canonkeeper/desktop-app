// SPDX-License-Identifier: AGPL-3.0-only

package aisettings

import (
	"strings"
	"sync"
	"unicode/utf8"
)

// The words of the probes (specs/2026-11-13-desk-probes.md PB2.2) are made up here and nowhere else: no word of a book, a
// record or a chat of the author goes to the provider with a check. Their long text is put together from short made-up
// sentences about an island that exists in no book of the repository (TestTheProbesSayNoLineOfTheBooks).

// Tool is the function a probe offers the model: its name, what it is and the JSON schema of its arguments.
type Tool struct {
	Name        string
	Description string
	Parameters  string
}

// Question is what a probe asks: the instructions, the text of the author's turn and the tool, when it offers one.
type Question struct {
	Instructions string
	Input        string
	Tool         *Tool
}

const probeInstructions = "Это проверка связи приложения CanonKeeper с моделью. Отвечай по-русски, коротко и только тем, о чём просят."

const shortQuestion = "Проверка связи. Ответь одним словом: готово."

var pingTool = Tool{
	Name:        "probe_ping",
	Description: "Отметка проверки связи: вызови эту функцию, когда тебя об этом просят.",
	Parameters: `{"type":"object","properties":{"word":{"type":"string","description":"Слово из вопроса."},` +
		`"count":{"type":"integer","description":"Число из вопроса."}},"required":["word","count"],"additionalProperties":false}`,
}

const toolQuestion = "Проверка инструментов. Вызови функцию probe_ping: word — «маяк», count — 3. Ничего не пиши, только вызови функцию."

// longInputFact is the word of the fact at the start of the long text: the answer that holds it read the text whole. The
// filler never says it.
const longInputFact = "бакен"

const (
	longInputOpening  = "Проверка длинного текста. В самом начале — одна запись, которую нужно запомнить.\n\nЗапомни: шифр смотрителя маяка — «сорок семь бакенов».\n\n"
	longInputQuestion = "\n\nВопрос: какой шифр смотрителя маяка назван в самом начале этого текста? Ответь только шифром."
	// longInputCharacters is about 20 000 tokens of Russian text (PB2.3 (3)): 72 000 characters are 16 000 tokens for the
	// densest tokenizers of Russian and 30 000 for the sparsest.
	longInputCharacters = 72_000
	// calibrationCharacters is the text of the calibration: long enough for a tenth of a token per 1 000 characters.
	calibrationCharacters = 6_000
	// calibrationFrom starts the text of the calibration at other sentences than the long text.
	calibrationFrom = 1_000
)

const longOutputQuestion = "Проверка длинного ответа. Напиши летопись выдуманного острова Тихая Гавань: ровно 30 пронумерованных глав, " +
	"в каждой не меньше 100 слов. Пиши до тридцатой главы, не сокращай и не останавливайся раньше."

// The made-up sentences of the long text: when, who, what they did and what went on around.
var (
	fillerWhen = []string{
		"На рассвете", "К полудню", "Под вечер", "Ночью", "Ранним утром", "После обеда",
		"На закате", "Перед бурей", "В тихую погоду", "В туманный день", "В середине недели", "В первый день месяца",
	}
	fillerWho = []string{
		"рыбаки с южного берега", "старый паромщик", "дети смотрителя", "торговцы солью", "лодочники из устья", "пастухи с холмов",
		"плотники верфи", "сборщики водорослей", "почтальон острова", "две сестры-ткачихи", "кузнец с пристани", "учитель деревенской школы",
	}
	fillerDid = []string{
		"чинили сети у причала", "пересчитывали мешки с зерном", "красили борта лодок", "носили воду к маяку",
		"чистили медные фонари", "латали крышу склада", "собирали ракушки на отмели", "сушили рыбу на жердях",
		"выбирали камни для ограды", "спорили о погоде", "развешивали паруса", "грузили бочки на телегу",
	}
	fillerAround = []string{
		"а ветер гнал облака к северу.", "и море оставалось спокойным.", "пока чайки кружили над водой.",
		"и никто не вспоминал о вчерашнем.", "а в гавани звонил колокол.", "хотя дождь собирался с утра.",
		"и дым из труб стелился низко.", "пока прилив подбирался к дюнам.", "а на холме мычали коровы.",
		"и в окнах горел свет.", "хотя лёд ещё держался у скал.", "и запах смолы плыл над берегом.",
	}
)

// filler is made-up text of at least characters characters from the sentence from on: six sentences a paragraph.
func filler(from, characters int) string {
	var text strings.Builder
	count := 0
	for index := from; count < characters; index++ {
		if index > from {
			if (index-from)%6 == 0 {
				text.WriteString("\n\n")
				count += 2
			} else {
				text.WriteByte(' ')
				count++
			}
		}
		n := len(fillerWhen)
		sentence := fillerWhen[index%n] + " " + fillerWho[index/n%n] + " " + fillerDid[index/(n*n)%n] + ", " + fillerAround[index*7%n]
		text.WriteString(sentence)
		count += utf8.RuneCountInString(sentence)
	}
	return text.String()
}

// texts are the long texts of the probes, made once.
var texts = sync.OnceValues(func() (string, string) {
	return longInputOpening + filler(0, longInputCharacters) + longInputQuestion, filler(calibrationFrom, calibrationCharacters) + "\n\n"
})

// CalibrationCharacters is the characters of the text the calibration adds to the short question.
func CalibrationCharacters() int {
	_, calibration := texts()
	return utf8.RuneCountInString(calibration)
}

// Name is the name of the function the probe of a tool offers; empty for the other probes.
func (probe Probe) Name() string {
	if probe == ProbeTool {
		return pingTool.Name
	}
	return ""
}

// QuestionOf is what a probe asks (PB2.3). The calibration asks the short question after its text, so the difference of
// their usage is the text alone.
func QuestionOf(probe Probe) Question {
	long, calibration := texts()
	question := Question{Instructions: probeInstructions}
	switch probe {
	case ProbeTool:
		tool := pingTool
		question.Input, question.Tool = toolQuestion, &tool
	case ProbeLongInput:
		question.Input = long
	case ProbeLongOutput:
		question.Input = longOutputQuestion
	case ProbeCalibrate:
		question.Input = calibration + shortQuestion
	default:
		question.Input = shortQuestion
	}
	return question
}
