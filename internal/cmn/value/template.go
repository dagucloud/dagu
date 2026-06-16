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
	reVarSubstitution      = regexp.MustCompile(`\$\{([^}]+)\}|\$([a-zA-Z0-9_][a-zA-Z0-9_]*)`)
	bindingRefPattern      = regexp.MustCompile(`\$\{([^}]+)\}`)
	bindingNamePattern     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	quotedReferencePattern = regexp.MustCompile(`"\$\{([A-Za-z0-9_]\w*(?:\.[^}]+)?)\}"`)
	referencePattern       = regexp.MustCompile(`\$\{([A-Za-z0-9_]\w*)(\.[^}]+)\}|\$([A-Za-z0-9_]\w*)(\.[^\s]+)`)
)

type template struct{ source string }

func checkBindings(ctx context.Context, input string, scope RuntimeScope) error {
	_, err := walkBindings(input, func(token, path string) (string, error) {
		_, err := bindingValue(ctx, path, scope, false)
		return token, err
	})
	return err
}

func resolveBindings(ctx context.Context, input string, scope RuntimeScope) (string, error) {
	if err := checkBindings(ctx, input, scope); err != nil {
		return "", err
	}
	return walkBindings(input, func(_ string, path string) (string, error) {
		value, err := bindingValue(ctx, path, scope, true)
		if err != nil {
			return "", err
		}
		return formatBindingValue(value), nil
	})
}

func (t template) resolveReferences(ctx context.Context, r *resolver) string {
	return referencePattern.ReplaceAllStringFunc(t.source, func(match string) string {
		varName, path, ok := referenceParts(match)
		if !ok {
			return match
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
		if value, ok := resolveQuotedReference(ctx, r, ref); ok {
			return strconv.Quote(value)
		}
		return match
	})
}

func referenceParts(match string) (string, string, bool) {
	subMatches := referencePattern.FindStringSubmatch(match)
	if len(subMatches) != 5 {
		return "", "", false
	}
	if strings.HasPrefix(match, "${") {
		return subMatches[1], subMatches[2], subMatches[1] != ""
	}
	return subMatches[3], subMatches[4], subMatches[3] != ""
}

func resolveQuotedReference(ctx context.Context, r *resolver, ref string) (string, bool) {
	if name, path, ok := strings.Cut(ref, "."); ok {
		return r.resolveReference(ctx, name, "."+path)
	}
	return r.resolve(ref)
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
		if isSingleQuotedVar(t.source, loc[0], loc[1]) ||
			(r.recognizeEscapedDollar && isEscapedDollar(t.source, loc[0])) {
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

// isSingleQuotedVar reports whether the matched variable token starts inside
// a single-quoted span in the original input.
func isSingleQuotedVar(input string, start, end int) bool {
	inSingleQuote := false
	for i := range start {
		if input[i] != '\'' || isEscapedSingleQuote(input, i) {
			continue
		}
		if inSingleQuote {
			inSingleQuote = false
			continue
		}
		if singleQuoteCanOpen(input, i) {
			inSingleQuote = true
		}
	}
	if !inSingleQuote {
		return false
	}
	for i := end; i < len(input); i++ {
		if input[i] == '\'' && !isEscapedSingleQuote(input, i) {
			return true
		}
	}
	return false
}

func singleQuoteCanOpen(input string, idx int) bool {
	if idx == 0 {
		return true
	}
	switch input[idx-1] {
	case '$', '}':
		return false
	default:
		return true
	}
}

func isEscapedSingleQuote(input string, idx int) bool {
	backslashes := 0
	for i := idx - 1; i >= 0 && input[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func walkBindings(input string, visit func(token, path string) (string, error)) (string, error) {
	var b strings.Builder
	last := 0
	for _, loc := range bindingRefPattern.FindAllStringSubmatchIndex(input, -1) {
		path := strings.TrimSpace(input[loc[2]:loc[3]])
		if !supportedStrictBinding(strings.Split(path, ".")) {
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

func bindingValue(ctx context.Context, path string, scope RuntimeScope, requireValue bool) (any, error) {
	segments := strings.Split(path, ".")
	if !supportedStrictBinding(segments) {
		return nil, nil
	}
	switch segments[0] {
	case "consts":
		return bindingMapValue("consts", segments[1], scope.Consts, requireValue)
	case "params":
		return bindingMapValue("params", segments[1], scope.Params, requireValue)
	case "steps":
		return bindingStepOutputValue(ctx, segments, scope.Steps, requireValue)
	default:
		return nil, nil
	}
}

func bindingStepOutputValue(ctx context.Context, segments []string, steps map[string]StepInfo, requireValue bool) (any, error) {
	if len(steps) == 0 && !requireValue {
		return nil, nil
	}
	stepName := segments[1]
	path := "." + strings.Join(segments[2:], ".")
	value, ok := resolveStepProperty(ctx, stepName, path, steps)
	if ok {
		return value, nil
	}
	if !requireValue {
		return nil, nil
	}
	return nil, fmt.Errorf("unknown steps.%s.%s binding", stepName, strings.Join(segments[2:], "."))
}

func bindingMapValue(namespace, name string, values Values, requireValue bool) (any, error) {
	if values == nil && !requireValue {
		return nil, nil
	}
	value, ok := values[name]
	if !ok {
		if namespace == "params" {
			return nil, fmt.Errorf("unknown params.%s binding", name)
		}
		return nil, fmt.Errorf("unknown %s binding %q", namespace, name)
	}
	return value, nil
}

func supportedStrictBinding(segments []string) bool {
	switch segments[0] {
	case "consts", "params":
		return len(segments) == 2 &&
			bindingNamePattern.MatchString(segments[0]) &&
			bindingNamePattern.MatchString(segments[1])
	case "steps":
		if len(segments) < 4 || segments[2] != "outputs" {
			return false
		}
		if !validStepOutputStepName(segments[1]) {
			return false
		}
		for _, segment := range segments[3:] {
			if !validOutputPathSegment(segment) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func strictBindingNamespace(namespace string) bool {
	switch namespace {
	case "consts", "params":
		return true
	default:
		return false
	}
}

func validateBindingSegments(segments []string) error {
	if len(segments) == 0 {
		return nil
	}
	for _, segment := range segments {
		if !bindingNamePattern.MatchString(segment) {
			return fmt.Errorf("binding path segment %q is invalid", segment)
		}
	}
	switch segments[0] {
	case "consts":
		if len(segments) != 2 {
			return fmt.Errorf("consts bindings must use ${consts.<name>}")
		}
	case "params":
		if len(segments) != 2 {
			return fmt.Errorf("params bindings must use ${params.<name>}")
		}
	}
	return nil
}

func reservedBinding(namespace string) bool {
	return strictBindingNamespace(namespace)
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
			if reservedBinding(bindingNamespace(expr)) {
				return value[start:]
			}
			return ""
		}
		offset = start + 2 + end + 1
	}
	return ""
}

func bindingNamespace(path string) string {
	namespace, _, _ := strings.Cut(path, ".")
	return namespace
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
