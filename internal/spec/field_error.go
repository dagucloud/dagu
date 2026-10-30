// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"errors"
	"fmt"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// FieldError is a DAG error about one field of the YAML document.
type FieldError struct {
	// Path locates the field in the document, such as schedule[1].calendar.
	Path string
	Err  error
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("field '%s': %v", e.Path, e.Err)
}

func (e *FieldError) Unwrap() error {
	return e.Err
}

// ErrorMessages formats errors about the YAML document data. An error about
// a field found in data starts with the field's [line:column], the form
// editors place markers at.
func ErrorMessages(data []byte, errs []error) []string {
	messages := make([]string, 0, len(errs))
	var file *ast.File
	for _, err := range errs {
		message := err.Error()
		var fieldErr *FieldError
		if errors.As(err, &fieldErr) {
			if file == nil {
				file, _ = parser.ParseBytes(data, 0)
			}
			if line, column, ok := fieldPosition(file, fieldErr.Path); ok {
				message = fmt.Sprintf("[%d:%d] %s", line, column, message)
			}
		}
		messages = append(messages, message)
	}
	return messages
}

func fieldPosition(file *ast.File, path string) (line, column int, ok bool) {
	if file == nil {
		return 0, 0, false
	}
	yamlPath, err := yaml.PathString("$." + path)
	if err != nil {
		return 0, 0, false
	}
	node, err := yamlPath.FilterFile(file)
	if err != nil || node == nil || node.GetToken() == nil {
		return 0, 0, false
	}
	position := node.GetToken().Position
	return position.Line, position.Column, true
}
