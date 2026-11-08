package authormaterials

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// numberFormat is how Excel shows a number: as it is, as a date or a time, or as a percent.
type numberFormat int

const (
	plainNumber numberFormat = iota
	dateNumber
	timeNumber
	dateTimeNumber
	percentNumber
)

// numberFormatOf reads a number format: one of the formats Excel has without a code, or the code of the workbook.
func numberFormatOf(id int, code string) numberFormat {
	switch {
	case id == 9 || id == 10:
		return percentNumber
	case id >= 14 && id <= 17:
		return dateNumber
	case id >= 18 && id <= 21 || id >= 45 && id <= 47:
		return timeNumber
	case id == 22:
		return dateTimeNumber
	case code == "":
		return plainNumber
	}
	// The first section of the code, without quoted text, escaped characters and the brackets of colours and conditions.
	var plain strings.Builder
	quoted, bracket, escaped := false, false, false
	for _, char := range strings.ToLower(code) {
		switch {
		case escaped:
			escaped = false
		case char == '\\':
			escaped = true
		case char == '"':
			quoted = !quoted
		case quoted:
		case char == '[':
			bracket = true
		case char == ']':
			bracket = false
		case bracket:
		case char == ';':
			goto read
		default:
			plain.WriteRune(char)
		}
	}
read:
	format := plain.String()
	hasDate := strings.ContainsAny(format, "yd")
	hasTime := strings.ContainsAny(format, "hs")
	switch {
	case strings.Contains(format, "%"):
		return percentNumber
	case hasDate && hasTime:
		return dateTimeNumber
	case hasDate || strings.Contains(format, "m") && !hasTime && !strings.ContainsAny(format, "0#?"):
		return dateNumber
	case hasTime:
		return timeNumber
	default:
		return plainNumber
	}
}

// valueReader reads the value of a cell as Excel shows it.
type valueReader struct {
	shared   []string
	styles   []cellStyle
	date1904 bool
}

func (values valueReader) text(cell xlsxCell) string {
	raw := strings.TrimSpace(cell.Value)
	switch cell.Type {
	case "s":
		if index, err := strconv.Atoi(raw); err == nil && index >= 0 && index < len(values.shared) {
			return strings.TrimSpace(values.shared[index])
		}
		return ""
	case "inlineStr":
		return strings.TrimSpace(cell.Inline.String())
	case "str":
		return strings.TrimSpace(cell.Value)
	case "b":
		switch raw {
		case "1", "true":
			return "да"
		case "0", "false":
			return "нет"
		}
		return raw
	case "e":
		// An error of a formula («#ДЕЛ/0!») says nothing of the record.
		return ""
	case "d":
		if at, err := time.Parse("2006-01-02T15:04:05", strings.TrimSuffix(raw, "Z")); err == nil {
			return at.Format("02.01.2006")
		}
		return raw
	}
	number, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return raw
	}
	format := plainNumber
	if cell.Style >= 0 && cell.Style < len(values.styles) {
		format = values.styles[cell.Style].number
	}
	switch format {
	case percentNumber:
		return shortNumber(number*100, raw) + "%"
	case dateNumber, timeNumber, dateTimeNumber:
		if at, ok := values.moment(number); ok {
			switch format {
			case dateNumber:
				return at.Format("02.01.2006")
			case timeNumber:
				return at.Format("15:04")
			default:
				return at.Format("02.01.2006 15:04")
			}
		}
	}
	return shortNumber(number, raw)
}

// moment is the date and time of a serial number of a workbook.
func (values valueReader) moment(serial float64) (time.Time, bool) {
	if serial < 0 || serial > 2958465 {
		return time.Time{}, false
	}
	base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	switch {
	case values.date1904:
		base = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	case serial < 61:
		// Excel counts a 29 February 1900 that never was; the days before it are one day off.
		base = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC)
	}
	days := math.Floor(serial)
	seconds := math.Round((serial - days) * 86400)
	return base.AddDate(0, 0, int(days)).Add(time.Duration(seconds) * time.Second), true
}

// shortNumber writes a number in the fifteen digits Excel shows, so 0.1+0.2 reads 0.3; a number too long for them stays as written.
func shortNumber(number float64, raw string) string {
	text := strconv.FormatFloat(number, 'g', 15, 64)
	if strings.ContainsAny(text, "eE") {
		return raw
	}
	return text
}
