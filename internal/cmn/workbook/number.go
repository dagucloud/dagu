// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"regexp"
	"strconv"
	"strings"
)

// numberText is the narrow text a number is read from where no type is
// pinned, by where and by header matching: folded, without thousands
// separators, and without one yen sign before it or one 円 after it, so
// ￥123,000 and 123,000円 read as 123000.
func numberText(s string) string {
	s = foldWidth(s)
	s = strings.TrimSpace(strings.TrimPrefix(s, "¥"))
	s = strings.TrimSpace(strings.TrimSuffix(s, "円"))
	return strings.ReplaceAll(s, ",", "")
}

// kanjiDigits maps the kanji digits, the daiji used on receipts and
// contracts included, to their values.
var kanjiDigits = map[rune]int{
	'〇': 0, '零': 0,
	'一': 1, '壱': 1, '弌': 1,
	'二': 2, '弐': 2, '貳': 2, '貮': 2,
	'三': 3, '参': 3, '參': 3,
	'四': 4, '肆': 4,
	'五': 5, '伍': 5,
	'六': 6, '陸': 6,
	'七': 7, '漆': 7, '柒': 7,
	'八': 8, '捌': 8,
	'九': 9, '玖': 9,
}

// kanjiUnits maps the kanji units to the power of ten each stands for.
// The small units 十, 百, and 千 scale the digit before them within a
// section; the large units 万, 億, and 兆 close a section.
var kanjiUnits = map[rune]int{
	'十': 1, '拾': 1,
	'百': 2, '佰': 2, '陌': 2,
	'千': 3, '阡': 3, '仟': 3,
	'万': 4, '萬': 4,
	'億': 8,
	'兆': 12,
}

const (
	kanjiDigitClass = `[〇零一壱弌二弐貳貮三参參四肆五伍六陸七漆柒八捌九玖]`
	smallUnitClass  = `[十拾百佰陌千阡仟]`
	largeUnitClass  = `[万萬億兆]`
	// arabicNumber is digits with optional thousands separators in groups
	// of three, , or 、, and an optional fraction.
	arabicNumber = `(?:\d{1,3}(?:[,、]\d{3})+|\d+)(?:\.\d+)?|\.\d+`
	// kanjiSection is what sits between large units: digits scaled by
	// small units in descending order, with an optional ones place.
	kanjiSection = `(?:(?:` + kanjiDigitClass + `|` + arabicNumber + `)?` + smallUnitClass + `)+(?:` + kanjiDigitClass + `|` + arabicNumber + `)?|` + kanjiDigitClass
	// numberCore is the number proper: an Arabic number, kanji digits in
	// place, or sections closed by descending large units.
	numberCore = `(?:` + arabicNumber + `|` + kanjiDigitClass + `+|(?:(?:` + arabicNumber + `|` + kanjiSection + `)?` + largeUnitClass + `)+(?:` + arabicNumber + `|` + kanjiSection + `)?|` + kanjiSection + `)`
	// priceTags are the words a form writes beside an amount; any other
	// word changes the amount or the currency and is not read.
	priceTags = `消費税込|消費税抜|消費税別|消費税|税込|税抜|税別|内税|外税|本体価格|合計金額|合計|小計|総計|総額|金額|単価|概算`
)

var (
	leadingTag  = regexp.MustCompile(`^(?:\((?:` + priceTags + `)\)|(?:` + priceTags + `))\s*:?\s*`)
	trailingTag = regexp.MustCompile(`\s*\((?:` + priceTags + `)\)$`)
	// numberPattern is an amount as a form writes it: a sign before or
	// after a yen mark, the number, a yen mark or percent after it, and
	// the dash some documents close an amount with.
	numberPattern = regexp.MustCompile(`^([-+▲△ー])?\s*(¥|(?i:JPY)|金)?\s*([-+▲△])?\s*(` + numberCore + `)\s*(円也|円|也|(?i:JPY)|%)?\s*([-ー―—‐])?$`)
	arabicToken   = regexp.MustCompile(`^(?:` + arabicNumber + `)$`)
)

// parseNumberText reads the number a text cell holds under a pinned
// number or integer: an ASCII number as strconv reads it, else an amount
// the way a Japanese form writes it, folded first. See spec 077, Japanese
// text under a pinned type, for the forms.
func parseNumberText(text string) (float64, bool) {
	s := foldWidth(text)
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, true
	}
	s = strings.TrimSpace(trailingTag.ReplaceAllString(s, ""))
	s = strings.TrimSpace(leadingTag.ReplaceAllString(s, ""))
	negative := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		negative = true
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	m := numberPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	before, currency, after, core, suffix := m[1], m[2], m[3], m[4], m[5]
	switch {
	case before != "" && after != "", negative && (before != "" || after != ""):
		return 0, false
	case before == "ー" && !kanaMinusAllowed(s[len(before):]):
		return 0, false
	case suffix == "%" && currency != "":
		return 0, false
	case suffix == "也" && currency != "金":
		return 0, false
	}
	value, ok := evalNumberCore(core)
	if !ok {
		return 0, false
	}
	switch {
	case negative, before == "-", before == "▲", before == "△", before == "ー", after == "-", after == "▲", after == "△":
		value = -value
	}
	return value, true
}

// kanaMinusAllowed says whether a katakana prolonged mark may stand for a
// minus: only before a digit or a yen mark, so ー万 is not read as a
// negative when 一万 was meant.
func kanaMinusAllowed(rest string) bool {
	rest = strings.TrimSpace(rest)
	return rest != "" && (rest[0] >= '0' && rest[0] <= '9' || strings.HasPrefix(rest, "¥"))
}

// numberToken is one piece of a number core: an Arabic number, a kanji
// digit, or a unit.
type numberToken struct {
	kind     tokenKind
	mantissa string // digits without the decimal point, for numbers
	fraction int    // digits after the point, for numbers
	exponent int    // the power of ten, for units
}

type tokenKind int

const (
	tokenNumber tokenKind = iota
	tokenSmallUnit
	tokenLargeUnit
)

// tokenizeNumber splits a number core into tokens; the regex has already
// fixed its shape, so an unknown rune means a bug, not bad input.
func tokenizeNumber(core string) ([]numberToken, bool) {
	var tokens []numberToken
	runes := []rune(core)
	for i := 0; i < len(runes); {
		r := runes[i]
		switch {
		case r >= '0' && r <= '9' || r == '.':
			j := i
			for j < len(runes) && (runes[j] >= '0' && runes[j] <= '9' || runes[j] == '.' || runes[j] == ',' || runes[j] == '、') {
				j++
			}
			text := string(runes[i:j])
			if !arabicToken.MatchString(text) {
				return nil, false
			}
			text = strings.NewReplacer(",", "", "、", "").Replace(text)
			whole, frac, _ := strings.Cut(text, ".")
			tokens = append(tokens, numberToken{kind: tokenNumber, mantissa: whole + frac, fraction: len(frac)})
			i = j
		default:
			if d, ok := kanjiDigits[r]; ok {
				tokens = append(tokens, numberToken{kind: tokenNumber, mantissa: strconv.Itoa(d)})
			} else if u, ok := kanjiUnits[r]; ok && u <= 3 {
				tokens = append(tokens, numberToken{kind: tokenSmallUnit, exponent: u})
			} else if ok {
				tokens = append(tokens, numberToken{kind: tokenLargeUnit, exponent: u})
			} else {
				return nil, false
			}
			i++
		}
	}
	return tokens, true
}

// evalNumberCore evaluates a number core. Without a unit, an Arabic
// number is itself and kanji digits stand in place, so 二〇二六 is 2026.
// With units, each small unit scales the number before it, or one, within
// a section, and each large unit closes the section, so 壱拾弐万参千 is
// 123000 and 12万3千4 is 123004. Units must descend. A number times a
// power of ten is built as decimal text, so 1.15万 is exactly 11500.
func evalNumberCore(core string) (float64, bool) {
	tokens, ok := tokenizeNumber(core)
	if !ok || len(tokens) == 0 {
		return 0, false
	}
	hasUnit := false
	for _, t := range tokens {
		if t.kind != tokenNumber {
			hasUnit = true
		}
	}
	if !hasUnit {
		if len(tokens) == 1 {
			return scaled(tokens[0], 0)
		}
		// Kanji digits in place.
		var digits strings.Builder
		for _, t := range tokens {
			if t.fraction != 0 || len(t.mantissa) != 1 {
				return 0, false
			}
			digits.WriteString(t.mantissa)
		}
		return scaled(numberToken{mantissa: digits.String()}, 0)
	}
	const none = 99
	var total, section float64
	var pending *numberToken
	lastSmall, lastLarge := none, none
	for i := range tokens {
		t := tokens[i]
		switch t.kind {
		case tokenNumber:
			if pending != nil {
				return 0, false
			}
			pending = &tokens[i]
		case tokenSmallUnit:
			if t.exponent >= lastSmall {
				return 0, false
			}
			term, ok := scaled(orOne(pending), t.exponent)
			if !ok {
				return 0, false
			}
			section += term
			pending = nil
			lastSmall = t.exponent
		case tokenLargeUnit:
			if t.exponent >= lastLarge {
				return 0, false
			}
			var term float64
			if pending == nil && section == 0 {
				term, _ = scaled(numberToken{mantissa: "1"}, t.exponent)
			} else {
				var ok bool
				if term, ok = scaled(orZero(pending), t.exponent); !ok {
					return 0, false
				}
				sectionTerm, _ := scaled(numberToken{mantissa: strconv.FormatFloat(section, 'f', -1, 64)}, t.exponent)
				term += sectionTerm
			}
			total += term
			section, pending = 0, nil
			lastSmall, lastLarge = none, t.exponent
		}
	}
	rest, ok := scaled(orZero(pending), 0)
	if !ok {
		return 0, false
	}
	return total + section + rest, true
}

func orOne(t *numberToken) numberToken {
	if t == nil {
		return numberToken{mantissa: "1"}
	}
	return *t
}

func orZero(t *numberToken) numberToken {
	if t == nil {
		return numberToken{mantissa: "0"}
	}
	return *t
}

// scaled returns a number token times ten to the exponent, built as
// decimal text so the result is exact.
func scaled(t numberToken, exponent int) (float64, bool) {
	f, err := strconv.ParseFloat(t.mantissa+"e"+strconv.Itoa(exponent-t.fraction), 64)
	return f, err == nil
}
