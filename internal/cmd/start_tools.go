// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
	dagutools "github.com/dagucloud/dagu/internal/tools"
	daguaqua "github.com/dagucloud/dagu/internal/tools/aqua"
)

func prepareDAGTools(ctx *Context, dag *core.DAG) ([]string, error) {
	if dag == nil || dag.Tools == nil {
		return nil, nil
	}
	if err := validateDAGToolsSupported(dag); err != nil {
		return nil, err
	}
	manifest, err := daguaqua.New().Install(ctx.Context, dag.Tools, dagutools.InstallOptions{
		DataDir: ctx.Config.Paths.DataDir,
		WorkDir: dag.WorkingDir,
	})
	if err != nil {
		return nil, fmt.Errorf("prepare DAG tools: %w", err)
	}
	return dagutools.EnvVars(manifest, dagToolsBasePath(ctx)), nil
}

func dagToolsBasePath(ctx *Context) string {
	if ctx != nil && ctx.Config != nil {
		for _, env := range ctx.Config.Core.BaseEnv.AsSlice() {
			key, value, ok := strings.Cut(env, "=")
			if ok && strings.EqualFold(key, "PATH") {
				return value
			}
		}
	}
	return os.Getenv("PATH")
}

func validateDAGToolsSupported(dag *core.DAG) error {
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
