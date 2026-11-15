// SPDX-License-Identifier: AGPL-3.0-only

package programtools

import (
	"fmt"
	"strconv"
	"strings"
)

// The words of the bridge and of the door to the program of the author (specs/2026-11-15-desk-mcp.md Data «ответы моста»):
// none names a saga, none speaks of a turn of the chat, each is shorter than 2 000 characters (MC2.5, MC2.7).
const (
	WordsClosed     = "CanonKeeper закрыт или чтение саг выключено. Попросите автора открыть CanonKeeper и включить «Разрешить читать мои саги» в «Настройки → Помощник»"
	WordsNewSession = "CanonKeeper обновился. Попросите автора начать новую сессию в этой программе — тогда чтение саг вернётся"
	WordsReinstall  = "CanonKeeper обновился. Попросите автора установить расширение заново: в CanonKeeper «Настройки → Помощник» → «Сохранить файл расширения», затем Install Extension… в Claude Desktop"
	WordsRestart    = "CanonKeeper обновлён, но ещё работает прежняя версия. Попросите автора перезапустить CanonKeeper"
	WordsNoAnswer   = "CanonKeeper не ответил, повторите вызов"
	WordsBusy       = "CanonKeeper занят, повторите вызов"
	WordsClosing    = "CanonKeeper закрывается — повторите вызов, когда автор снова откроет CanonKeeper"
	WordsNoSagas    = "У автора нет саг в CanonKeeper"
	WordsSeveral    = "У автора несколько саг — вызовите sagas и назовите одну в аргументе saga"
	WordsNoSuch     = "Такой саги нет — список саг даёт sagas"
	WordsAmbiguous  = "Начало id совпало у двух саг — назовите больше знаков id из sagas"
	WordsNotReady   = "Текст этой саги ещё готовится в CanonKeeper — читать её можно будет через несколько минут. Скажите это автору"
	WordsChanged    = `Ответ изменился после прошлой страницы — прочитайте его заново с первой страницы, без "page"`
	WordsNotRecord  = "Чтение не записалось в журнал CanonKeeper, поэтому текст не отдан. Повторите вызов"
	// WordsPageForm is a value of page that is not the one the last line of a page gives.
	WordsPageForm = `Значение "page" не по форме: возьмите его дословно из последней строки прошлой страницы`
)

// WordsNoTool is a name the set does not have, with the names it has.
func WordsNoTool() string {
	return "Такого инструмента нет. Инструменты CanonKeeper: " + strings.Join(Names(), ", ")
}

// WordsNoPage is a page past the last.
func WordsNoPage(page, pages int) string {
	return fmt.Sprintf("Страницы %d нет: всего страниц %d", page, pages)
}

// PageLine is the first and the last line of a page of an answer of several pages: the next is the same call with page; the
// last page names no next.
func PageLine(page, pages int, mark string) string {
	if page >= pages {
		return fmt.Sprintf("Страница %d из %d.", page, pages)
	}
	return fmt.Sprintf(`Страница %d из %d. Следующая — тот же вызов с "page": "%d:%s"`, page, pages, page+1, mark)
}

// Mismatch are the words of a bridge and an app of different sets (MC1.4): the app is newer — a new session, or for the copy
// in the extension of Claude Desktop the extension anew; the bridge is newer — the app restarted.
func Mismatch(appVersion, bridgeVersion, kind string) string {
	if newer(bridgeVersion, appVersion) {
		return WordsRestart
	}
	if kind == KindExtension {
		return WordsReinstall
	}
	return WordsNewSession
}

// newer says the version a is later than b by its numbers; a version that is no numbers is never later.
func newer(a, b string) bool {
	left, okLeft := numbers(a)
	right, okRight := numbers(b)
	if !okLeft || !okRight {
		return false
	}
	for index := range max(len(left), len(right)) {
		var x, y int
		if index < len(left) {
			x = left[index]
		}
		if index < len(right) {
			y = right[index]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func numbers(version string) ([]int, bool) {
	version, _, _ = strings.Cut(strings.TrimPrefix(version, "v"), "-")
	if version == "" {
		return nil, false
	}
	var out []int
	for _, part := range strings.Split(version, ".") {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return nil, false
		}
		out = append(out, number)
	}
	return out, true
}
