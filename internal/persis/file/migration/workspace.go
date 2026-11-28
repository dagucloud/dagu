// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package migration

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectrecord"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// maxNameLength is the most characters a project name holds.
const maxNameLength = 64

// fallbackName is the name made from a workspace name that has no letter or
// digit to start a project name with.
const fallbackName = "workspace"

// scannedDAG is a DAG file in paths.dags_dir, read as v2 read it.
type scannedDAG struct {
	// name is the DAG's name, which keys its runs and most of its state.
	name string
	// fileName is the file's name without its extension, which keys the
	// rest of its state and which v2's webhook URL of the DAG names.
	fileName string
	// path is the file, absolute with its symbolic links resolved.
	path string
	// queue is the queue the DAG's runs wait in, "" for the DAG's own.
	queue string
	// workspace is the v2 workspace whose label the file's first document
	// carries, or "" when the file is the default project's.
	workspace string
}

// workspaceMovePlanner returns the planner that makes each v2 workspace a
// project and moves the state of the workspace's DAGs from the default
// project into it. It registers each project, with the workspace's DAG files
// as files that run in place, before it plans the moves. A project
// registered by an earlier attempt keeps its ID and name and gets the
// records of the current files, so a resumed plan places state in the same
// projects.
func workspaceMovePlanner(cfg *config.Config, sameFileSystem func(a, b string) (bool, error)) func(context.Context) ([]move, error) {
	return func(ctx context.Context) ([]move, error) {
		workspaces, err := v2Workspaces(ctx, cfg)
		if err != nil || len(workspaces) == 0 {
			return nil, err
		}
		dags, err := scanDAGs(ctx, cfg, workspaces)
		if err != nil {
			return nil, err
		}
		names, err := registerWorkspaces(ctx, cfg, workspaces, dags)
		if err != nil {
			return nil, err
		}
		return planWorkspaceMoves(ctx, cfg, names, dags, sameFileSystem)
	}
}

// v2Workspaces returns the names of the v2 workspaces, sorted.
func v2Workspaces(ctx context.Context, cfg *config.Config) ([]string, error) {
	if cfg.Paths.WorkspacesDir == "" {
		return nil, nil
	}
	store, err := persisfile.NewServerStores(cfg).WorkspaceStore()
	if err != nil {
		return nil, fmt.Errorf("read the workspaces: %w", err)
	}
	list, err := store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the workspaces: %w", err)
	}
	names := make([]string, 0, len(list))
	for _, ws := range list {
		names = append(names, ws.Name)
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

// scanDAGs returns the DAG files in paths.dags_dir, each with the workspace
// of workspaces whose label its first document carries. A file that fails
// to load carries no label, and stays the default project's.
func scanDAGs(ctx context.Context, cfg *config.Config, workspaces []string) ([]scannedDAG, error) {
	if cfg.Paths.DAGsDir == "" {
		return nil, nil
	}
	scan, err := filedag.Discover(cfg.Paths.DAGsDir, filedag.DiscoveryOptions{
		Recursive:       cfg.DAGDiscovery.Recursive,
		Symlinks:        cfg.DAGDiscovery.Symlinks,
		SkipDirs:        cfg.DAGDiscovery.SkipDirs,
		SkipRoots:       cfg.DiscoverySkipRoots(),
		ConfineSymlinks: true,
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find the DAGs of the workspaces: %w", err)
	}
	var dags []scannedDAG
	for _, file := range scan.Files {
		dag, err := spec.Load(ctx, file.ResolvedPath,
			spec.OnlyMetadata(), spec.WithoutEval(), spec.SkipSchemaValidation())
		if err != nil {
			continue
		}
		path, err := filepath.Abs(file.ResolvedPath)
		if err != nil {
			return nil, err
		}
		if real, err := filepath.EvalSymlinks(path); err == nil {
			path = real
		}
		scanned := scannedDAG{
			name:     dag.Name,
			fileName: fileutil.TrimYAMLFileExtension(filepath.Base(filepath.FromSlash(file.RelPath))),
			path:     path,
			queue:    dag.Queue,
		}
		if ws, ok := workspace.WorkspaceNameFromLabels(dag.Labels); ok && slices.Contains(workspaces, ws) {
			scanned.workspace = ws
		}
		dags = append(dags, scanned)
	}
	slices.SortFunc(dags, func(a, b scannedDAG) int { return strings.Compare(a.path, b.path) })
	return dags, nil
}

// registerWorkspaces registers a project for each workspace, with the
// records of its DAGs and files, records it in each of its state
// directories, and returns the name of each workspace's project.
func registerWorkspaces(
	ctx context.Context, cfg *config.Config, workspaces []string, dags []scannedDAG,
) (map[string]string, error) {
	registry := projectregistry.New(cfg.Paths.DataDir)
	registered, err := registry.List(ctx)
	if err != nil {
		return nil, err
	}
	names, err := projectNames(cfg, workspaces, registered)
	if err != nil {
		return nil, err
	}
	fileOwner := fileStateOwners(dags)
	for _, ws := range workspaces {
		in := project.CreateInput{Name: names[ws], DisplayName: ws, MigratedFrom: ws}
		for _, dag := range dags {
			if dag.workspace != ws {
				continue
			}
			in.InPlaceFiles = append(in.InPlaceFiles, dag.path)
			// v2's webhook URL of the file name reaches the project that
			// gets its webhook.
			if fileOwner[planKey(dag.fileName)] == ws && !slices.Contains(in.MigratedDAGs, dag.fileName) {
				in.MigratedDAGs = append(in.MigratedDAGs, dag.fileName)
			}
		}
		p, err := registry.RegisterMigrated(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("register workspace %s as project %s: %w", ws, in.Name, err)
		}
		paths, err := cfg.ProjectPaths(p.Name)
		if err != nil {
			return nil, err
		}
		for _, dir := range paths.RecordDirs {
			if err := projectrecord.Write(dir, projectrecord.Record{ID: p.ID, Name: p.Name}); err != nil {
				return nil, err
			}
		}
	}
	return names, nil
}

// projectNames returns the name of each workspace's project: the one an
// earlier attempt registered, else the workspace name when it keeps the name
// rules and is free, else a valid name made from it, with a number added
// while that is taken. Workspaces whose names are valid as they are choose
// first, so a renamed workspace never takes another one's name.
func projectNames(cfg *config.Config, workspaces []string, registered []project.Project) (map[string]string, error) {
	names := make(map[string]string, len(workspaces))
	taken := map[string]bool{project.DefaultName: true}
	for _, p := range registered {
		taken[strings.ToLower(p.Name)] = true
		if p.MigratedFrom != "" {
			names[p.MigratedFrom] = p.Name
		}
	}
	free := func(name string) (bool, error) {
		if project.ValidateName(name) != nil || taken[strings.ToLower(name)] {
			return false, nil
		}
		// A state directory of the name may record a deleted project.
		paths, err := cfg.ProjectPaths(name)
		if err != nil {
			return false, err
		}
		for _, dir := range paths.RecordDirs {
			if _, err := projectrecord.Read(dir); !errors.Is(err, fs.ErrNotExist) {
				return false, err
			}
		}
		return true, nil
	}
	claim := func(ws, name string) {
		names[ws] = name
		taken[strings.ToLower(name)] = true
	}

	for _, ws := range workspaces {
		if _, ok := names[ws]; ok {
			continue
		}
		ok, err := free(ws)
		if err != nil {
			return nil, err
		}
		if ok {
			claim(ws, ws)
		}
	}
	for _, ws := range workspaces {
		if _, ok := names[ws]; ok {
			continue
		}
		base := nameFrom(ws)
		name := base
		for n := 2; ; n++ {
			ok, err := free(name)
			if err != nil {
				return nil, err
			}
			if ok {
				break
			}
			suffix := "-" + strconv.Itoa(n)
			name = base[:min(len(base), maxNameLength-len(suffix))] + suffix
		}
		claim(ws, name)
	}
	return names, nil
}

// nameFrom makes a project name from a workspace name: it drops the
// characters a name cannot start with, turns every other character a name
// cannot hold into '-', and keeps the first maxNameLength characters. The
// result may still be reserved or a Windows device name.
func nameFrom(ws string) string {
	ws = strings.TrimLeftFunc(ws, func(r rune) bool { return !isASCIIAlnum(r) })
	name := strings.Map(func(r rune) rune {
		if isASCIIAlnum(r) || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, ws)
	if name == "" {
		return fallbackName
	}
	return name[:min(len(name), maxNameLength)]
}

func isASCIIAlnum(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}
