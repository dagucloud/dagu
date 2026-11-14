// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package clickhouse

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// paramPrefix is the URL parameter prefix of server-side query parameters.
const paramPrefix = "param_"

// formatParam renders a params value in the text form the server parses for a
// {name:Type} placeholder. A string is sent as written, so a Date, DateTime,
// Decimal, or Tuple value is given as its text literal. Lists and maps become
// Array and Map literals.
func formatParam(v any) (string, error) {
	if s, ok := v.(string); ok {
		return escapeText(s, false), nil
	}
	return formatLiteral(v)
}

// formatLiteral renders v as a value inside an Array or Map literal, where
// strings are quoted.
func formatLiteral(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return `\N`, nil
	case string:
		return "'" + escapeText(x, true) + "'", nil
	case bool:
		return strconv.FormatBool(x), nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(x), nil
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			part, err := formatNested(item)
			if err != nil {
				return "", err
			}
			parts[i] = part
		}
		return "[" + strings.Join(parts, ",") + "]", nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			part, err := formatNested(x[k])
			if err != nil {
				return "", err
			}
			parts[i] = "'" + escapeText(k, true) + "':" + part
		}
		return "{" + strings.Join(parts, ",") + "}", nil
	default:
		return "", fmt.Errorf("unsupported value type %T", v)
	}
}

// formatNested renders an element of a list or map; a null is NULL there.
func formatNested(v any) (string, error) {
	if v == nil {
		return "NULL", nil
	}
	return formatLiteral(v)
}

// escapeText escapes the characters that end or alter a value in the server's
// text parsing: the backslash, line and field separators, NUL, and, inside a
// quoted string, the single quote.
func escapeText(s string, quoted bool) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		case 0:
			b.WriteString(`\0`)
		case '\'':
			if quoted {
				b.WriteString(`\'`)
			} else {
				b.WriteRune(r)
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// formatSetting renders a settings value for the URL.
func formatSetting(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		if x {
			return "1", nil
		}
		return "0", nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(x), nil
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("unsupported value type %T (must be a string, number, or boolean)", v)
	}
}
