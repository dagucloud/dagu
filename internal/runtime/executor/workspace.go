// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/v2/internal/runtimeenv"
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

// PrepareDAGWorkspace snapshots the files declared by a DAG.
func PrepareDAGWorkspace(ctx context.Context, dag *ir.DAG) (*WorkspaceSeed, error) {
	root, opts, err := dagWorkspacePackOptions(ctx, dag)
	if err != nil || opts == nil {
		return nil, err
	}
	descriptor, archive, err := workspacebundle.PackDirectory(root, *opts)
	if err != nil {
		return nil, fmt.Errorf("prepare DAG file dependencies: %w", err)
	}
	return &WorkspaceSeed{Descriptor: *descriptor, Archive: archive}, nil
}

// PrepareDAGWorkspaceFile snapshots declared files into a staged archive under bundleDir.
func PrepareDAGWorkspaceFile(ctx context.Context, dag *ir.DAG, bundleDir string) (*workspacebundle.Descriptor, string, error) {
	root, opts, err := dagWorkspacePackOptions(ctx, dag)
	if err != nil || opts == nil {
		return nil, "", err
	}
	if strings.TrimSpace(bundleDir) == "" {
		return nil, "", fmt.Errorf("workspace bundle directory is not configured")
	}
	descriptor, archivePath, err := workspacebundle.PackDirectoryToFile(root, filepath.Join(bundleDir, "staging"), *opts)
	if err != nil {
		return nil, "", fmt.Errorf("prepare DAG file dependencies: %w", err)
	}
	return descriptor, archivePath, nil
}

func dagWorkspacePackOptions(ctx context.Context, dag *ir.DAG) (string, *workspacebundle.PackOptions, error) {
	includes := dagFileDependencies(dag)
	if len(includes) == 0 {
		return "", nil, nil
	}
	if dag.YamlData == nil {
		return "", nil, fmt.Errorf("DAG file dependencies require the dispatched definition")
	}

	var sourceFile string
	if strings.TrimSpace(dag.SourceFile) != "" {
		var err error
		sourceFile, err = filepath.Abs(dag.SourceFile)
		if err != nil {
			return "", nil, fmt.Errorf("resolve DAG source file %q: %w", dag.SourceFile, err)
		}
	}
	root := dag.WorkingDir
	if strings.TrimSpace(root) == "" {
		if sourceFile == "" {
			return "", nil, fmt.Errorf("DAG file dependencies require a source file or working directory")
		}
		root = filepath.Dir(sourceFile)
	} else {
		var err error
		root, err = runtimeenv.ResolveWorkingDir(ctx, dag)
		if err != nil {
			return "", nil, fmt.Errorf("resolve DAG working directory %q: %w", dag.WorkingDir, err)
		}
	}
	dagPath, err := workspaceDAGPath(root, sourceFile)
	if err != nil {
		return "", nil, err
	}
	return root, &workspacebundle.PackOptions{
		DAGPath:  dagPath,
		DAGData:  dag.YamlData,
		Includes: includes,
	}, nil
}

func workspaceDAGPath(root, sourceFile string) (string, error) {
	if sourceFile != "" && workspacebundle.IsPathWithin(root, sourceFile) {
		rel, err := filepath.Rel(root, sourceFile)
		if err != nil {
			return "", fmt.Errorf("resolve workspace DAG path: %w", err)
		}
		normalized, err := workspacebundle.NormalizeRelativePath(rel)
		if err != nil {
			return "", fmt.Errorf("normalize workspace DAG path: %w", err)
		}
		return normalized, nil
	}

	for suffix := 0; ; suffix++ {
		name := ".dagu-workflow.yaml"
		if suffix > 0 {
			name = fmt.Sprintf(".dagu-workflow-%d.yaml", suffix)
		}
		_, err := os.Lstat(filepath.Join(root, name))
		if os.IsNotExist(err) {
			return name, nil
		}
		if err != nil {
			return "", fmt.Errorf("inspect workspace DAG path %q: %w", name, err)
		}
	}
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
