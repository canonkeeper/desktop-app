package sagamemory

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ScanInstructions ask the model to list every place of a text chunk that matches a task.
const ScanInstructions = `Ты просматриваешь фрагмент книжной саги (несколько глав подряд, абзацы пронумерованы ¶N) и выполняешь задачу поиска. Выписывай только места, которые прямо относятся к задаче, — в том числе упоминания мимоходом, сноски, синонимы и описания без ключевых слов. Не включай то, что относится к задаче лишь отдалённо или по общим словам. Ничего не выдумывай.
Для каждого места укажи item — короткое название найденного (для перечислений — название вещи/расы/персонажа в начальной форме; для сцен — короткая метка сцены).
Верни только JSON: {"found":[{"item":"название","chapter":"номер главы как в заголовке, например 481","p":номер абзаца,"quote":"дословная цитата до 160 символов","note":"кратко, почему относится"}]}
Если ничего нет — {"found":[]}.`

// ScanFinding is one place the scan model reported; Chapter is the chapter key it named.
type ScanFinding struct {
	Item    string `json:"item"`
	Chapter string `json:"chapter"`
	P       int    `json:"p"`
	Quote   string `json:"quote"`
	Note    string `json:"note"`
}

// ParseScan extracts the findings JSON from model output.
func ParseScan(output string) ([]ScanFinding, error) {
	start, end := strings.Index(output, "{"), strings.LastIndex(output, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("%w: no JSON object", ErrModelOutput)
	}
	var parsed struct {
		Found []ScanFinding `json:"found"`
	}
	if err := json.Unmarshal([]byte(output[start:end+1]), &parsed); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrModelOutput, err)
	}
	return parsed.Found, nil
}
