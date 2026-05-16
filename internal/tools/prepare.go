// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/internal/core"
)

// PrepareDAG validates, installs, and exposes the tools declared by a DAG.
func PrepareDAG(ctx context.Context, dag *core.DAG, installer Installer, opts InstallOptions, basePath string) ([]string, error) {
	if dag == nil || dag.Tools == nil {
		return ToolDirEnvVars(opts), nil
	}
	if installer == nil {
		return nil, fmt.Errorf("tools installer is required")
	}
	if err := ValidateDAGSupported(dag); err != nil {
		return nil, err
	}
	manifest, err := installer.Install(ctx, dag.Tools, opts)
	if err != nil {
		return nil, fmt.Errorf("prepare DAG tools: %w", err)
	}
	return EnvVars(manifest, basePath), nil
}

// ValidateDAGSupported returns an error when DAG tools are declared for an
// execution mode that cannot receive the host-local tool environment.
func ValidateDAGSupported(dag *core.DAG) error {
	if dag == nil || dag.Tools == nil {
		return nil
	}
	if dag.Container != nil {
		return fmt.Errorf("tools are not supported with DAG-level container yet")
	}
	for _, step := range dag.Steps {
		if step.Container != nil {
			return fmt.Errorf("tools are not supported with step container %q yet", step.Name)
		}
		switch step.ExecutorConfig.Type {
		case "docker", "ssh", "k8s", "kubernetes":
			return fmt.Errorf("tools are not supported with executor %q on step %q yet", step.ExecutorConfig.Type, step.Name)
		}
	}
	return nil
}
