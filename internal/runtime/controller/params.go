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
		// Every value is quoted. The child DAG parses this with
		// spec.parseStringParams, which reads bare punctuation as parameter
		// syntax: an unquoted ["a","b"] arrives as five positional fragments.
		parts = append(parts, key+"="+strconv.Quote(formatArgValue(args[key])))
	}
	return strings.Join(parts, " ")
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
