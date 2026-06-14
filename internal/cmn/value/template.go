// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	reVarSubstitution       = regexp.MustCompile(`\$\{([^}]+)\}|\$([a-zA-Z0-9_][a-zA-Z0-9_]*)`)
	bindingRefPattern       = regexp.MustCompile(`\$\{([^}]+)\}`)
	bindingShorthandPattern = regexp.MustCompile(`\$(consts|params|env|steps)(\.[A-Za-z_][A-Za-z0-9_]*)+`)
	bindingNamePattern      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	bindingEnvNamePattern   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	quotedReferencePattern  = regexp.MustCompile(`"\$\{([A-Za-z0-9_]\w*(?:\.[^}]+)?)\}"`)
	referencePattern        = regexp.MustCompile(`\$\{([A-Za-z0-9_]\w*)(\.[^}]+)\}|\$([A-Za-z0-9_]\w*)(\.[^\s]+)`)
)

type Values map[string]any
type StepOutputs map[string]Values
type StepOutputNames map[string]struct{}
type StepOutputContracts map[string]StepOutputNames

// StaticScope contains declarations and contracts used by static validation.
type StaticScope struct {
	Consts Values
	Params Values
	Env    Values
	Steps  StepOutputContracts
}

// RuntimeScope contains actual values available during runtime resolution.
type RuntimeScope struct {
	Consts  Values
	Params  Values
	Env     Values
	Steps   StepOutputs
	StepMap map[string]StepInfo
}

type Scope = RuntimeScope

// ValuesFromStrings converts string variables into binding values.
func ValuesFromStrings(values map[string]string) Values {
	if len(values) == 0 {
		return nil
	}
	out := make(Values, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

type template struct{ source string }

func checkBindings(input string, scope RuntimeScope) error {
	if match := bindingShorthandPattern.FindString(input); match != "" {
		return fmt.Errorf("invalid binding shorthand %s; use ${...}", match)
	}
	if malformed := malformedBinding(input); malformed != "" {
		return fmt.Errorf("malformed binding %s", malformed)
	}
	_, err := walkBindings(input, func(token, path string) (string, error) {
		_, err := bindingValue(path, scope, false)
		return token, err
	})
	return err
}

func resolveBindings(input string, scope RuntimeScope) (string, error) {
	if err := checkBindings(input, scope); err != nil {
		return "", err
	}
	return walkBindings(input, func(_ string, path string) (string, error) {
		value, err := bindingValue(path, scope, true)
		if err != nil {
			return "", err
		}
		return formatBindingValue(value), nil
	})
}

func (t template) resolveReferences(ctx context.Context, r *resolver) string {
	return referencePattern.ReplaceAllStringFunc(t.source, func(match string) string {
		subMatches := referencePattern.FindStringSubmatch(match)
		if len(subMatches) < 3 {
			return match
		}

		var varName, path string
		if strings.HasPrefix(subMatches[0], "${") {
			varName = subMatches[1]
			path = subMatches[2]
		} else {
			varName = subMatches[3]
			path = subMatches[4]
		}

		if value, ok := r.resolveReference(ctx, varName, path); ok {
			return value
		}
		return match
	})
}

func (t template) resolveQuotedReferences(ctx context.Context, r *resolver) string {
	return quotedReferencePattern.ReplaceAllStringFunc(t.source, func(match string) string {
		ref := match[3 : len(match)-2]

		var value string
		var ok bool
		if dotIdx := strings.Index(ref, "."); dotIdx >= 0 {
			value, ok = r.resolveReference(ctx, ref[:dotIdx], ref[dotIdx:])
		} else {
			value, ok = r.resolve(ref)
		}

		if ok {
			return strconv.Quote(value)
		}
		return match
	})
}

func (t template) resolveVariables(r *resolver) string {
	matches := reVarSubstitution.FindAllStringSubmatchIndex(t.source, -1)
	if len(matches) == 0 {
		return t.source
	}

	var b strings.Builder
	last := 0
	for _, loc := range matches {
		b.WriteString(t.source[last:loc[0]])
		last = loc[1]

		match := t.source[loc[0]:loc[1]]
		if isSingleQuotedVar(t.source, loc[0], loc[1]) || isEscapedDollar(t.source, loc[0]) {
			b.WriteString(match)
			continue
		}

		var key string
		if loc[2] >= 0 {
			key = t.source[loc[2]:loc[3]]
		} else if loc[4] >= 0 {
			key = t.source[loc[4]:loc[5]]
		} else {
			b.WriteString(match)
			continue
		}

		if strings.Contains(key, ".") {
			b.WriteString(match)
			continue
		}
		if value, found := r.resolveForReplace(key); found {
			b.WriteString(value)
			continue
		}
		b.WriteString(match)
	}

	b.WriteString(t.source[last:])
	return b.String()
}

// isSingleQuotedVar reports whether the matched variable token is enclosed
// in single quotes in the original input.
func isSingleQuotedVar(input string, start, end int) bool {
	return start > 0 && end < len(input) && input[start-1] == '\'' && input[end] == '\''
}

func walkBindings(input string, visit func(token, path string) (string, error)) (string, error) {
	var b strings.Builder
	last := 0
	for _, loc := range bindingRefPattern.FindAllStringSubmatchIndex(input, -1) {
		path := strings.TrimSpace(input[loc[2]:loc[3]])
		if !reservedBinding(strings.Split(path, ".")[0]) {
			continue
		}
		b.WriteString(input[last:loc[0]])
		replacement, err := visit(input[loc[0]:loc[1]], path)
		if err != nil {
			return "", err
		}
		b.WriteString(replacement)
		last = loc[1]
	}
	b.WriteString(input[last:])
	return b.String(), nil
}

func bindingValue(path string, scope RuntimeScope, requireValue bool) (any, error) {
	segments := strings.Split(path, ".")
	if err := validateBindingSegments(segments); err != nil {
		return nil, err
	}
	switch segments[0] {
	case "consts":
		return bindingMapValue("consts", segments[1], scope.Consts, requireValue)
	case "params":
		return bindingMapValue("params", segments[1], scope.Params, requireValue)
	case "env":
		return bindingMapValue("env", segments[1], scope.Env, requireValue)
	case "steps":
		if scope.Steps == nil && !requireValue {
			return nil, nil
		}
		values, ok := scope.Steps[segments[1]]
		if !ok {
			return nil, fmt.Errorf("unknown steps binding ${%s}", path)
		}
		return bindingMapValue("steps", segments[3], values, requireValue)
	default:
		return nil, nil
	}
}

func bindingMapValue(namespace, name string, values Values, requireValue bool) (any, error) {
	if values == nil && !requireValue {
		return nil, nil
	}
	value, ok := values[name]
	if !ok {
		return nil, fmt.Errorf("unknown %s binding %q", namespace, name)
	}
	return value, nil
}

func validateBindingSegments(segments []string) error {
	if len(segments) == 0 {
		return nil
	}
	namespace := segments[0]
	for idx, segment := range segments {
		pattern := bindingNamePattern
		if namespace == "env" && idx == 1 {
			pattern = bindingEnvNamePattern
		}
		if !pattern.MatchString(segment) {
			return fmt.Errorf("binding path segment %q is invalid", segment)
		}
	}
	switch namespace {
	case "consts", "params", "env":
		if len(segments) != 2 {
			return fmt.Errorf("%s bindings must use ${%s.<name>}", namespace, namespace)
		}
	case "steps":
		if len(segments) != 4 || segments[2] != "outputs" {
			return fmt.Errorf("steps bindings must use ${steps.<step_id>.outputs.<name>}")
		}
	}
	return nil
}

func reservedBinding(namespace string) bool {
	switch namespace {
	case "consts", "params", "env", "steps":
		return true
	default:
		return false
	}
}

func malformedBinding(value string) string {
	for offset := 0; offset < len(value); {
		start := strings.Index(value[offset:], "${")
		if start < 0 {
			return ""
		}
		start += offset
		end := strings.IndexByte(value[start+2:], '}')
		if end < 0 {
			expr := strings.TrimSpace(strings.TrimPrefix(value[start:], "${"))
			if reservedBinding(expr) || strings.Contains(expr, ".") && reservedBinding(strings.Split(expr, ".")[0]) {
				return value[start:]
			}
			return ""
		}
		offset = start + 2 + end + 1
	}
	return ""
}

func formatBindingValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case int, int8, int16, int32, int64:
		return fmt.Sprintf("%d", v)
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", v)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}
