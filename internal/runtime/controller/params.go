// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ParamString renders tool-call arguments as the "key=value" parameter string a
// child DAG run expects. Keys are sorted so the same arguments always produce
// the same child run ID.
func ParamString(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}

	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		raw := args[key]
		value := formatArgValue(raw)
		if needsQuoting(raw, value) {
			value = strconv.Quote(value)
		}
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, " ")
}

// needsQuoting reports whether a rendered argument must be quoted to survive the
// child DAG's param splitter. Arguments come from the model, so a plain string
// may hold whitespace or quote characters the splitter would act on. Structured
// values are rendered as JSON, which carries its own quoting.
func needsQuoting(raw any, rendered string) bool {
	switch raw.(type) {
	case nil, string:
		return rendered == "" || strings.ContainsAny(rendered, " \t\n\"'")
	default:
		return false
	}
}

func formatArgValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		// JSON numbers decode as float64; render whole numbers without a fraction.
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(encoded)
	}
}
