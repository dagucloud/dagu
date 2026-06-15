// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

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
	ReferenceStrict  ReferenceKind = "strict"
	ReferenceEval    ReferenceKind = "eval"
	ReferenceInvalid ReferenceKind = "invalid"
)

// Reference describes one scanned placeholder.
type Reference struct {
	Raw        string
	Expr       string
	Namespace  string
	Segments   []string
	Kind       ReferenceKind
	Braced     bool
	Start      int
	End        int
	Err        error
	StepOutput *StepOutputReference
}

// StepOutputReference describes a step output reference in eval syntax.
type StepOutputReference struct {
	Expression string
	StepName   string
	Path       []string
}

// ScanReferences classifies strict references and eval refs.
func ScanReferences(raw string) []Reference {
	if raw == "" {
		return nil
	}

	refs := make([]Reference, 0)
	for _, loc := range bindingRefPattern.FindAllStringSubmatchIndex(raw, -1) {
		expr := strings.TrimSpace(raw[loc[2]:loc[3]])
		refs = append(refs, classifyBracedReference(raw[loc[0]:loc[1]], expr, loc[0], loc[1]))
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
			Kind:      ReferenceEval,
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

func classifyBracedReference(rawRef, expr string, start, end int) Reference {
	segments := strings.Split(expr, ".")
	ref := Reference{
		Raw:       rawRef,
		Expr:      expr,
		Namespace: segments[0],
		Segments:  segments,
		Braced:    true,
		Start:     start,
		End:       end,
	}
	if reservedBinding(ref.Namespace) {
		ref.Kind = ReferenceStrict
		if err := validateBindingSegments(segments); err != nil {
			ref.Kind = ReferenceInvalid
			ref.Err = err
		}
		return ref
	}
	if strings.Contains(expr, ".") {
		ref.Kind = ReferenceEval
		if stepOutput, ok := parseStepOutputReference(ref); ok {
			ref.StepOutput = &stepOutput
		}
	}
	return ref
}

func parseStepOutputReference(ref Reference) (StepOutputReference, bool) {
	if !ref.Braced || ref.Kind != ReferenceEval || len(ref.Segments) < 3 || ref.Segments[1] != "output" {
		return StepOutputReference{}, false
	}
	if !validStepOutputStepName(ref.Segments[0]) {
		return StepOutputReference{}, false
	}
	path := ref.Segments[2:]
	for _, segment := range path {
		if !validOutputPathSegment(segment) {
			return StepOutputReference{}, false
		}
	}
	return StepOutputReference{
		Expression: ref.Raw,
		StepName:   ref.Segments[0],
		Path:       append([]string(nil), path...),
	}, true
}

func validStepOutputStepName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func validOutputPathSegment(segment string) bool {
	if segment == "" {
		return false
	}
	for i, r := range segment {
		if i == 0 {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' {
				continue
			}
			return false
		}
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

// ValidateReferences validates strict references against a static scope.
func ValidateReferences(raw string, staticScope StaticScope, mode Mode, field string) error {
	refs := ScanReferences(raw)
	var errs []string
	for _, ref := range refs {
		switch ref.Kind {
		case ReferenceInvalid:
			errs = append(errs, ref.Err.Error())
		case ReferenceStrict:
			if err := validateStrictReference(ref, staticScope, mode); err != nil {
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

func validateStrictReference(ref Reference, scope StaticScope, mode Mode) error {
	if mode == ModeConstLoad && ref.Namespace != "consts" {
		return fmt.Errorf("%s is not available while loading consts", ref.Raw)
	}
	switch ref.Namespace {
	case "consts":
		return validateMapReference(ref, "consts", scope.Consts)
	case "params":
		return validateMapReference(ref, "params", scope.Params)
	case "env":
		return validateEnvReference(ref)
	case "steps":
		return validateStepReference(ref, scope)
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

func validateEnvReference(ref Reference) error {
	if len(ref.Segments) != 2 {
		return validateBindingSegments(ref.Segments)
	}
	return nil
}

func validateStepReference(ref Reference, scope StaticScope) error {
	if err := validateBindingSegments(ref.Segments); err != nil {
		return err
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
	return nil
}

// ExpandString expands a value without a caller context.
func ExpandString(raw string, runtimeScope RuntimeScope, mode Mode, field string) (string, error) {
	return ExpandStringContext(context.Background(), raw, runtimeScope, mode, field)
}

// ExpandStringContext expands strict references, then applies eval features for non-reserved references.
func ExpandStringContext(ctx context.Context, raw string, runtimeScope RuntimeScope, mode Mode, field string, opts ...Option) (string, error) {
	if raw == "" {
		return "", nil
	}
	resolved, err := resolveBindings(raw, runtimeScope)
	if err != nil {
		if field != "" {
			return "", fmt.Errorf("%s: %w", field, err)
		}
		return "", err
	}

	options := append(fieldPolicyOptions(runtimeScope, mode), opts...)
	return String(ctx, resolved, options...)
}

// ExpandObject recursively expands all string fields in an object without a caller context.
func ExpandObject[T any](obj T, runtimeScope RuntimeScope, mode Mode, field string) (T, error) {
	return ExpandObjectContext(context.Background(), obj, runtimeScope, mode, field)
}

// ExpandObjectContext recursively expands all string fields in an object.
func ExpandObjectContext[T any](ctx context.Context, obj T, runtimeScope RuntimeScope, mode Mode, field string, opts ...Option) (T, error) {
	v := reflect.ValueOf(obj)
	transform := func(ctx context.Context, s string) (string, error) {
		return ExpandStringContext(ctx, s, runtimeScope, mode, field, opts...)
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

type fieldPolicy struct {
	includeScopedEnv bool
	options          []Option
}

var fieldPolicies = map[Mode]fieldPolicy{
	ModeConstLoad: {
		includeScopedEnv: true,
		options:          []Option{WithoutSubstitute()},
	},
	ModeStaticValidation: {
		includeScopedEnv: true,
		options:          []Option{WithoutSubstitute()},
	},
	ModeWorkflowValue: {
		includeScopedEnv: true,
		options:          []Option{WithoutSubstitute()},
	},
	ModeShellCommand: {
		options: []Option{WithoutSubstitute()},
	},
	ModeDirectCommand: {
		includeScopedEnv: true,
		options:          []Option{WithoutSubstitute(), WithOSExpansion()},
	},
	ModeDynamicEval: {
		includeScopedEnv: true,
	},
}

var defaultFieldPolicy = fieldPolicy{
	includeScopedEnv: true,
	options:          []Option{WithoutSubstitute()},
}

func fieldPolicyOptions(scope RuntimeScope, mode Mode) []Option {
	policy, ok := fieldPolicies[mode]
	if !ok {
		policy = defaultFieldPolicy
	}

	opts := make([]Option, 0, len(policy.options)+2)
	if policy.includeScopedEnv {
		vars := valuesToStrings(scope.Env)
		if len(vars) > 0 {
			opts = append(opts, WithVariables(vars))
		}
	}
	if len(scope.StepMap) > 0 {
		opts = append(opts, WithStepMap(scope.StepMap))
	}
	opts = append(opts, policy.options...)
	return opts
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
