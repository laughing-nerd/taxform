package annotation

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// FormatValue converts one data value into the text printed in a box.
// copyName is the copy being printed, because a mask may apply to some
// copies only. A missing value (nil) prints as "".
func FormatValue(value any, format Format, copyName string) (string, error) {
	if value == nil {
		return "", nil
	}
	switch format.Type {
	case "", FormatText:
		text, err := toText(value)
		if err != nil {
			return "", err
		}
		if format.Upper {
			text = strings.ToUpper(text)
		}
		return text, nil
	case FormatCurrency, FormatNumber:
		return formatAmount(value, format)
	case FormatSSN:
		return formatID(value, format, copyName, "###-##-####")
	case FormatEIN:
		return formatID(value, format, copyName, "##-#######")
	case FormatDate:
		return formatDate(value, format)
	}
	return "", fmt.Errorf("unknown format type %q", format.Type)
}

// toText converts a single JSON value (string, number or boolean) to text.
func toText(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case bool:
		return strconv.FormatBool(v), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	}
	return "", fmt.Errorf("expected a single value, got %T", value)
}

// toExactNumber reads a JSON number or a decimal string such as "98250.50"
// as an exact fraction, so no floating-point error can creep into money.
func toExactNumber(value any) (*big.Rat, error) {
	text, err := toText(value)
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	number, ok := new(big.Rat).SetString(text)
	if !ok || strings.Contains(text, "/") {
		return nil, fmt.Errorf("%q is not a decimal number", text)
	}
	return number, nil
}

// formatAmount prints a currency or number: rounded, with commas, and with
// a negative sign shown as "-" or as parentheses.
func formatAmount(value any, format Format) (string, error) {
	number, err := toExactNumber(value)
	if err != nil {
		return "", err
	}
	decimals := 0
	if format.Type == FormatCurrency {
		decimals = 2
	}
	if format.Decimals != nil {
		decimals = *format.Decimals
	}

	// Round half away from zero, as taught in school: move the decimal point
	// right, add one half, and drop whatever is left after the point.
	// 98250.505 with 2 decimals: 9825050.5 + 0.5 = 9825051 → 98,250.51
	negative := number.Sign() < 0
	shifted := new(big.Rat).Abs(number)
	shifted.Mul(shifted, new(big.Rat).SetInt(powerOfTen(decimals)))
	shifted.Add(shifted, big.NewRat(1, 2))
	rounded := new(big.Int).Quo(shifted.Num(), shifted.Denom())

	if rounded.Sign() == 0 {
		negative = false // never print "-0.00"
		if format.Zero == "blank" {
			return "", nil
		}
	}

	// Split the digits into the whole part and the decimals: "9825051" with
	// 2 decimals gives "98250" and "51". Pad first so 0.05 becomes "005".
	digits := rounded.String()
	if len(digits) <= decimals {
		digits = strings.Repeat("0", decimals-len(digits)+1) + digits
	}
	whole := digits[:len(digits)-decimals]
	fraction := digits[len(digits)-decimals:]

	if format.Part == "fraction" { // separate cents box
		return fraction, nil
	}
	text := addCommas(whole)
	if decimals > 0 && format.Part != "integer" {
		text += "." + fraction
	}
	if negative {
		if format.Negative == "parens" {
			return "(" + text + ")", nil
		}
		return "-" + text, nil
	}
	return text, nil
}

func powerOfTen(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// addCommas inserts a comma every three digits from the right:
// "98250" → "98,250".
func addCommas(digits string) string {
	var out strings.Builder
	for i, digit := range digits {
		remaining := len(digits) - i
		if i > 0 && remaining%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(digit)
	}
	return out.String()
}

// formatID prints a 9-digit identifier using a pattern in which each "#" is
// one digit: "###-##-####" prints 123-45-6789. Dashes and spaces in the data
// are ignored. If the format's mask applies to this copy, the leading digits
// are hidden: XXX-XX-6789.
func formatID(value any, format Format, copyName, pattern string) (string, error) {
	text, err := toText(value)
	if err != nil {
		return "", err
	}
	digits := strings.NewReplacer("-", "", " ", "").Replace(text)
	if len(digits) != 9 || strings.Trim(digits, "0123456789") != "" {
		return "", fmt.Errorf("%q is not a 9-digit identifier", text)
	}
	chars := []rune(digits)

	if mask := format.Mask; mask != nil && appliesTo(mask.Copies, copyName) {
		hideWith := 'X'
		if mask.Char != "" {
			hideWith = []rune(mask.Char)[0]
		}
		for i := 0; i < len(chars)-mask.Visible; i++ {
			chars[i] = hideWith
		}
	}

	var out strings.Builder
	next := 0
	for _, symbol := range pattern {
		if symbol == '#' {
			out.WriteRune(chars[next])
			next++
		} else {
			out.WriteRune(symbol)
		}
	}
	return out.String(), nil
}

// formatDate reads an ISO 8601 date (2026-04-15) and prints it using a
// pattern of YYYY, YY, MM and DD. The default pattern is MM/DD/YYYY.
func formatDate(value any, format Format) (string, error) {
	text, err := toText(value)
	if err != nil {
		return "", err
	}
	date, err := time.Parse("2006-01-02", text)
	if err != nil {
		return "", fmt.Errorf("%q is not an ISO 8601 date (YYYY-MM-DD)", text)
	}
	pattern := format.Pattern
	if pattern == "" {
		pattern = "MM/DD/YYYY"
	}
	return strings.NewReplacer(
		"YYYY", fmt.Sprintf("%04d", date.Year()),
		"YY", fmt.Sprintf("%02d", date.Year()%100),
		"MM", fmt.Sprintf("%02d", int(date.Month())),
		"DD", fmt.Sprintf("%02d", date.Day()),
	).Replace(pattern), nil
}
