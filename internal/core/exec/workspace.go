// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"slices"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

const workspaceLabelKey = "workspace"

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

// WorkspaceNameFromLabels returns a valid single workspace label value.
//
// Missing, invalid, or conflicting workspace labels return ok=false. Use
// WorkspaceLabelFromLabels when callers must distinguish missing from malformed
// labels.
func WorkspaceNameFromLabels(labels core.Labels) (string, bool) {
	workspaceName, state := WorkspaceLabelFromLabels(labels)
	return workspaceName, state == WorkspaceLabelValid
}

func WorkspaceLabelFromLabels(labels core.Labels) (string, WorkspaceLabelState) {
	var workspaceName string
	for _, value := range labels.Get(workspaceLabelKey) {
		if value == "" {
			return "", WorkspaceLabelInvalid
		}
		if err := workspace.ValidateName(value); err != nil {
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

// WorkspaceFilter restricts list/search results to allowed workspaces.
type WorkspaceFilter struct {
	Enabled           bool
	Workspaces        []string
	IncludeUnlabelled bool
}

// MatchesLabels reports whether labels are visible under the filter.
func (f *WorkspaceFilter) MatchesLabels(labels core.Labels) bool {
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
