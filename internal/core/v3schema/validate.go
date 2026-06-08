// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package v3schema

import (
	"errors"
	"fmt"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

var rootFields = map[string]struct{}{
	"consts":                {},
	"container":             {},
	"defaults":              {},
	"delay_sec":             {},
	"description":           {},
	"handler_on":            {},
	"kubernetes":            {},
	"llm":                   {},
	"max_active_steps":      {},
	"max_clean_up_time_sec": {},
	"max_output_size":       {},
	"name":                  {},
	"params":                {},
	"preconditions":         {},
	"retry_policy":          {},
	"ssh":                   {},
	"steps":                 {},
	"timeout_sec":           {},
	"tools":                 {},
	"working_dir":           {},
}

// ValidateWorkflow validates root-level v3 workflow YAML schema behavior.
func ValidateWorkflow(data []byte) error {
	file, err := parser.ParseBytes(data, 0)
	if err != nil {
		return fmt.Errorf("invalid yaml: %w", err)
	}
	if file == nil || len(file.Docs) == 0 {
		return errors.New("yaml stream must contain at least one DAG document")
	}

	var errs validationErrors
	names := map[string]struct{}{}
	for i, doc := range file.Docs {
		errs.add(validateDocument(i, doc, names)...)
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

func validateDocument(index int, doc *ast.DocumentNode, names map[string]struct{}) []error {
	var errs validationErrors
	label := documentLabel(index)

	root, err := documentRoot(label, doc)
	if err != nil {
		return []error{err}
	}

	fields, fieldErrs := collectRootFields(label, root)
	errs.add(fieldErrs...)
	errs.add(validateDocumentName(index, label, fields, names)...)
	errs.add(validateSteps(label, fields)...)
	return errs
}

func documentRoot(label string, doc *ast.DocumentNode) (*ast.MappingNode, error) {
	if doc == nil || doc.Body == nil {
		return nil, fmt.Errorf("%s must not be empty", label)
	}

	root, ok := doc.Body.(*ast.MappingNode)
	if !ok || root == nil {
		return nil, fmt.Errorf("%s root must be a mapping", label)
	}
	if len(root.Values) == 0 {
		return nil, fmt.Errorf("%s must not be empty", label)
	}

	return root, nil
}

func collectRootFields(label string, root *ast.MappingNode) (map[string]*ast.MappingValueNode, []error) {
	var errs validationErrors
	fields := map[string]*ast.MappingValueNode{}
	for _, item := range root.Values {
		key, ok := rootFieldName(item)
		if !ok {
			errs.add(fmt.Errorf("%s root field names must be strings", label))
			continue
		}
		if _, ok := rootFields[key]; !ok {
			errs.add(fmt.Errorf("%s contains unknown root field %q", label, key))
			continue
		}
		fields[key] = item
	}
	return fields, errs
}

func validateDocumentName(index int, label string, fields map[string]*ast.MappingValueNode, names map[string]struct{}) []error {
	var errs validationErrors
	if _, ok := fields["name"]; index == 0 && ok {
		errs.add(fmt.Errorf("%s must not define name", label))
	}
	if index > 0 {
		nameField, ok := fields["name"]
		if !ok {
			errs.add(fmt.Errorf("%s must define name", label))
		} else if name, ok := scalarString(nameField.Value); !ok || strings.TrimSpace(name) == "" {
			errs.add(fmt.Errorf("%s name must be a non-empty string", label))
		} else if _, exists := names[name]; exists {
			errs.add(fmt.Errorf("DAG document name %q must be unique", name))
		} else {
			names[name] = struct{}{}
		}
	}
	return errs
}

func validateSteps(label string, fields map[string]*ast.MappingValueNode) []error {
	var errs validationErrors
	stepsField, ok := fields["steps"]
	if !ok {
		errs.add(fmt.Errorf("%s must define steps", label))
		return errs
	}
	steps, ok := stepsField.Value.(*ast.SequenceNode)
	if !ok || steps == nil {
		errs.add(fmt.Errorf("%s steps must be a non-empty sequence", label))
		return errs
	}
	if len(steps.Values) == 0 {
		errs.add(fmt.Errorf("%s steps must be a non-empty sequence", label))
	}

	return errs
}

func documentLabel(index int) string {
	if index == 0 {
		return "entrypoint document"
	}
	return fmt.Sprintf("document %d", index+1)
}

func rootFieldName(item *ast.MappingValueNode) (string, bool) {
	if item == nil || item.Key == nil {
		return "", false
	}
	return scalarString(item.Key)
}

func scalarString(node ast.Node) (string, bool) {
	switch n := node.(type) {
	case *ast.StringNode:
		return n.Value, true
	default:
		return "", false
	}
}

type validationErrors []error

func (e *validationErrors) add(errs ...error) {
	for _, err := range errs {
		if err != nil {
			*e = append(*e, err)
		}
	}
}

func (e validationErrors) Error() string {
	var b strings.Builder
	for i, err := range e {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(err.Error())
	}
	return b.String()
}
