// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
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
	if supportedStrictBinding(segments) {
		ref.Kind = referenceStrict
		if stepOutput, ok := parseStepOutputReference(ref); ok {
			ref.StepOutput = &stepOutput
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
	if !ref.Braced {
		return StepOutputReference{}, false
	}

	var stepName string
	var path []string
	switch {
	case len(ref.Segments) >= 4 && ref.Segments[0] == "steps" && ref.Segments[2] == "outputs":
		stepName = ref.Segments[1]
		path = ref.Segments[3:]
	case len(ref.Segments) >= 3 && ref.Segments[1] == "output":
		stepName = ref.Segments[0]
		path = ref.Segments[2:]
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

// validateReferences keeps value-reference misses non-fatal.
func validateReferences(string, StaticScope, mode, string) error {
	return nil
}

func referenceWarnings(raw string, staticScope StaticScope, runtimeScope RuntimeScope, mode mode, field string) []string {
	refs := scanReferences(raw)
	if len(refs) == 0 {
		return nil
	}

	var warnings []string
	seen := make(map[string]struct{})
	for _, ref := range refs {
		if ref.Kind != referenceStrict {
			continue
		}
		err := referenceMiss(ref, staticScope, runtimeScope, mode)
		if err == nil {
			continue
		}
		key := field + "\x00" + ref.Raw
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		warnings = append(warnings, referenceWarning(field, ref.Raw, err))
	}
	return warnings
}

func referenceMiss(ref reference, staticScope StaticScope, runtimeScope RuntimeScope, mode mode) error {
	if mode == modeConstLoad && runtimeReferenceAvailableAfterConstLoad(ref) {
		return fmt.Errorf("%s is not available while loading consts", ref.Raw)
	}

	switch ref.Namespace {
	case "consts":
		return mapReferenceMiss(ref, "consts", staticScope.Consts)
	case "params":
		return paramReferenceMiss(ref, staticScope.Params, runtimeScope.Params)
	case "env":
		if runtimeScope.Env == nil {
			return nil
		}
		if _, ok := runtimeScope.Env.Get(ref.Segments[1]); ok {
			return nil
		}
		return fmt.Errorf("unknown env.%s binding", ref.Segments[1])
	case "steps":
		if runtimeScope.Steps == nil {
			return nil
		}
		_, err := bindingStepOutputValue(context.Background(), ref.Segments, runtimeScope.Steps, true)
		return err
	default:
		return nil
	}
}

func mapReferenceMiss(ref reference, namespace string, values Values) error {
	if _, ok := values[ref.Segments[1]]; ok {
		return nil
	}
	return fmt.Errorf("unknown %s binding %s", namespace, ref.Raw)
}

func paramReferenceMiss(ref reference, declarations, values Values) error {
	name := ref.Segments[1]
	if _, ok := declarations[name]; !ok {
		return fmt.Errorf("unknown params.%s binding", name)
	}
	if value, ok := values[name]; ok && value != nil {
		return nil
	}
	return fmt.Errorf("params.%s has no runtime value", name)
}

func referenceWarning(field, token string, err error) string {
	if field == "" {
		return fmt.Sprintf("value reference %s could not be resolved; preserving literal text: %v", token, err)
	}
	return fmt.Sprintf("%s: value reference %s could not be resolved; preserving literal text: %v", field, token, err)
}

func runtimeReferenceAvailableAfterConstLoad(ref reference) bool {
	if !ref.Braced {
		return false
	}
	switch ref.Namespace {
	case "params":
		return supportedStrictBinding(ref.Segments)
	case "env":
		return len(ref.Segments) == 2 && bindingNamePattern.MatchString(ref.Segments[0]) && bindingNamePattern.MatchString(ref.Segments[1])
	case "steps":
		if len(ref.Segments) < 4 || ref.Segments[2] != "outputs" {
			return false
		}
		if !validStepOutputStepName(ref.Segments[1]) {
			return false
		}
		for _, segment := range ref.Segments[3:] {
			if !validOutputPathSegment(segment) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
