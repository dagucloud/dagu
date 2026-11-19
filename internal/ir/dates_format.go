// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// namedDateFormats maps the named formats to their Go layouts. The unix
// format has no layout.
var namedDateFormats = map[string]string{
	DateFormatDate:        time.DateOnly,
	DateFormatDateCompact: "20060102",
	DateFormatDatetime:    time.DateTime,
	DateFormatRFC3339:     time.RFC3339,
	DateFormatUnix:        "",
}

// strftimeLayouts maps the strftime directives that have a Go layout.
var strftimeLayouts = map[byte]string{
	'Y': "2006",
	'y': "06",
	'm': "01",
	'd': "02",
	'e': "_2",
	'H': "15",
	'I': "03",
	'M': "04",
	'S': "05",
	'p': "PM",
	'a': "Mon",
	'A': "Monday",
	'b': "Jan",
	'h': "Jan",
	'B': "January",
	'j': "002",
	'Z': "MST",
	'z': "-0700",
	'F': time.DateOnly,
	'T': time.TimeOnly,
	'R': "15:04",
}

// strftimeDirectives lists the directives a strftime pattern accepts, for
// error messages.
const strftimeDirectives = "%Y %y %m %d %e %H %I %M %S %p %a %A %b %h %B %j %Z %z %F %T %R %s %u %w %V %G %%"

// validateDateFormat checks a format: empty, a named format, or a strftime
// pattern with known directives.
func validateDateFormat(format string) error {
	if format == "" {
		return nil
	}
	if _, ok := namedDateFormats[format]; ok {
		return nil
	}
	if !strings.Contains(format, "%") {
		return fmt.Errorf("unknown format %q: use date, date_compact, datetime, rfc3339, unix, or a strftime pattern such as %%Y/%%m/%%d", format)
	}
	_, err := formatStrftime(format, time.Time{})
	return err
}

// formatDate formats t, in its own zone, with a format that passed
// validateDateFormat.
func formatDate(format string, t time.Time) (string, error) {
	if format == "" {
		format = DateFormatDate
	}
	if layout, ok := namedDateFormats[format]; ok {
		if format == DateFormatUnix {
			return strconv.FormatInt(t.Unix(), 10), nil
		}
		return t.Format(layout), nil
	}
	return formatStrftime(format, t)
}

// formatStrftime formats t with a strftime pattern, failing on a directive
// it does not know.
func formatStrftime(pattern string, t time.Time) (string, error) {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		if i+1 == len(pattern) {
			return "", fmt.Errorf("format %q ends with %%: write %%%% for a literal %%", pattern)
		}
		i++
		directive := pattern[i]
		if layout, ok := strftimeLayouts[directive]; ok {
			b.WriteString(t.Format(layout))
			continue
		}
		switch directive {
		case '%':
			b.WriteByte('%')
		case 's':
			b.WriteString(strconv.FormatInt(t.Unix(), 10))
		case 'u':
			b.WriteString(strconv.Itoa((int(t.Weekday())+6)%7 + 1))
		case 'w':
			b.WriteString(strconv.Itoa(int(t.Weekday())))
		case 'V':
			_, week := t.ISOWeek()
			fmt.Fprintf(&b, "%02d", week)
		case 'G':
			year, _ := t.ISOWeek()
			fmt.Fprintf(&b, "%04d", year)
		default:
			return "", fmt.Errorf("unknown directive %%%c in %q: use %s", directive, pattern, strftimeDirectives)
		}
	}
	return b.String(), nil
}
