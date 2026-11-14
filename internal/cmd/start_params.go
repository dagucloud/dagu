// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/launcher"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// stdinHasParamsInput reports whether stdin parameters were requested and stdin
// is a pipe or redirected file. Character devices are never parameter input.
func stdinHasParamsInput(ctx *Context) (bool, error) {
	if !stdinParamsRequested(ctx) {
		return false, nil
	}
	info, err := os.Stdin.Stat()
	if err != nil {
		return false, fmt.Errorf("failed to inspect params from stdin: %w", err)
	}
	return info.Mode()&os.ModeCharDevice == 0, nil
}

// stdinParamsRequested reports whether stdin was selected as a parameter source.
func stdinParamsRequested(ctx *Context) bool {
	enabled, err := ctx.Command.Flags().GetBool(paramsStdinFlag.name)
	return err == nil && enabled
}

// maxStdinParamsSize bounds the bytes read from stdin as run params, so a
// large redirected file or stream cannot exhaust memory. It is the limit the
// launcher passes params on stdin within.
const maxStdinParamsSize = launcher.MaxParamsSize

// readStdinParams returns stdin trimmed, for use as run params.
func readStdinParams() (string, error) {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxStdinParamsSize+1))
	if err != nil {
		return "", fmt.Errorf("failed to read params from stdin: %w", err)
	}
	if len(data) > maxStdinParamsSize {
		return "", fmt.Errorf("params from stdin exceed the %d byte limit", maxStdinParamsSize)
	}
	return strings.TrimSpace(string(data)), nil
}

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
