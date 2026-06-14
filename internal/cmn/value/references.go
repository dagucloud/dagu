// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

var stepOutputReferencePattern = regexp.MustCompile(`\$\{([A-Za-z0-9_-]+)\.output\.([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\}`)

// Mode identifies the workflow phase that owns a value.
type Mode int

const (
	ModeConstLoad Mode = iota
	ModeStaticValidation
	ModeWorkflowValue
	ModeShellCommand
	ModeDirectCommand
	ModeDynamicEval
)

func (m Mode) String() string {
	switch m {
	case ModeConstLoad:
		return "const-load"
	case ModeStaticValidation:
		return "static-validation"
	case ModeWorkflowValue:
		return "workflow-value"
	case ModeShellCommand:
		return "shell-command"
	case ModeDirectCommand:
		return "direct-command"
	case ModeDynamicEval:
		return "dynamic-eval"
	default:
		return fmt.Sprintf("mode(%d)", m)
	}
}

// ReferenceKind classifies a placeholder found in a value string.
type ReferenceKind string

const (
	ReferenceDagu          ReferenceKind = "dagu"
	ReferenceCompatibility ReferenceKind = "compatibility"
	ReferenceInvalid       ReferenceKind = "invalid"
)

// Reference describes one scanned placeholder.
type Reference struct {
	Raw       string
	Expr      string
	Namespace string
	Segments  []string
	Kind      ReferenceKind
	Strict    bool
	Start     int
	End       int
	Err       error
}

// StepOutputReference describes a legacy step output reference.
type StepOutputReference struct {
	Expression string
	StepName   string
	Path       []string
}

// ScanReferences classifies Dagu-owned references and legacy compatibility refs.
func ScanReferences(raw string, mode Mode) []Reference {
	if raw == "" {
		return nil
	}

	refs := make([]Reference, 0)
	for _, loc := range bindingRefPattern.FindAllStringSubmatchIndex(raw, -1) {
		expr := strings.TrimSpace(raw[loc[2]:loc[3]])
		refs = append(refs, classifyStrictReference(raw[loc[0]:loc[1]], expr, loc[0], loc[1]))
	}
	for _, loc := range referencePattern.FindAllStringSubmatchIndex(raw, -1) {
		if loc[0]+1 < len(raw) && raw[loc[0]+1] == '{' {
			continue
		}
		rawRef := raw[loc[0]:loc[1]]
		namespace := raw[loc[6]:loc[7]]
		expr := namespace + raw[loc[8]:loc[9]]
		ref := Reference{
			Raw:       rawRef,
			Expr:      expr,
			Namespace: namespace,
			Segments:  strings.Split(expr, "."),
			Kind:      ReferenceCompatibility,
			Start:     loc[0],
			End:       loc[1],
		}
		if reservedBinding(namespace) {
			ref.Kind = ReferenceInvalid
			ref.Err = fmt.Errorf("invalid binding shorthand %s; use ${%s}", rawRef, expr)
		}
		refs = append(refs, ref)
	}

	sort.SliceStable(refs, func(i, j int) bool {
		return refs[i].Start < refs[j].Start
	})
	return refs
}

// ScanStepOutputReferences returns legacy ${step.output.path} references.
func ScanStepOutputReferences(raw string) []StepOutputReference {
	matches := stepOutputReferencePattern.FindAllStringSubmatch(raw, -1)
	if len(matches) == 0 {
		return nil
	}
	refs := make([]StepOutputReference, 0, len(matches))
	for _, match := range matches {
		if len(match) != 3 {
			continue
		}
		refs = append(refs, StepOutputReference{
			Expression: match[0],
			StepName:   match[1],
			Path:       strings.Split(match[2], "."),
		})
	}
	return refs
}

func classifyStrictReference(rawRef, expr string, start, end int) Reference {
	segments := strings.Split(expr, ".")
	ref := Reference{
		Raw:       rawRef,
		Expr:      expr,
		Namespace: segments[0],
		Segments:  segments,
		Strict:    true,
		Start:     start,
		End:       end,
	}
	if reservedBinding(ref.Namespace) {
		ref.Kind = ReferenceDagu
		if err := validateBindingSegments(segments); err != nil {
			ref.Kind = ReferenceInvalid
			ref.Err = err
		}
		return ref
	}
	if strings.Contains(expr, ".") {
		ref.Kind = ReferenceCompatibility
	}
	return ref
}

// ValidateReferences validates Dagu-owned references against a static scope.
func ValidateReferences(raw string, staticScope Scope, mode Mode, field string) error {
	refs := ScanReferences(raw, mode)
	var errs []string
	for _, ref := range refs {
		switch ref.Kind {
		case ReferenceInvalid:
			errs = append(errs, ref.Err.Error())
		case ReferenceDagu:
			if err := validateDaguReference(ref, staticScope, mode); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}
	if len(errs) == 0 {
		return nil
	}
	if field != "" {
		return fmt.Errorf("%s: %s", field, strings.Join(errs, "; "))
	}
	return fmt.Errorf("%s", strings.Join(errs, "; "))
}

func validateDaguReference(ref Reference, scope Scope, mode Mode) error {
	if mode == ModeConstLoad && ref.Namespace != "consts" {
		return fmt.Errorf("%s is not available while loading consts", ref.Raw)
	}
	switch ref.Namespace {
	case "consts":
		return validateMapReference(ref, "consts", scope.Consts)
	case "params":
		return validateMapReference(ref, "params", scope.Params)
	case "env":
		return validateMapReference(ref, "env", scope.Env)
	case "steps":
		if len(ref.Segments) != 4 {
			return validateBindingSegments(ref.Segments)
		}
		if scope.Steps == nil {
			return fmt.Errorf("unknown steps reference %s", ref.Raw)
		}
		outputs, ok := scope.Steps[ref.Segments[1]]
		if !ok {
			return fmt.Errorf("unknown step %q in %s", ref.Segments[1], ref.Raw)
		}
		if _, ok := outputs[ref.Segments[3]]; !ok {
			return fmt.Errorf("unknown output %q in %s", ref.Segments[3], ref.Raw)
		}
	}
	return nil
}

func validateMapReference(ref Reference, namespace string, values Values) error {
	if len(ref.Segments) != 2 {
		return validateBindingSegments(ref.Segments)
	}
	if _, ok := values[ref.Segments[1]]; !ok {
		return fmt.Errorf("unknown %s binding %s", namespace, ref.Raw)
	}
	return nil
}

// ExpandString expands a value without a caller context.
func ExpandString(raw string, runtimeScope Scope, mode Mode, field string) (string, error) {
	return ExpandStringContext(context.Background(), raw, runtimeScope, mode, field)
}

// ExpandStringContext expands Dagu-owned references, then applies the legacy
// compatibility evaluator for non-reserved references.
func ExpandStringContext(ctx context.Context, raw string, runtimeScope Scope, mode Mode, field string, opts ...Option) (string, error) {
	if raw == "" {
		return "", nil
	}
	if err := ValidateReferences(raw, runtimeScope, mode, field); err != nil {
		return "", err
	}
	resolved, err := ParseTemplate(raw).Resolve(runtimeScope)
	if err != nil {
		if field != "" {
			return "", fmt.Errorf("%s: %w", field, err)
		}
		return "", err
	}

	options := append(scopeOptions(runtimeScope, mode), opts...)
	options = append(options, modeOptions(mode)...)
	return String(ctx, resolved, options...)
}

// ExpandObject recursively expands all string fields in an object without a caller context.
func ExpandObject[T any](obj T, runtimeScope Scope, mode Mode, field string) (T, error) {
	return ExpandObjectContext(context.Background(), obj, runtimeScope, mode, field)
}

// ExpandObjectContext recursively expands all string fields in an object.
func ExpandObjectContext[T any](ctx context.Context, obj T, runtimeScope Scope, mode Mode, field string, opts ...Option) (T, error) {
	v := reflect.ValueOf(obj)
	options := append(scopeOptions(runtimeScope, mode), opts...)
	options = append(options, modeOptions(mode)...)
	transform := func(ctx context.Context, s string) (string, error) {
		return ExpandStringContext(ctx, s, runtimeScope, mode, field, options...)
	}
	result, err := walkValue(ctx, v, transform)
	if err != nil {
		return obj, err
	}
	val, ok := result.Interface().(T)
	if !ok {
		return obj, fmt.Errorf("type assertion failed: expected %T, got %T", obj, result.Interface())
	}
	return val, nil
}

func scopeOptions(scope Scope, mode Mode) []Option {
	opts := make([]Option, 0, 2)
	if mode != ModeShellCommand {
		vars := valuesToStrings(scope.Env)
		if len(vars) > 0 {
			opts = append(opts, WithVariables(vars))
		}
	}
	if len(scope.StepMap) > 0 {
		opts = append(opts, WithStepMap(scope.StepMap))
	}
	return opts
}

func modeOptions(mode Mode) []Option {
	switch mode {
	case ModeConstLoad, ModeStaticValidation, ModeWorkflowValue, ModeShellCommand:
		return []Option{WithoutSubstitute()}
	case ModeDirectCommand:
		return []Option{WithoutSubstitute(), WithOSExpansion()}
	case ModeDynamicEval:
		return []Option{}
	default:
		return []Option{WithoutSubstitute()}
	}
}

func valuesToStrings(values Values) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = formatBindingValue(value)
	}
	return out
}
