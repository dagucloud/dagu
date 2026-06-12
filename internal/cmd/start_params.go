// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/json"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core/spec"
)

func quoteStartDashArgs(args []string) []string {
	if isSingleJSONDashArg(args) {
		return args
	}
	return spec.QuoteRuntimeParams(args, nil)
}

func isSingleJSONDashArg(args []string) bool {
	if len(args) != 1 {
		return false
	}

	input := strings.TrimSpace(stringutil.RemoveQuotes(args[0]))
	if input == "" {
		return false
	}

	isObject := strings.HasPrefix(input, "{") && strings.HasSuffix(input, "}")
	isArray := strings.HasPrefix(input, "[") && strings.HasSuffix(input, "]")
	if !isObject && !isArray {
		return false
	}
	return json.Valid([]byte(input))
}
