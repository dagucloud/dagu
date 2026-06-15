// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"fmt"
	"sort"
	"strings"
)

// mode identifies the workflow phase that owns a value.
type mode int

const (
	modeConstLoad mode = iota
	modeStaticValidation
	modeWorkflowValue
	modeShellCommand
	modeDirectCommand
	modeDynamicEval
)

// referenceKind classifies a placeholder found in a value string.
type referenceKind string

const (
	referenceStrict  referenceKind = "strict"
	referenceEval    referenceKind = "eval"
	referenceInvalid referenceKind = "invalid"
)

type reference struct {
	Raw        string
	Expr       string
	Namespace  string
	Segments   []string
	Kind       referenceKind
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

// scanReferences classifies strict references and eval refs.
func scanReferences(raw string) []reference {
	if raw == "" {
		return nil
	}

	refs := make([]reference, 0)
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
		ref := reference{
			Raw:       rawRef,
			Expr:      expr,
			Namespace: namespace,
			Segments:  strings.Split(expr, "."),
			Kind:      referenceEval,
			Start:     loc[0],
			End:       loc[1],
		}
		if reservedBinding(namespace) {
			ref.Kind = referenceInvalid
			ref.Err = fmt.Errorf("invalid binding shorthand %s; use ${%s}", rawRef, expr)
		}
		refs = append(refs, ref)
	}

	sort.SliceStable(refs, func(i, j int) bool {
		return refs[i].Start < refs[j].Start
	})
	return refs
}

func classifyBracedReference(rawRef, expr string, start, end int) reference {
	segments := strings.Split(expr, ".")
	ref := reference{
		Raw:       rawRef,
		Expr:      expr,
		Namespace: segments[0],
		Segments:  segments,
		Braced:    true,
		Start:     start,
		End:       end,
	}
	if reservedBinding(ref.Namespace) {
		ref.Kind = referenceStrict
		if err := validateBindingSegments(segments); err != nil {
			ref.Kind = referenceInvalid
			ref.Err = err
		}
		return ref
	}
	if strings.Contains(expr, ".") {
		ref.Kind = referenceEval
		if stepOutput, ok := parseStepOutputReference(ref); ok {
			ref.StepOutput = &stepOutput
		}
	}
	return ref
}

func parseStepOutputReference(ref reference) (StepOutputReference, bool) {
	if !ref.Braced || ref.Kind != referenceEval {
		return StepOutputReference{}, false
	}

	var stepName string
	var path []string
	switch {
	case len(ref.Segments) >= 3 && ref.Segments[1] == "output":
		stepName = ref.Segments[0]
		path = ref.Segments[2:]
	case len(ref.Segments) >= 4 && ref.Segments[0] == "steps" && ref.Segments[2] == "outputs":
		stepName = ref.Segments[1]
		path = ref.Segments[3:]
	default:
		return StepOutputReference{}, false
	}

	if !validStepOutputStepName(stepName) {
		return StepOutputReference{}, false
	}
	for _, segment := range path {
		if !validOutputPathSegment(segment) {
			return StepOutputReference{}, false
		}
	}
	return StepOutputReference{
		Expression: ref.Raw,
		StepName:   stepName,
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

// validateReferences validates strict references against a static scope.
func validateReferences(raw string, staticScope StaticScope, mode mode, field string) error {
	refs := scanReferences(raw)
	var errs []string
	for _, ref := range refs {
		if mode == modeConstLoad && ref.Braced && runtimeBindingNamespace(ref.Namespace) {
			errs = append(errs, fmt.Sprintf("%s is not available while loading consts", ref.Raw))
			continue
		}
		//nolint:exhaustive // Eval references are intentionally allowed and need no validation here.
		switch ref.Kind {
		case referenceInvalid:
			errs = append(errs, ref.Err.Error())
		case referenceStrict:
			if err := validateStrictReference(ref, staticScope); err != nil {
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

func runtimeBindingNamespace(namespace string) bool {
	switch namespace {
	case "params", "env", "steps":
		return true
	default:
		return false
	}
}

func validateStrictReference(ref reference, scope StaticScope) error {
	if ref.Namespace == "consts" {
		return validateMapReference(ref, "consts", scope.Consts)
	}
	return nil
}

func validateMapReference(ref reference, namespace string, values Values) error {
	if len(ref.Segments) != 2 {
		return validateBindingSegments(ref.Segments)
	}
	if _, ok := values[ref.Segments[1]]; !ok {
		return fmt.Errorf("unknown %s binding %s", namespace, ref.Raw)
	}
	return nil
}
