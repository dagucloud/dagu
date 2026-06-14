// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	valueResolutionBracedRefPattern   = regexp.MustCompile(`\$\{([^}]+)\}`)
	valueResolutionShorthandPattern   = regexp.MustCompile(`\$(consts|params|steps)(\.[A-Za-z][A-Za-z0-9_]*)+`)
	valueResolutionIdentifierPattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	valueResolutionSupportedNamespace = map[string]struct{}{
		"consts": {},
		"params": {},
		"steps":  {},
	}
)

func validateValueResolutionReferences(dag *DAG) ErrorList {
	var errs ErrorList
	if dag == nil {
		return errs
	}

	for _, step := range dag.Steps {
		for _, ref := range valueResolutionRunFields(step) {
			errs = append(errs, validateValueResolutionString(dag, ref.field, ref.value)...)
		}
	}
	return errs
}

type valueResolutionRunField struct {
	field string
	value string
}

func valueResolutionRunFields(step Step) []valueResolutionRunField {
	fields := make([]valueResolutionRunField, 0, len(step.Commands)+2)
	if step.CmdWithArgs != "" {
		fields = append(fields, valueResolutionRunField{field: "run", value: step.CmdWithArgs})
	}
	if step.Script != "" {
		fields = append(fields, valueResolutionRunField{field: "run", value: step.Script})
	}
	for idx, cmd := range step.Commands {
		if cmd.CmdWithArgs != "" {
			fields = append(fields, valueResolutionRunField{
				field: fmt.Sprintf("run[%d]", idx),
				value: cmd.CmdWithArgs,
			})
		}
	}
	return fields
}

func validateValueResolutionString(dag *DAG, field string, value string) ErrorList {
	var errs ErrorList
	strictUnqualified := usesValueResolutionSpec(dag, value)

	for _, match := range valueResolutionShorthandPattern.FindAllString(value, -1) {
		errs = append(errs, NewValidationError(field, match,
			fmt.Errorf("%s is invalid Dagu-looking reference syntax; use ${...}", match)))
	}

	matches := valueResolutionBracedRefPattern.FindAllStringSubmatch(value, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		ref := strings.TrimSpace(match[1])
		segments := strings.Split(ref, ".")
		if len(segments) == 1 {
			if strictUnqualified {
				errs = append(errs, NewValidationError(field, match[0],
					fmt.Errorf("reference %s is unqualified; use params.%s, consts.%s, or steps.<step_id>.outputs.<name>",
						match[0], ref, ref)))
			}
			continue
		}

		namespace := segments[0]
		if _, ok := valueResolutionSupportedNamespace[namespace]; !ok {
			continue
		}

		if err := validateValueResolutionPath(namespace, segments); err != nil {
			errs = append(errs, NewValidationError(field, match[0], err))
			continue
		}

		if namespace == "consts" {
			if _, ok := dag.Consts[segments[1]]; !ok {
				errs = append(errs, NewValidationError(field, match[0],
					fmt.Errorf("unknown consts reference %s", match[0])))
			}
		}
	}

	return errs
}

func usesValueResolutionSpec(dag *DAG, value string) bool {
	if dag != nil && len(dag.Consts) > 0 {
		return true
	}
	if valueResolutionShorthandPattern.MatchString(value) {
		return true
	}
	for _, match := range valueResolutionBracedRefPattern.FindAllStringSubmatch(value, -1) {
		if len(match) < 2 {
			continue
		}
		namespace, _, ok := strings.Cut(strings.TrimSpace(match[1]), ".")
		if !ok {
			continue
		}
		if _, supported := valueResolutionSupportedNamespace[namespace]; supported {
			return true
		}
	}
	return false
}

func validateValueResolutionPath(namespace string, segments []string) error {
	for _, segment := range segments {
		if !valueResolutionIdentifierPattern.MatchString(segment) {
			return fmt.Errorf("reference path segment %q must match %s", segment, valueResolutionIdentifierPattern.String())
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
