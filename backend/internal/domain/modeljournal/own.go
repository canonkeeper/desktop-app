// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"math"
	"strconv"
	"strings"
)

// nbsp keeps a number with its sign and its groups, as the page writes the sums (src/editions/desktop/reading/amounts.ts).
const nbsp = "\u00a0"

// signs are the signs of the currencies of the prices of «Свой ИИ»: the dollar before its number, the rest after it.
var signs = map[string]struct {
	sign   string
	before bool
}{
	"RUB": {"₽", false},
	"USD": {"$", true},
	"CNY": {"¥", false},
	"EUR": {"€", false},
}

// OwnWords are the numbers of «Свой ИИ» of a row (JR3.2, Data): «6 запросов · 48 тыс. токенов · ≈ 3 ₽», an estimate with
// «≈», a sum in every currency, none without the prices — the words of the details of the page, by the one table
// testdata/own-words.json.
func OwnWords(own OwnNumbers) string {
	parts := []string{
		grouped(int64(own.Requests)) + " " + plural(own.Requests, "запрос", "запроса", "запросов"),
		approximately(tokenWords(own.Tokens), own.Estimated),
	}
	for _, cost := range own.Costs {
		parts = append(parts, approximately(moneyWords(cost.Micros, cost.Currency), true))
	}
	return strings.Join(parts, " · ")
}

func approximately(words string, estimate bool) string {
	if estimate {
		return "≈" + nbsp + words
	}
	return words
}

// tokenWords are «820 токенов», «48 тыс. токенов», «1,2 млн токенов»: a thousand thousands is a million.
func tokenWords(tokens int64) string {
	if tokens < 1000 {
		return strconv.FormatInt(tokens, 10) + " " + plural(int(tokens), "токен", "токена", "токенов")
	}
	if thousands := int64(math.Round(float64(tokens) / 1000)); thousands < 1000 {
		return strconv.FormatInt(thousands, 10) + " тыс. токенов"
	}
	value := float64(tokens) / 1_000_000
	var number string
	switch {
	case value >= 100:
		number = grouped(int64(math.Round(value)))
	case value >= 1:
		number = decimal(value, 1)
	default:
		number = decimal(value, 2)
	}
	return number + nbsp + "млн" + nbsp + "токенов"
}

// moneyWords are a sum as the page says it: from 100 a whole number, below it the cents, «,00» dropped, never «0» for a sum
// that is not nothing; the sign of the currency before or after.
func moneyWords(micros int64, currency string) string {
	value := float64(micros) / 1_000_000
	var number string
	switch {
	case value >= 100:
		number = grouped(int64(math.Round(value)))
	case value > 0 && value < 0.01:
		number = "0,01"
	default:
		number = strings.Replace(strconv.FormatFloat(math.Round(value*100)/100, 'f', 2, 64), ".", ",", 1)
		number = strings.TrimSuffix(number, ",00")
	}
	sign, known := signs[currency]
	switch {
	case !known:
		return number + nbsp + currency
	case sign.before:
		return sign.sign + number
	default:
		return number + nbsp + sign.sign
	}
}

// decimal is a number with up to the digits after the comma, the zeros at the end dropped.
func decimal(value float64, digits int) string {
	scale := math.Pow(10, float64(digits))
	text := strconv.FormatFloat(math.Round(value*scale)/scale, 'f', digits, 64)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	return strings.Replace(text, ".", ",", 1)
}

// grouped is a whole number with its thousands apart: «1 000».
func grouped(value int64) string {
	digits := strconv.FormatInt(value, 10)
	if len(digits) < 4 {
		return digits
	}
	var out strings.Builder
	for index, digit := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			out.WriteString(nbsp)
		}
		out.WriteRune(digit)
	}
	return out.String()
}
