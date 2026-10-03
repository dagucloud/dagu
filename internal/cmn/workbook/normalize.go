// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"regexp"
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

// eraEpochs maps each era name and initial to the Gregorian year its
// first year falls in.
var eraEpochs = map[string]int{
	"明治": 1868, "明": 1868, "M": 1868,
	"大正": 1912, "大": 1912, "T": 1912,
	"昭和": 1926, "昭": 1926, "S": 1926,
	"平成": 1989, "平": 1989, "H": 1989,
	"令和": 2019, "令": 2019, "R": 2019,
}

// eraDatePattern is a date in a Japanese era, long or short: 令和8年10月3日,
// 令和元年5月1日, R8.10.3, H31/4/30, with an optional time of day.
var eraDatePattern = regexp.MustCompile(`^(?i)(明治|大正|昭和|平成|令和|[MTSHR明大昭平令])\s*(元|\d{1,2})\s*[年./]\s*(\d{1,2})\s*[月./]\s*(\d{1,2})\s*日?(?:\s+(\d{1,2}):(\d{2})(?::(\d{2}))?)?$`)

// parseEraDate reads a date written in a Japanese era. The first year of
// an era is 元年 or year 1; year 0 is not a year, and a day the month does
// not have is refused.
func parseEraDate(s string) (time.Time, bool) {
	m := eraDatePattern.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	epoch, ok := eraEpochs[strings.ToUpper(m[1])]
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
	t := time.Date(epoch+year-1, time.Month(month), day, hour, minute, second, 0, time.UTC)
	if t.Day() != day {
		return time.Time{}, false
	}
	return t, true
}
