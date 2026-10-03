// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// now is the clock a date with no year takes its year from; tests replace
// it.
var now = time.Now

// eras are the Japanese eras a date may name, each with its names, the
// Gregorian year its first year falls in, and the day it began; an era
// ends the day before the next begins. The names are the full name, the
// first kanji, the initial, and the ligature an IME offers.
var eras = []era{
	{names: []string{"明治", "明", "M", "㍾"}, epoch: 1868, from: time.Date(1868, 1, 25, 0, 0, 0, 0, time.UTC)},
	{names: []string{"大正", "大", "T", "㍽"}, epoch: 1912, from: time.Date(1912, 7, 30, 0, 0, 0, 0, time.UTC)},
	{names: []string{"昭和", "昭", "S", "㍼"}, epoch: 1926, from: time.Date(1926, 12, 25, 0, 0, 0, 0, time.UTC)},
	{names: []string{"平成", "平", "H", "㍻"}, epoch: 1989, from: time.Date(1989, 1, 8, 0, 0, 0, 0, time.UTC)},
	{names: []string{"令和", "令", "R", "㋿"}, epoch: 2019, from: time.Date(2019, 5, 1, 0, 0, 0, 0, time.UTC)},
}

type era struct {
	names []string
	epoch int
	from  time.Time
}

// eraNamed returns the era a name denotes and the day the next era began,
// which the named era's dates must stay before.
func eraNamed(name string) (era, time.Time, bool) {
	name = strings.ToUpper(name)
	for i, e := range eras {
		if slices.Contains(e.names, name) {
			until := time.Time{}
			if i+1 < len(eras) {
				until = eras[i+1].from
			}
			return e, until, true
		}
	}
	return era{}, time.Time{}, false
}

const (
	// dateNumeral is a year, month, or day written in kanji, in place or
	// with the small units: 二〇二六, 二千二十六, 十, 三十一.
	dateNumeral = `[〇零一壱弌二弐貳貮三参參四肆五伍六陸七漆柒八捌九玖十拾百佰陌千阡仟]+`
	dateNumber  = `(?:\d{1,4}|` + dateNumeral + `)`
	dateYear    = `(?:\d{4}|` + dateNumeral + `)`
	eraNames    = `((?i:明治|大正|昭和|平成|令和|[MTSHR明大昭平令㍾㍽㍼㍻㋿]))`
)

var (
	// timeSuffix is a time of day after a date, with a colon or 時, and
	// an optional 午前 or 午後. A bare number is never a time, so the day
	// of R8.10.3 stays a day.
	timeSuffix = regexp.MustCompile(`(\s+|日|\))\s*(午前|午後)?\s*(\d{1,2})(?::(\d{2})(?::(\d{2}))?|時(?:\s*(\d{1,2})\s*分(?:\s*(\d{1,2})\s*秒)?)?)\s*$`)
	// weekdayMark is the weekday a form writes after a date, in
	// parentheses or as 金曜日; it is dropped, not checked.
	weekdayMark = regexp.MustCompile(`\s*(?:\(\s*(?:[月火水木金土日](?:曜日|曜)?|(?i:mon|tue|wed|thu|fri|sat|sun)[a-z]*)\s*\)|[月火水木金土日]曜日?)$`)
	kanjiDate   = regexp.MustCompile(`^(` + dateYear + `)\s*年\s*(` + dateNumber + `)\s*月(?:\s*(` + dateNumber + `)\s*日?)?$`)
	numericDate = regexp.MustCompile(`^(\d{4})([/.\-])(\d{1,2})(?:([/.\-])(\d{1,2}))?$`)
	eraDate     = regexp.MustCompile(`^` + eraNames + `\s*(元|` + dateNumber + `)\s*(?:年\s*(` + dateNumber + `)\s*月(?:\s*(` + dateNumber + `)\s*日?)?|([./\-])\s*(\d{1,2})(?:([./\-])\s*(\d{1,2}))?)$`)
	monthDay    = regexp.MustCompile(`^(` + dateNumber + `)\s*月\s*(` + dateNumber + `)\s*日?$`)
	slashMD     = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})$`)
)

// parseDateText reads a date, with or without a time of day, the way a
// Japanese form writes it, after the ISO and slash forms were tried. See
// spec 077, Japanese text under a pinned type, for the forms. A date with
// no day is the first of its month; one with no year takes the year of
// the clock.
func parseDateText(s string) (time.Time, bool) {
	date := s
	var clock dayTime
	if m := timeSuffix.FindStringSubmatchIndex(s); m != nil {
		var ok bool
		if clock, ok = parseDayTime(s, m); !ok {
			return time.Time{}, false
		}
		date = strings.TrimSpace(s[:m[3]])
	}
	date = strings.TrimSpace(weekdayMark.ReplaceAllString(date, ""))

	var year, month, day int
	var from, until time.Time
	switch {
	case kanjiDate.MatchString(date):
		m := kanjiDate.FindStringSubmatch(date)
		p, ok := readDateParts(m[1], m[2], m[3])
		if !ok {
			return time.Time{}, false
		}
		year, month, day = p.year, p.month, p.day
	case numericDate.MatchString(date):
		m := numericDate.FindStringSubmatch(date)
		if m[4] != "" && m[4] != m[2] {
			return time.Time{}, false
		}
		p, ok := readDateParts(m[1], m[3], m[5])
		if !ok {
			return time.Time{}, false
		}
		year, month, day = p.year, p.month, p.day
	case eraDate.MatchString(date):
		m := eraDate.FindStringSubmatch(date)
		named, next, ok := eraNamed(m[1])
		if !ok {
			return time.Time{}, false
		}
		eraYear := 1
		if m[2] != "元" {
			if eraYear, ok = dateNumeralValue(m[2]); !ok || eraYear < 1 {
				return time.Time{}, false
			}
		}
		monthText, dayText := m[3], m[4]
		if m[5] != "" {
			if m[7] != "" && m[7] != m[5] {
				return time.Time{}, false
			}
			monthText, dayText = m[6], m[8]
		}
		if month, ok = dateNumeralValue(monthText); !ok {
			return time.Time{}, false
		}
		day = 1
		if dayText != "" {
			if day, ok = dateNumeralValue(dayText); !ok {
				return time.Time{}, false
			}
		}
		year = named.epoch + eraYear - 1
		from, until = named.from, next
	case monthDay.MatchString(date):
		m := monthDay.FindStringSubmatch(date)
		p, ok := readDateParts("", m[1], m[2])
		if !ok {
			return time.Time{}, false
		}
		year, month, day = now().Year(), p.month, p.day
	case slashMD.MatchString(date):
		m := slashMD.FindStringSubmatch(date)
		p, ok := readDateParts("", m[1], m[2])
		if !ok {
			return time.Time{}, false
		}
		year, month, day = now().Year(), p.month, p.day
	default:
		return time.Time{}, false
	}
	if year < 1000 || year > 9999 || month < 1 || month > 12 || day < 1 || day > 31 {
		return time.Time{}, false
	}
	t := time.Date(year, time.Month(month), day, clock.hour, clock.minute, clock.second, 0, time.UTC)
	if t.Day() != day || (!from.IsZero() && t.Before(from)) || (!until.IsZero() && !t.Before(until)) {
		return time.Time{}, false
	}
	return t, true
}

// datePart holds the year, month, and day a date form names; a missing
// year is zero and a missing day is the first.
type dateParts struct {
	year, month, day int
}

// readDateParts reads a year, month, and day written in Arabic digits or
// in kanji.
func readDateParts(yearText, monthText, dayText string) (dateParts, bool) {
	var p dateParts
	var ok bool
	if yearText != "" {
		if p.year, ok = dateNumeralValue(yearText); !ok {
			return dateParts{}, false
		}
	}
	if p.month, ok = dateNumeralValue(monthText); !ok {
		return dateParts{}, false
	}
	p.day = 1
	if dayText != "" {
		if p.day, ok = dateNumeralValue(dayText); !ok {
			return dateParts{}, false
		}
	}
	return p, true
}

// dateNumeralValue reads a part of a date written in Arabic digits or in
// kanji, in place or with units.
func dateNumeralValue(text string) (int, bool) {
	if n, err := strconv.Atoi(text); err == nil {
		return n, true
	}
	f, ok := evalNumberCore(text)
	if !ok || f != float64(int(f)) || f < 0 || f > 9999 {
		return 0, false
	}
	return int(f), true
}

// dayTime is a time of day read after a date.
type dayTime struct {
	hour, minute, second int
}

// parseDayTime reads the time timeSuffix matched: hours 0 to 23, or 0 to
// 12 with 午前 (12 is midnight) or 午後 (0 and 12 are noon).
func parseDayTime(s string, m []int) (dayTime, bool) {
	group := func(i int) string {
		if m[2*i] < 0 {
			return ""
		}
		return s[m[2*i]:m[2*i+1]]
	}
	number := func(text string) int {
		n, _ := strconv.Atoi(text)
		return n
	}
	period := group(2)
	t := dayTime{hour: number(group(3))}
	if group(4) != "" {
		t.minute, t.second = number(group(4)), number(group(5))
	} else {
		t.minute, t.second = number(group(6)), number(group(7))
	}
	if t.minute > 59 || t.second > 59 {
		return dayTime{}, false
	}
	switch period {
	case "":
		if t.hour > 23 {
			return dayTime{}, false
		}
	case "午前":
		if t.hour > 12 {
			return dayTime{}, false
		}
		if t.hour == 12 {
			t.hour = 0
		}
	case "午後":
		if t.hour > 12 {
			return dayTime{}, false
		}
		if t.hour < 12 {
			t.hour += 12
		}
	}
	return t, true
}
