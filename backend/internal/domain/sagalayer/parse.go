package sagalayer

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/sagamemory"
)

const maxTextRunes = 2000

// loose reads a text field of an answer whatever the model put there: a number becomes its digits,
// anything else but a string is empty. One odd field does not cost a paid answer.
type loose string

func (text *loose) UnmarshalJSON(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	switch typed := value.(type) {
	case string:
		*text = loose(typed)
	case float64:
		*text = loose(strconv.FormatFloat(typed, 'f', -1, 64))
	default:
		*text = ""
	}
	return nil
}

func (text loose) clean() string {
	trimmed := strings.TrimSpace(string(text))
	if utf8.RuneCountInString(trimmed) > maxTextRunes {
		trimmed = string([]rune(trimmed)[:maxTextRunes])
	}
	return trimmed
}

var refPattern = regexp.MustCompile(`(?i)¶\s*\d+|(b\d+c\d+|\d+)(?:\s*¶\s*(\d+))?`)

// ParseRefs reads the places in a reference of the model: a chapter key with or without a paragraph. A
// key the version does not know is dropped. The chapter is kept by its id: the key of a chapter shifts
// when a chapter is added before it, and the record stays right until the next build.
func ParseRefs(text string, version *sagamemory.Version) []Ref {
	var refs []Ref
	for _, match := range refPattern.FindAllStringSubmatch(text, -1) {
		if match[1] == "" {
			continue
		}
		chapter, ok := version.Find(match[1])
		if !ok {
			continue
		}
		paragraph, _ := strconv.Atoi(match[2])
		refs = append(refs, Ref{Chapter: chapter.ID, P: paragraph})
	}
	return refs
}

func jsonObject(output string) ([]byte, error) {
	start, end := strings.Index(output, "{"), strings.LastIndex(output, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("%w: no JSON object", sagamemory.ErrModelOutput)
	}
	return []byte(output[start : end+1]), nil
}

type answerThread struct {
	Thread    loose `json:"thread"`
	Kind      loose `json:"kind"`
	First     loose `json:"first"`
	Last      loose `json:"last"`
	Status    loose `json:"status"`
	ClosedHow loose `json:"closed_how"`
	Weight    loose `json:"weight"`
}

func (answer answerThread) thread(version *sagamemory.Version) Thread {
	return Thread{
		Text: answer.Thread.clean(), Kind: answer.Kind.clean(),
		First: ParseRefs(string(answer.First), version), Last: ParseRefs(string(answer.Last), version),
	}
}

// ParseBook reads the answer of the map call of one book; an answer without threads is rejected.
func ParseBook(output string, version *sagamemory.Version) (Book, error) {
	body, err := jsonObject(output)
	if err != nil {
		return Book{}, err
	}
	var raw struct {
		Arcs []struct {
			Title   loose `json:"title"`
			From    loose `json:"from"`
			To      loose `json:"to"`
			Gist    loose `json:"gist"`
			Outcome loose `json:"outcome"`
		} `json:"arcs"`
		Threads *[]answerThread `json:"threads"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Book{}, fmt.Errorf("%w: %v", sagamemory.ErrModelOutput, err)
	}
	if raw.Threads == nil {
		return Book{}, fmt.Errorf("%w: no threads", sagamemory.ErrModelOutput)
	}
	var book Book
	for _, arc := range raw.Arcs {
		if arc.Title.clean() == "" {
			continue
		}
		parsed := Arc{Title: arc.Title.clean(), Gist: arc.Gist.clean(), Outcome: arc.Outcome.clean()}
		if refs := ParseRefs(string(arc.From), version); len(refs) > 0 {
			parsed.From = refs[0]
		}
		if refs := ParseRefs(string(arc.To), version); len(refs) > 0 {
			parsed.To = refs[len(refs)-1]
		}
		book.Arcs = append(book.Arcs, parsed)
	}
	for _, answer := range *raw.Threads {
		if answer.Thread.clean() == "" {
			continue
		}
		thread := answer.thread(version)
		thread.Closed = answer.Status.clean() == "closed"
		thread.ClosedHow = answer.ClosedHow.clean()
		book.Threads = append(book.Threads, thread)
	}
	return book, nil
}

// ParseRegistry reads the answer of the reduce and the what-was-missed calls; an answer without the
// list is rejected, an unknown importance is minor.
func ParseRegistry(output string, version *sagamemory.Version) ([]Thread, error) {
	body, err := jsonObject(output)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Open *[]answerThread `json:"open"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", sagamemory.ErrModelOutput, err)
	}
	if raw.Open == nil {
		return nil, errors.Join(sagamemory.ErrModelOutput, errors.New("no open threads"))
	}
	threads := make([]Thread, 0, len(*raw.Open))
	for _, answer := range *raw.Open {
		if answer.Thread.clean() == "" {
			continue
		}
		thread := answer.thread(version)
		thread.Weight = WeightMinor
		if strings.EqualFold(answer.Weight.clean(), WeightMajor) {
			thread.Weight = WeightMajor
		}
		threads = append(threads, thread)
	}
	return threads, nil
}

// ParseVerdict reads the answer of the check of one record.
func ParseVerdict(output string) (Verdict, error) {
	body, err := jsonObject(output)
	if err != nil {
		return Verdict{}, err
	}
	var raw struct {
		V      loose `json:"v"`
		Thread loose `json:"thread"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Verdict{}, fmt.Errorf("%w: %v", sagamemory.ErrModelOutput, err)
	}
	return Verdict{Decision: raw.V.clean(), Text: raw.Thread.clean()}, nil
}
