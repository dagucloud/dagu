// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNotFound          = errors.New("controller not found")
	ErrAlreadyExists     = errors.New("controller already exists")
	ErrInvalidDefinition = errors.New("invalid controller definition")
	ErrDefinitionCorrupt = errors.New("controller definition is corrupt")
	ErrRuntimeCorrupt    = errors.New("controller runtime is corrupt")
	ErrInvalidPrompt     = errors.New("invalid controller prompt")
	ErrActiveController  = errors.New("controller is active")
	ErrInvalidLifecycle  = errors.New("invalid controller lifecycle operation")
	ErrSnapshotTooLarge  = errors.New("controller runtime snapshot exceeds size limit")
)

// ValidationIssue identifies one invalid field in a Controller definition.
type ValidationIssue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ValidationError aggregates strict definition validation failures.
type ValidationError struct {
	Issues []ValidationIssue
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return ErrInvalidDefinition.Error()
	}
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		if issue.Path == "" {
			parts = append(parts, issue.Message)
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", issue.Path, issue.Message))
	}
	return strings.Join(parts, "; ")
}

func (e *ValidationError) Unwrap() error {
	return ErrInvalidDefinition
}
