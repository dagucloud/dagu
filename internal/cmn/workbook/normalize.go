// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/width"
)

// foldWidth reads full-width digits, letters, and punctuation as their
// ASCII forms, the way a form filled on a Japanese keyboard writes them,
// and the minus sign U+2212 as a hyphen, and drops surrounding white
// space. It serves the pinned types only; a cell read as text keeps what
// it holds.
func foldWidth(s string) string {
	s = width.Fold.String(s)
	s = strings.ReplaceAll(s, "−", "-")
	return strings.TrimSpace(s)
}

// numberText is the text a pinned number is parsed from: folded, without
// thousands separators, and without one yen sign before it or one 円
// after it, so ￥123,000 and 123,000円 read as 123000.
func numberText(s string) string {
	s = foldWidth(s)
	s = strings.TrimSpace(strings.TrimPrefix(s, "¥"))
	s = strings.TrimSpace(strings.TrimSuffix(s, "円"))
	return strings.ReplaceAll(s, ",", "")
}

// japaneseLayouts are the date forms a kanji date takes, tried after the
// ISO and slash forms.
var japaneseLayouts = []string{
	"2006年1月2日 15:04:05", "2006年1月2日 15:04", "2006年1月2日", "2006.1.2",
}

// eras are the Japanese eras a date may name, each with its name, its
// initial, the Gregorian year its first year falls in, and the day it
// began; an era ends the day before the next begins.
var eras = []era{
	{names: []string{"明治", "明", "M"}, epoch: 1868, from: time.Date(1868, 1, 25, 0, 0, 0, 0, time.UTC)},
	{names: []string{"大正", "大", "T"}, epoch: 1912, from: time.Date(1912, 7, 30, 0, 0, 0, 0, time.UTC)},
	{names: []string{"昭和", "昭", "S"}, epoch: 1926, from: time.Date(1926, 12, 25, 0, 0, 0, 0, time.UTC)},
	{names: []string{"平成", "平", "H"}, epoch: 1989, from: time.Date(1989, 1, 8, 0, 0, 0, 0, time.UTC)},
	{names: []string{"令和", "令", "R"}, epoch: 2019, from: time.Date(2019, 5, 1, 0, 0, 0, 0, time.UTC)},
}

type era struct {
	names []string
	epoch int
	from  time.Time
}

// eraNamed returns the era a name or initial denotes and the day the next
// era began, which the named era's dates must stay before.
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

// eraDatePattern is a date in a Japanese era, long or short: 令和8年10月3日,
// 令和元年5月1日, R8.10.3, H31/4/30, with an optional time of day.
var eraDatePattern = regexp.MustCompile(`^(?i)(明治|大正|昭和|平成|令和|[MTSHR明大昭平令])\s*(元|\d{1,2})\s*[年./]\s*(\d{1,2})\s*[月./]\s*(\d{1,2})\s*日?(?:\s+(\d{1,2}):(\d{2})(?::(\d{2}))?)?$`)

// parseEraDate reads a date written in a Japanese era. The first year of
// an era is 元年 or year 1; year 0 is not a year, a day the month does not
// have is refused, and so is a day outside the era, such as 令和元年4月30日,
// the day before 令和 began.
func parseEraDate(s string) (time.Time, bool) {
	m := eraDatePattern.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	named, until, ok := eraNamed(m[1])
	if !ok {
		return time.Time{}, false
	}
	year := 1
	if m[2] != "元" {
		year, _ = strconv.Atoi(m[2])
	}
	month, _ := strconv.Atoi(m[3])
	day, _ := strconv.Atoi(m[4])
	var hour, minute, second int
	if m[5] != "" {
		hour, _ = strconv.Atoi(m[5])
		minute, _ = strconv.Atoi(m[6])
		second, _ = strconv.Atoi(m[7])
	}
	if year < 1 || month < 1 || month > 12 || day < 1 || hour > 23 || minute > 59 || second > 59 {
		return time.Time{}, false
	}
	t := time.Date(named.epoch+year-1, time.Month(month), day, hour, minute, second, 0, time.UTC)
	if t.Day() != day || t.Before(named.from) || (!until.IsZero() && !t.Before(until)) {
		return time.Time{}, false
	}
	return t, true
}
