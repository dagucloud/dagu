// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/eval"
)

var (
	runtimeValueBracedRefPattern   = regexp.MustCompile(`\$\{([^}]+)\}`)
	runtimeValueShorthandPattern   = regexp.MustCompile(`\$(consts|params|steps)(\.[A-Za-z][A-Za-z0-9_]*)+`)
	runtimeValueIdentifierPattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	runtimeValueSupportedNamespace = map[string]struct{}{
		"consts": {},
		"params": {},
		"steps":  {},
	}
)

func (e Env) resolveDAGValues(ctx context.Context, input string, opts ...eval.Option) (string, error) {
	options := eval.NewOptions()
	for _, opt := range opts {
		opt(options)
	}
	if input == "" || options.NoExpansion {
		return input, nil
	}

	for _, match := range runtimeValueShorthandPattern.FindAllString(input, -1) {
		return "", fmt.Errorf("%s is invalid Dagu-looking reference syntax; use ${...}", match)
	}
	for _, malformed := range malformedRuntimeValueReferences(input) {
		return "", fmt.Errorf("malformed Dagu reference %s", malformed)
	}

	matches := runtimeValueBracedRefPattern.FindAllStringSubmatchIndex(input, -1)
	if len(matches) == 0 {
		return input, nil
	}

	var b strings.Builder
	last := 0
	for _, loc := range matches {
		b.WriteString(input[last:loc[0]])
		last = loc[1]

		expr := strings.TrimSpace(input[loc[2]:loc[3]])
		value, handled, err := e.resolveDAGValue(ctx, expr)
		if err != nil {
			return "", err
		}
		if handled {
			b.WriteString(value)
		} else {
			b.WriteString(input[loc[0]:loc[1]])
		}
	}
	b.WriteString(input[last:])

	return b.String(), nil
}

func (e Env) resolveDAGValue(ctx context.Context, expr string) (string, bool, error) {
	segments := strings.Split(expr, ".")
	if len(segments) == 1 {
		if _, ok := runtimeValueSupportedNamespace[segments[0]]; ok {
			return "", false, validateRuntimeValuePath(segments[0], segments)
		}
		return "", false, nil
	}

	namespace := segments[0]
	if _, ok := runtimeValueSupportedNamespace[namespace]; !ok {
		return "", false, nil
	}
	if err := validateRuntimeValuePath(namespace, segments); err != nil {
		return "", false, err
	}

	switch namespace {
	case "consts":
		if e.DAG == nil {
			return "", false, fmt.Errorf("unknown consts reference ${%s}", expr)
		}
		value, ok := e.DAG.Consts[segments[1]]
		if !ok {
			return "", false, fmt.Errorf("unknown consts reference ${%s}", expr)
		}
		return formatRuntimeValue(value), true, nil
	case "params":
		value, ok := e.resolveRuntimeParam(segments[1])
		if !ok {
			return "", false, fmt.Errorf("unknown params reference ${%s}", expr)
		}
		return value, true, nil
	case "steps":
		value, err := e.resolveRuntimeStepOutput(ctx, segments[1], segments[3])
		if err != nil {
			return "", false, fmt.Errorf("unknown steps output reference ${%s}: %w", expr, err)
		}
		return value, true, nil
	default:
		return "", false, nil
	}
}

func malformedRuntimeValueReferences(value string) []string {
	var malformed []string
	for offset := 0; offset < len(value); {
		start := strings.Index(value[offset:], "${")
		if start < 0 {
			break
		}
		start += offset
		end := strings.IndexByte(value[start+2:], '}')
		if end < 0 {
			candidate := value[start:]
			if isRuntimeDaguReferencePrefix(candidate) {
				malformed = append(malformed, candidate)
			}
			break
		}
		if end == 0 {
			malformed = append(malformed, "${}")
		}
		offset = start + 2 + end + 1
	}
	return malformed
}

func isRuntimeDaguReferencePrefix(value string) bool {
	expr := strings.TrimSpace(strings.TrimPrefix(value, "${"))
	for namespace := range runtimeValueSupportedNamespace {
		if expr == namespace || strings.HasPrefix(expr, namespace+".") {
			return true
		}
	}
	return false
}

func validateRuntimeValuePath(namespace string, segments []string) error {
	for _, segment := range segments {
		if !runtimeValueIdentifierPattern.MatchString(segment) {
			return fmt.Errorf("reference path segment %q must match %s", segment, runtimeValueIdentifierPattern.String())
		}
	}

	switch namespace {
	case "consts", "params":
		if len(segments) != 2 {
			return fmt.Errorf("%s references must use ${%s.<name>}", namespace, namespace)
		}
	case "steps":
		if len(segments) != 4 || segments[2] != "outputs" {
			return fmt.Errorf("steps references must use ${steps.<step_id>.outputs.<name>}")
		}
	}
	return nil
}

func (e Env) resolveRuntimeParam(name string) (string, bool) {
	if e.DAG != nil {
		params := e.DAG.ParamsMap()
		if value, ok := params[name]; ok {
			return value, true
		}
	}
	if e.Scope == nil {
		return "", false
	}
	entry, ok := e.Scope.GetEntry(name)
	if !ok || entry.Source != eval.EnvSourceParam {
		return "", false
	}
	return entry.Value, true
}

func (e Env) resolveRuntimeStepOutput(_ context.Context, stepID string, name string) (string, error) {
	info, ok := e.StepMap[stepID]
	if !ok {
		return "", fmt.Errorf("step %s is unavailable", stepID)
	}
	if info.Outputs == nil {
		return "", fmt.Errorf("step %s has no published outputs", stepID)
	}

	var outputs map[string]any
	if err := json.Unmarshal([]byte(*info.Outputs), &outputs); err != nil {
		return "", fmt.Errorf("step %s outputs are not a JSON object: %w", stepID, err)
	}
	value, ok := outputs[name]
	if !ok {
		return "", fmt.Errorf("output %s is unavailable", name)
	}
	return formatRuntimeValue(value), nil
}

func formatRuntimeValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.FormatInt(int64(v), 10)
	case int8:
		return strconv.FormatInt(int64(v), 10)
	case int16:
		return strconv.FormatInt(int64(v), 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case uint8:
		return strconv.FormatUint(uint64(v), 10)
	case uint16:
		return strconv.FormatUint(uint64(v), 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case float32:
		return formatRuntimeFloat(float64(v), 32)
	case float64:
		return formatRuntimeFloat(v, 64)
	case json.Number:
		return v.String()
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(data)
	}
}

func formatRuntimeFloat(value float64, bitSize int) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return ""
	}
	return strconv.FormatFloat(value, 'f', -1, bitSize)
}
