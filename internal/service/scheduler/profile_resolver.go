// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/dagsettings"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/profile"
)

type dagProfileResolver struct {
	settingsStore dagsettings.Store
	profileStore  profile.Store
}

func NewDAGProfileResolver(settingsStore dagsettings.Store, profileStore profile.Store) DAGProfileResolver {
	return &dagProfileResolver{
		settingsStore: settingsStore,
		profileStore:  profileStore,
	}
}

func (r *dagProfileResolver) ResolveProfile(ctx context.Context, dagName string, workspaceName string) (string, error) {
	if r == nil {
		return "", nil
	}
	return dagsettings.ResolveProfile(ctx, r.settingsStore, r.profileStore, dagName, workspaceName)
}

func dagWorkspaceName(dag *ir.DAG) (string, error) {
	if dag == nil {
		return "", nil
	}
	workspaceName, state := exec.WorkspaceLabelFromLabels(dag.Labels)
	switch state {
	case exec.WorkspaceLabelValid:
		return workspaceName, nil
	case exec.WorkspaceLabelMissing:
		return "", nil
	case exec.WorkspaceLabelInvalid:
		return "", fmt.Errorf("invalid workspace label")
	}
	return "", nil
}
