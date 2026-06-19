// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"sort"
	"strings"
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
		if isEscapedDollar(raw, loc[0]) {
			continue
		}
		expr := strings.TrimSpace(raw[loc[2]:loc[3]])
		refs = append(refs, classifyBracedReference(raw[loc[0]:loc[1]], expr, loc[0], loc[1]))
	}
	for _, loc := range referencePattern.FindAllStringSubmatchIndex(raw, -1) {
		if isEscapedDollar(raw, loc[0]) {
			continue
		}
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
	}
	return ref
}

func parseStepOutputReference(ref reference) (StepOutputReference, bool) {
	if !ref.Braced {
		return StepOutputReference{}, false
	}

	if len(ref.Segments) != 4 || ref.Segments[0] != "steps" || ref.Segments[2] != "outputs" {
		return StepOutputReference{}, false
	}

	stepName := ref.Segments[1]
	if !validStepOutputStepName(stepName) {
		return StepOutputReference{}, false
	}
	outputName := ref.Segments[3]
	if !validOutputPathSegment(outputName) {
		return StepOutputReference{}, false
	}
	return StepOutputReference{
		Expression: ref.Raw,
		StepName:   stepName,
		Path:       []string{outputName},
	}, true
}

// IsStepOutputReferenceToken reports whether token is an exact Spec 007 reference.
func IsStepOutputReferenceToken(token string) bool {
	_, ok := ParseStepOutputReferenceToken(token)
	return ok
}

// ParseStepOutputReferenceToken parses an exact Spec 007 reference token.
func ParseStepOutputReferenceToken(token string) (StepOutputReference, bool) {
	if !strings.HasPrefix(token, "${") || !strings.HasSuffix(token, "}") {
		return StepOutputReference{}, false
	}
	expr := strings.TrimSpace(token[2 : len(token)-1])
	return parseStepOutputReference(classifyBracedReference(token, expr, 0, len(token)))
}

func validStepOutputStepName(name string) bool {
	return bindingNamePattern.MatchString(name)
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
