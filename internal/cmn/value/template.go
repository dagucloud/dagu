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
	bindingShorthandPattern = regexp.MustCompile(`\$consts(\.[A-Za-z_][A-Za-z0-9_]*)+`)
	bindingNamePattern      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	quotedReferencePattern  = regexp.MustCompile(`"\$\{([A-Za-z0-9_]\w*(?:\.[^}]+)?)\}"`)
	referencePattern        = regexp.MustCompile(`\$\{([A-Za-z0-9_]\w*)(\.[^}]+)\}|\$([A-Za-z0-9_]\w*)(\.[^\s]+)`)
)

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
		if !reservedBinding(bindingNamespace(path)) {
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
	for _, segment := range segments {
		if !bindingNamePattern.MatchString(segment) {
			return fmt.Errorf("binding path segment %q is invalid", segment)
		}
	}
	if segments[0] == "consts" && len(segments) != 2 {
		return fmt.Errorf("consts bindings must use ${consts.<name>}")
	}
	return nil
}

func reservedBinding(namespace string) bool {
	return namespace == "consts"
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
