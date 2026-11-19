// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
)

type paramToken struct {
	Name  string
	Value string
}

// StartParamInput describes start params regardless of caller (CLI/API).
// Use DashArgs for parameter tokens or RawParams for parameter text. Quotes in
// RawParams delimit values rather than wrapping the complete input.
type StartParamInput struct {
	DashArgs  []string
	RawParams string
}

// paramTokenRegex matches positional and named params similarly to spec.parseStringParams.
var paramTokenRegex = regexp.MustCompile(
	`(?:([^\s="]+)=)?("(?:\\"|[^"])*"|` + "`[^`]*`" + `|[^"\s]+)`,
)

func ValidateStartArgs(hasDash bool, args []string) error {
	if hasDash || len(args) <= 1 {
		return nil
	}
	return fmt.Errorf(
		"unexpected arguments after DAG definition: %s (use '--' before parameters)",
		strings.Join(args[1:], " "),
	)
}

// ParamNames returns the names a params string gives values to, such as day
// in `day=2026-11-01`. A positional value has no name and is left out.
func ParamNames(raw string) ([]string, error) {
	pairs, err := parseOverridePairs(raw, nil)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, pair := range pairs {
		if pair.Name != "" {
			names = append(names, pair.Name)
		}
	}
	return names, nil
}

// ValidateStartParams validates positional params against declared defaults.
// Rule: allow 0..expected positional params; reject only when positional params exceed expected.
func ValidateStartParams(defaultParams string, input StartParamInput) error {
	provided, skipValidation := extractProvidedParamTokens(input)
	if skipValidation || len(provided) == 0 {
		return nil
	}

	expected := countDeclaredParams(defaultParams)
	if expected == 0 {
		// No declared params means there is no positional-count contract to enforce.
		return nil
	}

	got := countPositionalParams(provided)
	if got == 0 {
		// Named-only params should not trigger positional count validation.
		return nil
	}

	if got > expected {
		return fmt.Errorf("too many positional params: expected at most %d, got %d", expected, got)
	}

	return nil
}

func extractProvidedParamTokens(input StartParamInput) ([]paramToken, bool) {
	if len(input.DashArgs) > 0 {
		if shouldSkipDashArgsPositionalValidation(input.DashArgs) {
			return nil, true
		}
		return parseParamsFromArgs(input.DashArgs), false
	}

	raw := strings.TrimSpace(input.RawParams)
	if raw == "" {
		return nil, false
	}

	if isJSONParams(raw) {
		return nil, true
	}

	return parseParamTokens(raw), false
}

func shouldSkipDashArgsPositionalValidation(args []string) bool {
	// JSON payloads are passed as a single arg after "--" and should bypass
	// positional-count validation (same behavior as --params JSON input).
	if len(args) != 1 {
		return false
	}
	input := stringutil.RemoveQuotes(args[0])
	return isJSONParams(input)
}

func isJSONParams(input string) bool {
	input = strings.TrimSpace(input)
	if input == "" {
		return false
	}
	isJSONObject := strings.HasPrefix(input, "{") && strings.HasSuffix(input, "}")
	isJSONArray := strings.HasPrefix(input, "[") && strings.HasSuffix(input, "]")
	if !isJSONObject && !isJSONArray {
		return false
	}
	return json.Valid([]byte(input))
}

func parseParamsFromArgs(args []string) []paramToken {
	var tokens []paramToken
	for _, arg := range args {
		tokens = append(tokens, parseParamTokens(arg)...)
	}
	return tokens
}

func parseParamTokens(input string) []paramToken {
	matches := paramTokenRegex.FindAllStringSubmatch(strings.TrimSpace(input), -1)
	tokens := make([]paramToken, 0, len(matches))
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		name := match[1]
		value := match[2]
		if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
			value = stringutil.RemoveQuotes(value)
		}
		tokens = append(tokens, paramToken{Name: name, Value: value})
	}
	return tokens
}

func countPositionalParams(tokens []paramToken) int {
	count := 0
	for _, token := range tokens {
		if token.Name == "" {
			count++
		}
	}
	return count
}

func countDeclaredParams(defaultParams string) int {
	if strings.TrimSpace(defaultParams) == "" {
		return 0
	}
	return len(parseParamTokens(defaultParams))
}
