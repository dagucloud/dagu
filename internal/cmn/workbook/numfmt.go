// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"strings"

	"github.com/xuri/nfp"
)

// cellKind is what a cell's number format says a numeric value means.
type cellKind int

const (
	kindNumber cellKind = iota
	kindDate
	kindDateTime
	kindTime
)

// builtinDateFormats maps the built-in number format IDs that render dates
// or times. 14-17 and 27-36 are dates, 18-21, 45, and 47 are times, 22 is
// a date with a time, and 50-58 are the East Asian locale dates.
var builtinDateFormats = map[int]cellKind{
	14: kindDate, 15: kindDate, 16: kindDate, 17: kindDate,
	18: kindTime, 19: kindTime, 20: kindTime, 21: kindTime,
	22: kindDateTime,
	27: kindDate, 28: kindDate, 29: kindDate, 30: kindDate, 31: kindDate,
	32: kindTime, 33: kindTime, 34: kindTime, 35: kindTime, 36: kindDate,
	// 46 is [h]:mm:ss, an elapsed time that may exceed a day, so it stays
	// a number of days like the custom elapsed formats.
	45: kindTime, 46: kindNumber, 47: kindTime,
	50: kindDate, 51: kindDate, 52: kindDate, 53: kindDate, 54: kindDate,
	55: kindDate, 56: kindDate, 57: kindDate, 58: kindDate,
}

// styleKind reports what a style ID's number format renders, cached per
// workbook because every cell of a column usually shares one style.
func (w *file) styleKind(styleID int) cellKind {
	if kind, ok := w.kinds[styleID]; ok {
		return kind
	}
	kind := kindNumber
	if style, err := w.f.GetStyle(styleID); err == nil && style != nil {
		switch {
		case style.CustomNumFmt != nil && *style.CustomNumFmt != "":
			kind = customFormatKind(*style.CustomNumFmt)
		default:
			if k, ok := builtinDateFormats[style.NumFmt]; ok {
				kind = k
			}
		}
	}
	w.kinds[styleID] = kind
	return kind
}

// customFormatKind classifies a custom number format by the date and time
// tokens of its first section. Quoted literals such as "年", escaped
// characters, colors such as [Red], and locale tags such as [$-411] are
// not date tokens, so yyyy"年"m"月"d"日" is a date and [Red]0.00 is not.
func customFormatKind(code string) cellKind {
	parser := nfp.NumberFormatParser()
	sections := parser.Parse(code)
	if len(sections) == 0 {
		return kindNumber
	}
	var tokens []string
	for _, item := range sections[0].Items {
		switch item.TType {
		case nfp.TokenTypeElapsedDateTimes:
			// [h]:mm:ss counts elapsed time, which is a number of days.
			return kindNumber
		case nfp.TokenTypeDateTimes:
			tokens = append(tokens, strings.ToLower(item.TValue))
		}
	}
	if len(tokens) == 0 {
		return kindNumber
	}
	joined := strings.Join(tokens, " ")
	hasTime := strings.ContainsAny(joined, "hs") || strings.Contains(joined, "am/pm") || strings.Contains(joined, "a/p")
	hasDate := strings.ContainsAny(joined, "ydeg") || (strings.Contains(joined, "m") && !hasTime)
	switch {
	case hasDate && hasTime:
		return kindDateTime
	case hasTime:
		return kindTime
	default:
		return kindDate
	}
}
