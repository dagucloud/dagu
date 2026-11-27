// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package migration

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
)

// setApartDefaultDir is where the default project keeps its state in a path
// set apart, relative to that path.
var setApartDefaultDir = filepath.Join(".projects", "default")

// stateMovePlanner returns the planner of the moves of the default project's
// state out of the directories where v2 keeps it.
func stateMovePlanner(cfg *config.Config) func(context.Context) ([]move, error) {
	return func(context.Context) ([]move, error) {
		dirs := persisfile.DefaultProjectV2StateDirs(cfg)
		var moves []move
		for i, dir := range dirs {
			planned, err := planDir(dir, keptPaths(cfg, dirs, i))
			if err != nil {
				return nil, err
			}
			moves = append(moves, planned...)
		}
		return moves, nil
	}
}

// planDir moves dir.V2 to dir.Dir in one move when it can. It moves each
// entry on its own when only some entries belong in dir.Dir, when dir.Dir
// exists, or when dir.V2 holds a path in kept, whose entries stay.
func planDir(dir persisfile.V2StateDir, kept []string) ([]move, error) {
	info, err := os.Lstat(dir.V2)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("plan the migration: %w", err)
	}
	if info.Mode().IsRegular() {
		return []move{{From: dir.V2, To: dir.Dir}}, nil
	}
	found, err := exists(dir.Dir)
	if err != nil {
		return nil, fmt.Errorf("plan the migration: %w", err)
	}
	if dir.Holds == nil && !found && !holdsAny(dir.V2, kept) {
		return []move{{From: dir.V2, To: dir.Dir}}, nil
	}
	_, link, err := readLink(dir.V2, info.Mode())
	if err != nil {
		return nil, fmt.Errorf("plan the migration: %w", err)
	}
	if link && !inside(dir.Dir, dir.V2) {
		store, err := linkBeside(dir, found)
		if err != nil {
			return nil, fmt.Errorf("plan the migration: %w", err)
		}
		kept = append(kept, store)
	}

	entries, err := os.ReadDir(dir.V2)
	if err != nil {
		return nil, fmt.Errorf("plan the migration: %w", err)
	}
	var moves []move
	for _, entry := range entries {
		from := filepath.Join(dir.V2, entry.Name())
		if isOrHolds(from, kept) || (dir.Holds != nil && !dir.Holds(entry)) {
			continue
		}
		moves = append(moves, move{From: from, To: filepath.Join(dir.Dir, entry.Name())})
	}
	return moves, nil
}

// linkBeside keeps the entries that move out of dir.V2, a link that may
// lead to another file system, on the file system it leads to. It makes
// dir.Dir a link to a directory in the link's target, the one a project's
// state set apart in a path takes, so each entry moves by rename. found
// reports that dir.Dir exists: an earlier attempt made the link, or it is
// not this migration's and the entries move into it. It returns the
// directory as dir.V2 spells it, or "" when dir.Dir is not the link.
func linkBeside(dir persisfile.V2StateDir, found bool) (string, error) {
	real, err := realPath(dir.V2)
	if err != nil {
		return "", err
	}
	store := filepath.Join(real, setApartDefaultDir)
	if found {
		if target, err := os.Readlink(dir.Dir); err != nil || filepath.Clean(target) != store {
			return "", nil
		}
		return filepath.Join(dir.V2, setApartDefaultDir), nil
	}
	if err := fileutil.MkdirAll(store, dirPerm); err != nil {
		return "", err
	}
	if err := fileutil.MkdirAll(filepath.Dir(dir.Dir), dirPerm); err != nil {
		return "", err
	}
	if err := os.Symlink(store, dir.Dir); err != nil {
		// A path set apart puts the project's directory inside dir.V2,
		// where entries move by rename without a link.
		return "", fmt.Errorf("keep the state in %s on its disk with the link %s: %w; "+
			"making a link needs the privilege to create symbolic links (on Windows, Developer Mode or "+
			"an elevated process). For the log, artifact, run, and run work directories, setting the "+
			"matching paths.log_dir, paths.artifact_dir, paths.dag_runs_dir, or paths.dag_run_work_dir "+
			"explicitly, even to the same path, keeps the state in %s without a link", real, dir.Dir, err, dir.V2)
	}
	return filepath.Join(dir.V2, setApartDefaultDir), nil
}

// keptPaths returns the paths that the moves of dirs[index] leave in place:
// the other state directories, the destinations, and the server-wide state
// and DAG directories that a v2 directory may hold, such as the admin logs
// in the log directory.
func keptPaths(cfg *config.Config, dirs []persisfile.V2StateDir, index int) []string {
	p := cfg.Paths
	kept := []string{
		p.DataDir, p.AdminLogsDir, p.DAGsDir, p.ProjectsDir, p.AltDAGsDir, p.WikiDir, p.CalendarsDir,
		p.BaseConfig, p.ServiceRegistryDir, p.UsersDir, p.APIKeysDir, p.ContextsDir,
		p.RemoteNodesDir, p.WorkspacesDir, p.ToolsDir,
	}
	for i, dir := range dirs {
		kept = append(kept, dir.Dir)
		if i != index {
			kept = append(kept, dir.V2)
		}
	}
	return kept
}

// holdsAny reports whether one of paths lies inside dir.
func holdsAny(dir string, paths []string) bool {
	for _, path := range paths {
		if path != "" && inside(path, dir) {
			return true
		}
	}
	return false
}

// isOrHolds reports whether path is one of paths or holds one of them.
func isOrHolds(path string, paths []string) bool {
	for _, other := range paths {
		if other != "" && (planKey(other) == planKey(path) || inside(other, path)) {
			return true
		}
	}
	return false
}

// inside reports whether path lies inside dir.
func inside(path, dir string) bool {
	return strings.HasPrefix(planKey(path), planKey(dir)+string(filepath.Separator))
}
