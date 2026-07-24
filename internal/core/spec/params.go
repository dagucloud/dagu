// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
)

func overrideParams(paramPairs *[]paramPair, override []paramPair) error {
	if err := rejectUnknownNamedParams(*paramPairs, override); err != nil {
		return err
	}

	// Override the default parameters with the command line parameters
	pairsIndex := make(map[string]int)
	for i, paramPair := range *paramPairs {
		if paramPair.Name != "" {
			pairsIndex[paramPair.Name] = i
		}
	}
	for i, paramPair := range override {
		if paramPair.Name == "" {
			// For positional parameters
			if i < len(*paramPairs) {
				(*paramPairs)[i] = paramPair
			} else {
				*paramPairs = append(*paramPairs, paramPair)
			}
			continue
		}

		if foundIndex, ok := pairsIndex[paramPair.Name]; ok {
			(*paramPairs)[foundIndex] = paramPair
		} else {
			*paramPairs = append(*paramPairs, paramPair)
		}
	}
	return nil
}

// rejectUnknownNamedParams checks that all named overrides match a declared
// parameter name. It only enforces this when at least one default has a
// non-empty, non-numeric Name (i.e. the DAG declares named params).
// Positional defaults get numeric names like "1", "2" from parseParams;
// these are excluded from the declared set so they don't block named overrides.
func rejectUnknownNamedParams(declared []paramPair, overrides []paramPair) error {
	declaredNames := make(map[string]struct{})
	for _, p := range declared {
		if p.Name != "" && !isPositionalName(p.Name) {
			declaredNames[p.Name] = struct{}{}
		}
	}
	if len(declaredNames) == 0 {
		return nil // all positional or no defaults — accept everything
	}

	var unknown []string
	for _, p := range overrides {
		if p.Name == "" || isPositionalName(p.Name) {
			continue
		}
		if _, ok := declaredNames[p.Name]; !ok {
			unknown = append(unknown, p.Name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}

	accepted := make([]string, 0, len(declaredNames))
	for name := range declaredNames {
		accepted = append(accepted, name)
	}
	sort.Strings(accepted)

	return fmt.Errorf(
		"unknown parameter(s): %s; accepted parameters are: %s",
		quotedNames(unknown),
		strings.Join(accepted, ", "),
	)
}

// isPositionalName returns true if the name is a numeric index assigned to
// positional params (e.g. "1", "2", "3").
func isPositionalName(name string) bool {
	_, err := strconv.Atoi(name)
	return err == nil
}

func quotedNames(names []string) string {
	sort.Strings(names)
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(quoted, ", ")
}

// parseParams parses and processes the parameters for the DAG.
// Parameter values are always treated as literal strings — no variable
// expansion or command substitution is performed.
func parseParams(value any, params *[]paramPair, envs *[]string) error {
	noEvalCtx := BuildContext{opts: BuildOpts{Flags: BuildFlagNoEval}}

	paramPairs, err := parseParamValue(noEvalCtx, value)
	if err != nil {
		return core.NewValidationError("params", value, fmt.Errorf("%w: %s", ErrInvalidParamValue, err))
	}

	for index, paramPair := range paramPairs {
		*params = append(*params, paramPair)

		if paramPair.Name != "" {
			*envs = append(*envs, paramPair.String())
		}

		if paramPair.Name == "" {
			(*params)[index].Name = strconv.Itoa(index + 1)
		}
	}

	return nil
}

// parseParamValue parses the parameters for the DAG.
func parseParamValue(ctx BuildContext, input any) ([]paramPair, error) {
	switch v := input.(type) {
	case nil:
		return nil, nil

	case string:
		return parseStringParams(ctx, v)

	case []any:
		return parseMapParams(ctx, v)

	case []string:
		return parseListParams(ctx, v)

	// At this point, the schema input can be two cases:
	// 1. a map with a "schema" key and a "values" key
	// e.g. { "schema": "./schema.json", "values": { "batch_size": 10, "environment": "dev" } }
	// 2. a map with no "schema" key
	// e.g. { "batch_size": 10, "environment": "dev" }
	case map[string]any:
		if _, ok := extractParamsSchemaDeclaration(v); !ok {
			return parseMapParams(ctx, []any{v})
		}

		values, ok := v["values"]
		if !ok {
			return []paramPair{}, nil // Schema-only mode, no values to validate
		}

		return parseMapParams(ctx, []any{values})
	default:
		return nil, core.NewValidationError("params", v, fmt.Errorf("%w: %T", ErrInvalidParamValue, v))

	}
}

func parseListParams(ctx BuildContext, input []string) ([]paramPair, error) {
	var params []paramPair

	for _, v := range input {
		parsedParams, err := parseStringParams(ctx, v)
		if err != nil {
			return nil, err
		}
		params = append(params, parsedParams...)
	}

	return params, nil
}

func parseMapParams(ctx BuildContext, input []any) ([]paramPair, error) {
	var params []paramPair

	for _, m := range input {
		switch m := m.(type) {
		case string:
			parsedParams, err := parseStringParams(ctx, m)
			if err != nil {
				return nil, err
			}
			params = append(params, parsedParams...)

		case map[string]any:
			// Iterate deterministically to avoid random param order from Go maps.
			keys := make([]string, 0, len(m))
			for name := range m {
				keys = append(keys, name)
			}
			sort.Strings(keys)

			for _, name := range keys {
				value := m[name]
				var valueStr string

				switch v := value.(type) {
				case string:
					valueStr = v

				default:
					valueStr = fmt.Sprintf("%v", v)

				}

				paramPair := paramPair{name, valueStr}
				params = append(params, paramPair)
			}

		default:
			return nil, core.NewValidationError("params", m, fmt.Errorf("%w: %T", ErrInvalidParamValue, m))
		}
	}

	return params, nil
}

// paramRegex is a regex to match the parameters in the command.
var paramRegex = regexp.MustCompile(
	`(?:([^\s=]+)=)?("(?:\\"|[^"])*"|` + "`[^`]*`" + `|[^"\s]+)`,
)

// tryParseJSONParams attempts to parse the input as JSON and convert it to paramPairs.
// Returns an error if the input is not valid JSON.
func tryParseJSONParams(ctx BuildContext, input string) ([]paramPair, error) {
	// Try parsing as JSON object first
	var jsonObj map[string]any
	if err := json.Unmarshal([]byte(input), &jsonObj); err == nil {
		return parseMapParams(ctx, []any{jsonObj})
	}

	// Try parsing as JSON array
	var jsonArr []any
	if err := json.Unmarshal([]byte(input), &jsonArr); err == nil {
		var params []paramPair
		for _, item := range jsonArr {
			switch v := item.(type) {
			case string:
				params = append(params, paramPair{Name: "", Value: v})
			case map[string]any:
				mapParams, err := parseMapParams(ctx, []any{v})
				if err != nil {
					return nil, err
				}
				params = append(params, mapParams...)
			default:
				// Convert other types (numbers, booleans) to string
				params = append(params, paramPair{Name: "", Value: fmt.Sprintf("%v", v)})
			}
		}
		return params, nil
	}

	return nil, fmt.Errorf("not valid JSON")
}

func parseStringParams(ctx BuildContext, input string) ([]paramPair, error) {
	input = strings.TrimSpace(input)

	// Check if input looks like a JSON object or array
	if (strings.HasPrefix(input, "{") && strings.HasSuffix(input, "}")) ||
		(strings.HasPrefix(input, "[") && strings.HasSuffix(input, "]")) {
		params, err := tryParseJSONParams(ctx, input)
		if err == nil {
			return params, nil
		}
		// If JSON parsing fails, fall through to regex parsing
	}

	matches := paramRegex.FindAllStringSubmatch(input, -1)

	var params []paramPair

	for _, match := range matches {
		name := match[1]
		value := match[2]

		if strings.HasPrefix(value, `"`) {
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			} else {
				// Fallback for malformed strings (e.g., unterminated quotes)
				value = strings.Trim(value, `"`)
				value = strings.ReplaceAll(value, `\"`, `"`)
			}
		}

		params = append(params, paramPair{name, value})
	}

	return params, nil
}

type paramPair struct {
	Name  string
	Value string
}

func (p paramPair) String() string {
	if p.Name != "" {
		return fmt.Sprintf("%s=%s", p.Name, p.Value)
	}
	return p.Value
}

func (p paramPair) Escaped() string {
	if p.Name != "" {
		return fmt.Sprintf("%s=%q", p.Name, p.Value)
	}
	return fmt.Sprintf("%q", p.Value)
}

// SmartEscape returns a string representation that only quotes values when
// necessary (empty, or containing spaces/tabs/newlines/double-quotes).
// This allows variable references like ${ITEM.xxx} to remain unquoted so
// that their expanded content can be re-split into separate KEY=VALUE pairs.
func (p paramPair) SmartEscape() string {
	needsQuoting := p.Value == "" || strings.ContainsAny(p.Value, " \t\n\"")
	if p.Name != "" {
		if needsQuoting {
			return fmt.Sprintf("%s=%q", p.Name, p.Value)
		}
		return fmt.Sprintf("%s=%s", p.Name, p.Value)
	}
	if needsQuoting {
		return fmt.Sprintf("%q", p.Value)
	}
	return p.Value
}
