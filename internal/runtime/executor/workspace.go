// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
)

type workspaceSeedKey struct{}

// WithWorkspaceSeed carries an immutable workspace through inline child workflows.
func WithWorkspaceSeed(ctx context.Context, seed WorkspaceSeed) context.Context {
	return context.WithValue(ctx, workspaceSeedKey{}, seed)
}

func workspaceSeedFromContext(ctx context.Context) (WorkspaceSeed, bool) {
	seed, ok := ctx.Value(workspaceSeedKey{}).(WorkspaceSeed)
	return seed, ok
}

// PrepareDAGWorkspace snapshots the files declared by a DAG for distributed execution.
func PrepareDAGWorkspace(dag *ir.DAG) (*WorkspaceSeed, error) {
	includes := dagFileDependencies(dag)
	if len(includes) == 0 {
		return nil, nil
	}
	if dag == nil || strings.TrimSpace(dag.SourceFile) == "" {
		return nil, fmt.Errorf("DAG file dependencies require a source file")
	}
	if dag.YamlData == nil {
		return nil, fmt.Errorf("DAG file dependencies require the dispatched definition")
	}

	sourceFile, err := filepath.Abs(dag.SourceFile)
	if err != nil {
		return nil, fmt.Errorf("resolve DAG source file %q: %w", dag.SourceFile, err)
	}
	descriptor, archive, err := workspacebundle.PackDirectory(filepath.Dir(sourceFile), workspacebundle.PackOptions{
		DAGPath:  filepath.Base(sourceFile),
		DAGData:  dag.YamlData,
		Includes: includes,
	})
	if err != nil {
		return nil, fmt.Errorf("prepare DAG file dependencies: %w", err)
	}
	return &WorkspaceSeed{Descriptor: *descriptor, Archive: archive}, nil
}

func dagFileDependencies(dag *ir.DAG) []string {
	if dag == nil {
		return nil
	}
	var dependencies []string
	var collect func(*ir.Step)
	collect = func(step *ir.Step) {
		if step == nil {
			return
		}
		dependencies = append(dependencies, step.Dependencies...)
		if step.Foreach != nil {
			for i := range step.Foreach.Steps {
				collect(&step.Foreach.Steps[i])
			}
		}
	}
	for i := range dag.Steps {
		collect(&dag.Steps[i])
	}
	for _, handler := range []*ir.Step{
		dag.HandlerOn.Init,
		dag.HandlerOn.Failure,
		dag.HandlerOn.Success,
		dag.HandlerOn.Abort,
		dag.HandlerOn.Exit,
		dag.HandlerOn.Wait,
	} {
		collect(handler)
	}
	for _, localDAG := range dag.LocalDAGs {
		dependencies = append(dependencies, dagFileDependencies(localDAG)...)
	}
	return dependencies
}
