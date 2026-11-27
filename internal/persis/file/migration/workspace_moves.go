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
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

const (
	attentionFileName = "attention.json"
	// attentionSharedName is the kind of an attention item for a DAG name
	// that DAGs of more than one project share.
	attentionSharedName = "shared-dag-name"
	// setApartDirName holds the state of each project beside the others in
	// a path set apart, and beside a link's target.
	setApartDirName = ".projects"
)

// attentionFile is what attention.json holds: the items that the plan of
// the workspaces' moves could not settle, for the migration report.
type attentionFile struct {
	Items []attentionItem `json:"items"`
}

// attentionItem is one item that needs attention.
type attentionItem struct {
	Kind string `json:"kind"`
	// DAG is the DAG name the item is about.
	DAG string `json:"dag"`
	// Projects names the projects the item involves.
	Projects []string `json:"projects"`
}

// planWorkspaceMoves plans the moves of the state of each workspace DAG
// from the default project into the project of its workspace, names giving
// each workspace's project.
//
// v2 keeps a DAG's state by its name, so DAGs of the same name share it. A
// name that only one project's DAGs have moves with all its state. For a
// name that DAGs of several projects have, each run moves to the project of
// the workspace that the run's labels name, as v2 showed it to that
// workspace's users, and the rest of the name's state stays in the default
// project; the name is recorded for the migration report.
func planWorkspaceMoves(ctx context.Context, cfg *config.Config, names map[string]string, dags []scannedDAG) ([]move, error) {
	from, err := cfg.ProjectPaths(project.DefaultName)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]config.ProjectPaths, len(names))
	for ws, name := range names {
		to, err := cfg.ProjectPaths(name)
		if err != nil {
			return nil, err
		}
		if err := linkStateRoots(from, to, name); err != nil {
			return nil, err
		}
		paths[ws] = to
	}

	owners := make(map[string][]string)
	allNames := make([]string, 0, len(dags))
	for _, dag := range dags {
		owner := project.DefaultName
		if dag.workspace != "" {
			owner = names[dag.workspace]
		}
		key := strings.ToLower(dag.name)
		if !slices.Contains(owners[key], owner) {
			owners[key] = append(owners[key], owner)
		}
		allNames = append(allNames, dag.name)
	}
	byRunLabels := func(labels []string) (config.ProjectPaths, bool) {
		ws, ok := workspace.WorkspaceNameFromLabels(ir.NewLabels(labels))
		to, found := paths[ws]
		return to, ok && found
	}

	var (
		moves     []move
		attention []attentionItem
		planned   = make(map[string]bool)
	)
	for _, dag := range dags {
		key := strings.ToLower(dag.name)
		if dag.workspace == "" || planned[key] {
			continue
		}
		planned[key] = true
		var found []persisfile.DAGStatePath
		if shared := owners[key]; len(shared) > 1 {
			slices.Sort(shared)
			attention = append(attention, attentionItem{Kind: attentionSharedName, DAG: dag.name, Projects: shared})
			for _, spelling := range spellings(dags, key) {
				runs, err := persisfile.DAGRunPaths(ctx, cfg, from, spelling, byRunLabels)
				if err != nil {
					return nil, err
				}
				found = append(found, runs...)
			}
		} else {
			found, err = persisfile.DAGStatePaths(ctx, from, paths[dag.workspace], persisfile.DAGStateKeys{
				Name:     dag.name,
				FileName: dag.fileName,
				OwnQueue: ownsQueue(cfg, dags, dag),
				Others:   slices.DeleteFunc(slices.Clone(allNames), func(n string) bool { return n == dag.name }),
			})
			if err != nil {
				return nil, err
			}
		}
		for _, path := range found {
			moves = append(moves, move{From: path.From, To: path.To})
		}
	}
	if err := (journal{dir: filepath.Join(cfg.Paths.DataDir, dirName)}).writeJSON(attentionFileName, attentionFile{Items: attention}); err != nil {
		return nil, err
	}
	return uniqueMoves(moves), nil
}

// spellings returns each spelling, among dags, of the DAG name whose lower
// case is key. File systems that ignore case keep their state in one place.
func spellings(dags []scannedDAG, key string) []string {
	var names []string
	for _, dag := range dags {
		if strings.ToLower(dag.name) == key && !slices.Contains(names, dag.name) {
			names = append(names, dag.name)
		}
	}
	return names
}

// ownsQueue reports whether dag's runs wait in the queue of its own name,
// which no queue of config.yaml and no other DAG uses.
func ownsQueue(cfg *config.Config, dags []scannedDAG, dag scannedDAG) bool {
	if dag.queue != "" && dag.queue != dag.name {
		return false
	}
	if slices.ContainsFunc(cfg.Queues.Config, func(q config.QueueConfig) bool { return q.Name == dag.name }) {
		return false
	}
	return !slices.ContainsFunc(dags, func(other scannedDAG) bool {
		return other.name != dag.name && other.queue == dag.name
	})
}

// uniqueMoves drops each move of a path that an earlier move moves, as when
// two spellings of a name name one directory.
func uniqueMoves(moves []move) []move {
	seen := make(map[string]bool, len(moves))
	return slices.DeleteFunc(moves, func(mv move) bool {
		key := planKey(mv.From)
		if seen[key] {
			return true
		}
		seen[key] = true
		return false
	})
}

// linkStateRoots keeps on the disk a link leads to the state that moves
// from a store directory of from that is a symbolic link or a junction, as
// version 2 kept the default project's: the directory of to becomes a link
// to a directory beside the default project's in the link's target, so the
// state moves by rename. A directory of to that exists is left as it is.
func linkStateRoots(from, to config.ProjectPaths, name string) error {
	for _, root := range persisfile.DAGStateRoots(from, to) {
		info, err := os.Lstat(root.From)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("plan the migration: %w", err)
		}
		_, link, err := readLink(root.From, info.Mode())
		if err != nil {
			return fmt.Errorf("plan the migration: %w", err)
		}
		if !link {
			continue
		}
		found, err := exists(root.To)
		if err != nil {
			return fmt.Errorf("plan the migration: %w", err)
		}
		if found {
			continue
		}
		real, err := realPath(root.From)
		if err != nil {
			return fmt.Errorf("plan the migration: %w", err)
		}
		target := filepath.Join(real, setApartDirName, name)
		if filepath.Base(real) == project.DefaultName && filepath.Base(filepath.Dir(real)) == setApartDirName {
			target = filepath.Join(filepath.Dir(real), name)
		}
		if err := fileutil.MkdirAll(target, dirPerm); err != nil {
			return err
		}
		if err := fileutil.MkdirAll(filepath.Dir(root.To), dirPerm); err != nil {
			return err
		}
		if err := os.Symlink(target, root.To); err != nil {
			return fmt.Errorf("keep the state of project %s in %s on its disk with the link %s: %w; "+
				"making a link needs the privilege to create symbolic links (on Windows, Developer Mode or "+
				"an elevated process)", name, target, root.To, err)
		}
	}
	return nil
}
