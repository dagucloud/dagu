// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"

	"github.com/dagucloud/dagu/v2/internal/dagsettings"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// errInvalidWorkspaceLabel marks a DAG whose workspace label is malformed.
var errInvalidWorkspaceLabel = errors.New("invalid workspace label")

type dagProfileResolver struct {
	settingsStore dagsettings.Store
	profileStore  profile.Store
	// projectsEnabled keeps a workspace from choosing a DAG's default
	// profile.
	projectsEnabled bool
}

// NewDAGProfileResolver returns a resolver of the profile a DAG's runs use
// by default. While projects are on, the workspace a DAG names chooses none.
func NewDAGProfileResolver(settingsStore dagsettings.Store, profileStore profile.Store, projectsEnabled bool) DAGProfileResolver {
	return &dagProfileResolver{
		settingsStore:   settingsStore,
		profileStore:    profileStore,
		projectsEnabled: projectsEnabled,
	}
}

func (r *dagProfileResolver) ResolveProfile(ctx context.Context, dagName string, workspaceName string) (string, error) {
	if r == nil {
		return "", nil
	}
	if r.projectsEnabled {
		workspaceName = ""
	}
	return dagsettings.ResolveProfile(ctx, r.settingsStore, r.profileStore, dagName, workspaceName)
}

func dagWorkspaceName(dag *ir.DAG) (string, error) {
	if dag == nil {
		return "", nil
	}
	workspaceName, state := workspace.WorkspaceLabelFromLabels(dag.Labels)
	switch state {
	case workspace.WorkspaceLabelValid:
		return workspaceName, nil
	case workspace.WorkspaceLabelMissing:
		return "", nil
	case workspace.WorkspaceLabelInvalid:
		return "", errInvalidWorkspaceLabel
	}
	return "", nil
}
