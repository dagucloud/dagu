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

	"github.com/dagucloud/dagu/v2/internal/cmn/jsonutil"
)

var (
	reVarSubstitution      = regexp.MustCompile(`\$\{([^}]+)\}|\$([A-Za-z_][A-Za-z0-9_]*)|\$([0-9]+)`)
	bindingRefPattern      = regexp.MustCompile(`\$\{([^}]+)\}`)
	bindingNamePattern     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	quotedReferencePattern = regexp.MustCompile(`"\$\{([A-Za-z0-9_]\w*(?:\.[^}]+)?)\}"`)
	referencePattern       = regexp.MustCompile(`\$\{([A-Za-z0-9_]\w*)(\.[^}]+)\}|\$([A-Za-z0-9_]\w*)(\.[^\s]+)`)
)

var supportedBuiltinContextBindings = map[string]struct{}{
	"context.dag.name":                      {},
	"context.run.id":                        {},
	"context.run.status":                    {},
	"context.run.scheduled_at":              {},
	"context.run.root_name":                 {},
	"context.run.root_id":                   {},
	"context.attempt.id":                    {},
	"context.attempt.started_at":            {},
	"context.step.id":                       {},
	"context.step.name":                     {},
	"context.trigger.type":                  {},
	"context.trigger.actor":                 {},
	"context.paths.log_file":                {},
	"context.paths.work_dir":                {},
	"context.paths.artifacts_dir":           {},
	"context.paths.wiki_dir":                {},
	"context.paths.docs_dir":                {},
	"context.paths.step_stdout_file":        {},
	"context.paths.step_stderr_file":        {},
	"context.paths.step_output_file":        {},
	"context.profile.name":                  {},
	"context.profile.resolved_at":           {},
	"context.pushback.iteration":            {},
	"context.pushback.previous_stdout_file": {},
}

var legacyBuiltinContextAliases = map[string]string{
	"dag.name":                      "context.dag.name",
	"run.id":                        "context.run.id",
	"run.status":                    "context.run.status",
	"run.started_at":                "context.attempt.started_at",
	"run.scheduled_at":              "context.run.scheduled_at",
	"run.root_name":                 "context.run.root_name",
	"run.root_id":                   "context.run.root_id",
	"attempt.id":                    "context.attempt.id",
	"step.id":                       "context.step.id",
	"step.name":                     "context.step.name",
	"trigger.type":                  "context.trigger.type",
	"trigger.actor":                 "context.trigger.actor",
	"paths.log_file":                "context.paths.log_file",
	"paths.work_dir":                "context.paths.work_dir",
	"paths.artifacts_dir":           "context.paths.artifacts_dir",
	"paths.wiki_dir":                "context.paths.wiki_dir",
	"paths.docs_dir":                "context.paths.docs_dir",
	"paths.step_stdout_file":        "context.paths.step_stdout_file",
	"paths.step_stderr_file":        "context.paths.step_stderr_file",
	"paths.step_output_file":        "context.paths.step_output_file",
	"profile.name":                  "context.profile.name",
	"profile.resolved_at":           "context.profile.resolved_at",
	"pushback.iteration":            "context.pushback.iteration",
	"pushback.previous_stdout_file": "context.pushback.previous_stdout_file",
}

var legacyBuiltinContextAliasesByCanonical = func() map[string]string {
	aliases := make(map[string]string, len(legacyBuiltinContextAliases))
	for legacy, canonical := range legacyBuiltinContextAliases {
		aliases[canonical] = legacy
	}
	return aliases
}()

type template struct{ source string }

type protectedReferencesKey struct{}

// protectedReferences maps the placeholders that stand in for references while
// a field is evaluated.
type protectedReferences struct {
	all         *strings.Replacer // placeholder to inserted text or original reference
	toCommand   *strings.Replacer // placeholder to the text a substituted command receives
	fromCommand *strings.Replacer // command token back to its unresolved placeholder
}

func resolveBindings(
	ctx context.Context,
	input string,
	scope RuntimeScope,
	field Field,
	notices ValueReferenceNoticeSink,
) (string, protectedReferences, error) {
	var all, toCommand, fromCommand []string
	// Tokens come from one process-wide counter and all contain "DAGU_", so they
	// only need checking against input that contains it.
	taken := input
	if !strings.Contains(input, "DAGU_") {
		taken = ""
	}
	protectOriginal := func(token string) string {
		placeholder := uniqueToken(taken, "\uE000DAGU_UNRESOLVED_REF_")
		// Shells get an ASCII token because Windows substitution output may not keep other runes.
		commandToken := uniqueToken(taken, "__DAGU_UNRESOLVED_REF__")
		all = append(all, placeholder, token)
		toCommand = append(toCommand, placeholder, commandToken)
		fromCommand = append(fromCommand, commandToken, placeholder)
		return placeholder
	}
	// An item scope owns every foreach path, so quoted fields resolve and escaped ones stay literal.
	itemPath := func(path string) bool {
		return len(scope.Foreach) > 0 && strings.HasPrefix(path, "foreach.")
	}
	owned := func(path string) bool {
		segments := strings.Split(path, ".")
		return itemPath(path) || supportedStrictBinding(segments) || reservedBuiltinContextReference(segments)
	}
	// A leading non-identifier rune keeps an adjacent $NAME from absorbing a placeholder.
	resolved, err := walkBindings(input, field.escapesDollar(), owned, func(token string, path string, escaped bool) (string, error) {
		item := itemPath(path)
		switch {
		case escaped && item:
			return protectOriginal(token), nil
		case escaped:
			return token, nil
		case item && foreachStepReference(ctx, path, scope.Steps):
			// The legacy pass applies the field's quoting rules to step values.
			return token, nil
		}
		var value any
		var err error
		if item && !supportedStrictBinding(strings.Split(path, ".")) {
			segments, ok := quotedForeachField(path)
			if !ok {
				return token, nil
			}
			value, err = bindingForeachValue(path, segments, scope.Foreach, true)
		} else {
			value, err = bindingValue(ctx, path, scope, true)
		}
		if err != nil {
			addUnresolvedReferenceNotice(notices, field.path, token, err)
			return protectOriginal(token), nil
		}
		placeholder := uniqueToken(taken, "\uE000DAGU_RESOLVED_REF_")
		text := formatBindingValue(value)
		all = append(all, placeholder, text)
		toCommand = append(toCommand, placeholder, text)
		return placeholder, nil
	})
	return resolved, protectedReferences{
		all:         newReferenceReplacer(all),
		toCommand:   newReferenceReplacer(toCommand),
		fromCommand: newReferenceReplacer(fromCommand),
	}, err
}

func newReferenceReplacer(replacements []string) *strings.Replacer {
	if len(replacements) == 0 {
		return nil
	}
	return strings.NewReplacer(replacements...)
}

func restoreProtectedReferences(input string, protected *strings.Replacer) string {
	if protected == nil {
		return input
	}
	return protected.Replace(input)
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

func (t template) resolveQuotedReferences(ctx context.Context, r *resolver, style quotedRefStyle) string {
	return quotedReferencePattern.ReplaceAllStringFunc(t.source, func(match string) string {
		ref := match[3 : len(match)-2]
		if value, ok := resolveQuotedReference(ctx, r, ref); ok {
			return quoteQuotedRefValue(value, style)
		}
		return match
	})
}

// quoteQuotedRefValue re-quotes a resolved value for the double-quoted span it
// was resolved inside, using the escape convention the command interpreter
// understands. POSIX shells resolve \" back to a literal quote; PowerShell
// resolves neither that nor the backslash doubling strconv.Quote applies, so
// it needs its own rendering.
func quoteQuotedRefValue(value string, style quotedRefStyle) string {
	switch style {
	case quotedRefPowerShell:
		return quotePowerShellValue(value)
	case quotedRefPOSIX:
		return strconv.Quote(value)
	default:
		return strconv.Quote(value)
	}
}

// quotePowerShellValue renders value as a PowerShell double-quoted string. A
// literal quote doubles rather than taking a backslash, the backtick is the
// escape character and so doubles too, and a control character takes its
// backtick form because a raw one would otherwise end the line.
//
// $ is deliberately left alone. Only a shell named by commandDefersShellVars
// keeps $VAR for the shell to expand; for every other shell the variables
// phase expands it after this one, and it does not recognize a backtick
// escape, so escaping here would strand the backtick against the expanded
// value. Whether a resolved value should be re-expanded at all is a separate
// question from how it is quoted.
func quotePowerShellValue(value string) string {
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`""`)
		case '`':
			b.WriteString("``")
		case '\n':
			b.WriteString("`n")
		case '\r':
			b.WriteString("`r")
		case '\t':
			b.WriteString("`t")
		case 0:
			b.WriteString("`0")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
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
		var unbracedPositional bool
		if loc[2] >= 0 {
			key = t.source[loc[2]:loc[3]]
		} else if loc[4] >= 0 {
			key = t.source[loc[4]:loc[5]]
		} else if loc[6] >= 0 {
			key = t.source[loc[6]:loc[7]]
			unbracedPositional = true
		} else {
			b.WriteString(match)
			continue
		}

		if !validVariableTokenName(key) ||
			(unbracedPositional && numericVarContinues(t.source, key, loc[1])) ||
			strings.Contains(key, ".") {
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

func validVariableTokenName(name string) bool {
	return ValidEnvName(name) || isNumericVar(name)
}

func numericVarContinues(input, name string, end int) bool {
	if !isNumericVar(name) || end >= len(input) {
		return false
	}
	next := input[end]
	return next == '_' ||
		(next >= '0' && next <= '9') ||
		(next >= 'A' && next <= 'Z') ||
		(next >= 'a' && next <= 'z')
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

// walkBindings replaces each owned reference with the result of visit. When
// escapes is set, an escaped owned reference loses its escape marker;
// otherwise a backslash before it is ordinary text.
func walkBindings(input string, escapes bool, owned func(path string) bool, visit func(token, path string, escaped bool) (string, error)) (string, error) {
	var b strings.Builder
	last := 0
	for _, loc := range bindingRefPattern.FindAllStringSubmatchIndex(input, -1) {
		path := strings.TrimSpace(input[loc[2]:loc[3]])
		if !owned(path) {
			continue
		}
		start := loc[0]
		escaped := escapes && isEscapedDollar(input, start)
		if escaped {
			start--
		}
		b.WriteString(input[last:start])
		replacement, err := visit(input[loc[0]:loc[1]], path, escaped)
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
		if reservedBuiltinContextReference(segments) {
			return nil, newNoticeReasonError(
				ValueReferenceReasonUnknownContextField,
				fmt.Sprintf("unknown context field %s", path),
			)
		}
		return nil, nil
	}
	switch segments[0] {
	case "consts":
		return bindingMapValue("consts", segments[1], scope.Consts, requireValue)
	case "params":
		if len(segments) == 1 {
			return bindingParamsJSONValue(scope.ParamsJSON, requireValue)
		}
		return bindingMapValue("params", segments[1], scope.Params, requireValue)
	case "env":
		return bindingEnvValue(segments[1], scope.Env, requireValue)
	case "steps":
		return bindingStepOutputValue(ctx, segments, scope.Steps, requireValue)
	case "foreach":
		return bindingForeachValue(path, segments, scope.Foreach, requireValue)
	case "inputs":
		return bindingScopedPathValue("inputs", segments[1], scope.Inputs, requireValue)
	case "outputs":
		return bindingScopedPathValue("outputs", segments[1], scope.Outputs, requireValue)
	case "context", "dag", "run", "attempt", "step", "trigger", "paths", "profile", "pushback":
		return bindingBuiltinContextValue(path, scope.BuiltinContext, requireValue)
	default:
		return nil, nil
	}
}

func bindingScopedPathValue(namespace, name string, values Values, requireValue bool) (any, error) {
	if value, ok := values[name]; ok {
		return value, nil
	}
	if !requireValue {
		return nil, nil
	}
	if len(values) > 0 {
		return nil, fmt.Errorf("unknown %s.%s binding", namespace, name)
	}
	return nil, newNoticeReasonError(
		ValueReferenceReasonNamespaceUnavailable,
		fmt.Sprintf("%s.%s is unavailable in this context", namespace, name),
	)
}

func bindingParamsJSONValue(value string, requireValue bool) (any, error) {
	if value != "" {
		return value, nil
	}
	if !requireValue {
		return nil, nil
	}
	return nil, newNoticeReasonError(
		ValueReferenceReasonNamespaceUnavailable,
		"params is unavailable in this context",
	)
}

func bindingBuiltinContextValue(path string, builtins BuiltinContext, requireValue bool) (any, error) {
	if value, ok := builtins.Value(path); ok {
		return value, nil
	}
	if !requireValue {
		return nil, nil
	}
	return nil, newNoticeReasonError(
		ValueReferenceReasonNamespaceUnavailable,
		fmt.Sprintf("%s is populated during a DAG run", path),
	)
}

func bindingStepOutputValue(ctx context.Context, segments []string, steps map[string]StepInfo, requireValue bool) (any, error) {
	if len(steps) == 0 && !requireValue {
		return nil, nil
	}
	stepName := segments[1]
	outputName := segments[3]
	value, ok := resolveDeclaredStepOutput(ctx, stepName, outputName, steps)
	if ok {
		return value, nil
	}
	if !requireValue {
		return nil, nil
	}
	return nil, fmt.Errorf("unknown steps.%s.outputs.%s binding", stepName, outputName)
}

func bindingMapValue(namespace, name string, values Values, requireValue bool) (any, error) {
	if values == nil && !requireValue {
		return nil, nil
	}
	value, ok := values[name]
	if !ok {
		if namespace == "params" {
			// A caller can pass a param the spec never declared, so an absent
			// one says nothing about the spec being wrong.
			return nil, fmt.Errorf("unknown params.%s binding", name)
		}
		if namespace == "consts" {
			return nil, newNoticeReasonError(
				ValueReferenceReasonUnknownConstName,
				fmt.Sprintf("unknown consts.%s binding", name),
			)
		}
		return nil, fmt.Errorf("unknown %s binding %q", namespace, name)
	}
	return value, nil
}

func quotedForeachField(path string) ([]string, bool) {
	itemPath, ok := strings.CutPrefix(path, "foreach.")
	if !ok {
		return nil, false
	}
	end := strings.IndexAny(itemPath, ".[")
	if end < 0 || !bindingNamePattern.MatchString(itemPath[:end]) {
		return nil, false
	}
	quoted := itemPath[end+1:]
	if itemPath[end] == '[' {
		quoted, ok = strings.CutSuffix(quoted, "]")
		if !ok {
			return nil, false
		}
	}
	quoted = strings.TrimSpace(quoted)
	if !strings.HasPrefix(quoted, `"`) {
		return nil, false
	}
	var field string
	if err := json.Unmarshal([]byte(quoted), &field); err != nil {
		return nil, false
	}
	return []string{"foreach", itemPath[:end], field}, true
}

// foreachStepReference reports whether path reads a property of a step named
// foreach. Such a step takes precedence over the item scope.
func foreachStepReference(ctx context.Context, path string, steps map[string]StepInfo) bool {
	if _, ok := steps["foreach"]; !ok {
		return false
	}
	_, ok := resolveStepProperty(ctx, "foreach", strings.TrimPrefix(path, "foreach"), steps)
	return ok
}

func bindingForeachValue(path string, segments []string, values Values, requireValue bool) (any, error) {
	if len(values) == 0 {
		if !requireValue {
			return nil, nil
		}
		return nil, newNoticeReasonError(
			ValueReferenceReasonNamespaceUnavailable,
			fmt.Sprintf("%s is unavailable in this context", path),
		)
	}

	value, ok := values[segments[1]]
	if !ok {
		if !requireValue {
			return nil, nil
		}
		return nil, newNoticeReasonError(
			ValueReferenceReasonNamespaceUnavailable,
			fmt.Sprintf("%s is unavailable in this context", path),
		)
	}

	if len(segments) == 2 {
		return formatForeachItemValue(value), nil
	}

	field, ok := foreachObjectField(value, segments[2])
	if !ok {
		if !requireValue {
			return nil, nil
		}
		return nil, newNoticeReasonError(
			ValueReferenceReasonNamespaceUnavailable,
			fmt.Sprintf("%s is unavailable in this context", path),
		)
	}
	return formatForeachItemValue(field), nil
}

func foreachObjectField(value any, field string) (any, bool) {
	switch v := value.(type) {
	case map[string]any:
		got, ok := v[field]
		return got, ok
	case map[string]string:
		got, ok := v[field]
		return got, ok
	default:
		return nil, false
	}
}

func formatForeachItemValue(value any) any {
	switch value.(type) {
	case map[string]any, map[string]string, []any, []string:
		data, err := jsonutil.MarshalUnescaped(value)
		if err != nil {
			return value
		}
		return string(data)
	default:
		return value
	}
}

func bindingEnvValue(name string, scope *EnvScope, requireValue bool) (any, error) {
	if scope == nil && !requireValue {
		return nil, nil
	}
	if value, ok := scope.Get(name); ok {
		return value, nil
	}
	if !requireValue {
		return nil, nil
	}
	return nil, newNoticeReasonError(
		ValueReferenceReasonUnknownEnvBinding,
		fmt.Sprintf("unknown env.%s binding", name),
	)
}

func supportedStrictBinding(segments []string) bool {
	switch segments[0] {
	case "consts":
		return len(segments) == 2 && bindingNamePattern.MatchString(segments[1])
	case "params":
		if len(segments) == 1 {
			return true
		}
		return len(segments) == 2 && bindingNamePattern.MatchString(segments[1])
	case "env":
		return len(segments) == 2 && ValidEnvName(segments[1])
	case "steps":
		if len(segments) != 4 || segments[2] != "outputs" {
			return false
		}
		if !validStepOutputStepName(segments[1]) {
			return false
		}
		return validOutputPathSegment(segments[3])
	case "foreach":
		if len(segments) != 2 && len(segments) != 3 {
			return false
		}
		if !bindingNamePattern.MatchString(segments[1]) {
			return false
		}
		// An item field is a data key, so it may start with an underscore,
		// as the _row a workbook reader attaches to each row does, and use
		// any script, as workbook headers do.
		return len(segments) == 2 || validItemFieldSegment(segments[2])
	case "inputs", "outputs":
		return len(segments) == 2 && bindingNamePattern.MatchString(segments[1])
	case "context":
		if len(segments) != 3 {
			return false
		}
		_, ok := supportedBuiltinContextBindings[strings.Join(segments, ".")]
		return ok
	case "dag", "run", "attempt", "step", "trigger", "paths", "profile", "pushback":
		if len(segments) != 2 {
			return false
		}
		_, ok := legacyBuiltinContextAliases[strings.Join(segments, ".")]
		return ok
	default:
		return false
	}
}

func canonicalBuiltinContextPath(path string) (string, bool) {
	if _, ok := supportedBuiltinContextBindings[path]; ok {
		return path, true
	}
	canonical, ok := legacyBuiltinContextAliases[path]
	return canonical, ok
}

func legacyBuiltinContextPath(path string) (string, bool) {
	legacy, ok := legacyBuiltinContextAliasesByCanonical[path]
	return legacy, ok
}

func reservedBuiltinContextReference(segments []string) bool {
	if len(segments) < 2 || !validBuiltinContextSegments(segments) {
		return false
	}
	if segments[0] == "context" {
		return true
	}
	return false
}

func validBuiltinContextSegments(segments []string) bool {
	for _, segment := range segments {
		if !bindingNamePattern.MatchString(segment) {
			return false
		}
	}
	return true
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
