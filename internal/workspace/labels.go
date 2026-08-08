// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workspace

import (
	"slices"
)

const workspaceLabelKey = "workspace"

// Labels provides label values by key.
type Labels interface {
	Get(key string) []string
}

// WorkspaceLabelState describes whether labels contain a usable workspace.
type WorkspaceLabelState int

const (
	// WorkspaceLabelMissing means no workspace label is present.
	WorkspaceLabelMissing WorkspaceLabelState = iota
	// WorkspaceLabelValid means exactly one valid workspace label value is present.
	WorkspaceLabelValid
	// WorkspaceLabelInvalid means a workspace label is present but malformed or ambiguous.
	WorkspaceLabelInvalid
)

// WorkspaceNameFromLabels returns the valid workspace label, if one is present.
func WorkspaceNameFromLabels(labels Labels) (string, bool) {
	workspaceName, state := WorkspaceLabelFromLabels(labels)
	return workspaceName, state == WorkspaceLabelValid
}

// WorkspaceLabelFromLabels returns the workspace label and its validity state.
func WorkspaceLabelFromLabels(labels Labels) (string, WorkspaceLabelState) {
	var workspaceName string
	for _, value := range labels.Get(workspaceLabelKey) {
		if value == "" {
			return "", WorkspaceLabelInvalid
		}
		if err := ValidateName(value); err != nil {
			return value, WorkspaceLabelInvalid
		}
		if workspaceName != "" && workspaceName != value {
			return workspaceName, WorkspaceLabelInvalid
		}
		workspaceName = value
	}
	if workspaceName == "" {
		return "", WorkspaceLabelMissing
	}
	return workspaceName, WorkspaceLabelValid
}

// WorkspaceFilter restricts list and search results to allowed workspaces.
type WorkspaceFilter struct {
	Enabled           bool
	Workspaces        []string
	IncludeUnlabelled bool
}

// MatchesLabels reports whether labels are visible under the filter.
func (f *WorkspaceFilter) MatchesLabels(labels Labels) bool {
	if f == nil || !f.Enabled {
		return true
	}
	workspaceName, state := WorkspaceLabelFromLabels(labels)
	switch state {
	case WorkspaceLabelMissing:
		return f.IncludeUnlabelled
	case WorkspaceLabelInvalid:
		return false
	case WorkspaceLabelValid:
		return slices.Contains(f.Workspaces, workspaceName)
	default:
		return false
	}
}
