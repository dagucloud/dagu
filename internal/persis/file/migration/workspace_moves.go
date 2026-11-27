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

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/file/dag/dagindex"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

const (
	attentionFileName = "attention.json"
	// attentionSharedName is the kind of an attention item for a DAG name
	// that DAGs of more than one project share.
	attentionSharedName = "shared-dag-name"
	// attentionSharedFileName is the kind of an attention item for a DAG
	// file name that files of more than one project share.
	attentionSharedFileName = "shared-dag-file-name"
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
// v2 keeps most of a DAG's state by the DAG's name, so DAGs of the same name
// share it. A name that only one project's DAGs have moves with all of that
// state. For a name that DAGs of several projects have, each run moves to
// the project of the workspace that the run's labels name, as v2 showed it
// to that workspace's users, and the rest stays in the default project.
//
// v2 keeps the suspend flag, DAG settings, scheduler record, and webhook of
// a DAG by its file's name, which follows its file's project; see
// fileStateOwners. A suspend flag that files of several projects share is
// copied to each of them, so no suspended DAG resumes.
//
// Each name or file name that several projects share is recorded for the
// migration report.
func planWorkspaceMoves(
	ctx context.Context, cfg *config.Config, names map[string]string, dags []scannedDAG,
	sameFileSystem func(a, b string) (bool, error),
) ([]move, error) {
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
		if err := linkStateRoots(ctx, from, to, name, sameFileSystem); err != nil {
			return nil, err
		}
		paths[ws] = to
	}
	projectOf := func(ws string) string {
		if ws == "" {
			return project.DefaultName
		}
		return names[ws]
	}
	allNames := make([]string, 0, len(dags))
	for _, dag := range dags {
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
	)
	nameOwners := groupOwners(dags, func(dag scannedDAG) string { return dag.name })
	planned := make(map[string]bool)
	for _, dag := range dags {
		key := planKey(dag.name)
		if dag.workspace == "" || planned[key] {
			continue
		}
		planned[key] = true
		var found []persisfile.DAGStatePath
		if owners := nameOwners[key]; len(owners) > 1 {
			attention = append(attention, sharedItem(attentionSharedName, dag.name, owners, projectOf))
			for _, spelling := range spellings(dags, key) {
				runs, err := persisfile.DAGRunPaths(ctx, cfg, from, spelling, byRunLabels)
				if err != nil {
					return nil, err
				}
				found = append(found, runs...)
			}
		} else {
			found, err = persisfile.DAGStatePaths(from, paths[dag.workspace], persisfile.DAGStateKeys{
				Name:     dag.name,
				OwnQueue: ownsQueue(cfg, dags, dag),
				Others:   slices.DeleteFunc(slices.Clone(allNames), func(n string) bool { return n == dag.name }),
			})
			if err != nil {
				return nil, err
			}
		}
		moves = appendMoves(moves, found)
	}

	fileOwner := fileStateOwners(dags)
	fileOwners := groupOwners(dags, func(dag scannedDAG) string { return dag.fileName })
	flagOwners := groupOwners(dags, func(dag scannedDAG) string { return dagindex.SuspendFlagName(dag.fileName) })
	planned = make(map[string]bool)
	for _, dag := range dags {
		key := planKey(dag.fileName)
		if planned[key] {
			continue
		}
		planned[key] = true
		if owners := fileOwners[key]; len(owners) > 1 {
			attention = append(attention, sharedItem(attentionSharedFileName, dag.fileName, owners, projectOf))
		}
		flag := dagindex.SuspendFlagName(dag.fileName)
		flagShared := len(flagOwners[planKey(flag)]) > 1
		if flagShared {
			for _, ws := range flagOwners[planKey(flag)] {
				if ws != "" {
					if err := copySuspendFlag(from, paths[ws], flag); err != nil {
						return nil, err
					}
				}
			}
		}
		ws := fileOwner[key]
		if ws == "" {
			continue
		}
		found, err := persisfile.DAGFileStatePaths(ctx, from, paths[ws], dag.fileName)
		if err != nil {
			return nil, err
		}
		if flagShared {
			found = slices.DeleteFunc(found, func(p persisfile.DAGStatePath) bool {
				return p.From == filepath.Join(from.SuspendFlagsDir, flag)
			})
		}
		moves = appendMoves(moves, found)
	}

	if err := (journal{dir: filepath.Join(cfg.Paths.DataDir, dirName)}).writeJSON(attentionFileName, attentionFile{Items: attention}); err != nil {
		return nil, err
	}
	return uniqueMoves(moves), nil
}

// fileStateOwners returns, by planKey of a file name, the workspace whose
// project gets the state v2 keeps by that file name, "" for the default
// project. Files of one name in several projects, which only recursive
// discovery finds, leave it in the default project when it has such a file,
// else give it to the project of the one whose path sorts first, as dags
// are sorted, so v2's webhook URL of the name keeps reaching a DAG with its
// webhook.
func fileStateOwners(dags []scannedDAG) map[string]string {
	owners := make(map[string]string)
	for _, dag := range dags {
		key := planKey(dag.fileName)
		owner, seen := owners[key]
		switch {
		case !seen:
			owners[key] = dag.workspace
		case owner != "" && dag.workspace == "":
			owners[key] = ""
		}
	}
	return owners
}

// groupOwners returns, by planKey of the key of each DAG of dags, the
// workspaces of the DAGs with that key, "" for the default project, in the
// order of dags.
func groupOwners(dags []scannedDAG, key func(scannedDAG) string) map[string][]string {
	owners := make(map[string][]string)
	for _, dag := range dags {
		k := planKey(key(dag))
		if !slices.Contains(owners[k], dag.workspace) {
			owners[k] = append(owners[k], dag.workspace)
		}
	}
	return owners
}

// sharedItem returns the attention item of kind for the name that the
// workspaces owners share.
func sharedItem(kind, name string, owners []string, projectOf func(string) string) attentionItem {
	projects := make([]string, 0, len(owners))
	for _, ws := range owners {
		projects = append(projects, projectOf(ws))
	}
	slices.Sort(projects)
	return attentionItem{Kind: kind, DAG: name, Projects: projects}
}

// copySuspendFlag copies the suspend flag of the default project's stores
// on from to the stores on to, unless to holds it already.
func copySuspendFlag(from, to config.ProjectPaths, flag string) error {
	data, err := fileutil.ReadFile(filepath.Join(from.SuspendFlagsDir, flag))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("plan the migration: %w", err)
	}
	target := filepath.Join(to.SuspendFlagsDir, flag)
	if found, err := exists(target); err != nil || found {
		return err
	}
	if err := fileutil.MkdirAll(to.SuspendFlagsDir, dirPerm); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(target, data, filePerm)
}

func appendMoves(moves []move, paths []persisfile.DAGStatePath) []move {
	for _, path := range paths {
		moves = append(moves, move{From: path.From, To: path.To})
	}
	return moves
}

// spellings returns each spelling, among dags, of the DAG name whose
// planKey is key. File systems that ignore case keep their state in one
// place.
func spellings(dags []scannedDAG, key string) []string {
	var names []string
	for _, dag := range dags {
		if planKey(dag.name) == key && !slices.Contains(names, dag.name) {
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
// from a store directory of from that is a symbolic link or a junction to
// another file system, as version 2 kept the default project's: the
// directory of to becomes a link to a directory beside the default
// project's in the link's target, so the state moves by rename. Where the
// link cannot be made, as on Windows without the privilege to create
// symbolic links, the directory stays plain and the state is copied. A
// directory of to that exists is left as it is.
func linkStateRoots(
	ctx context.Context, from, to config.ProjectPaths, name string, sameFileSystem func(a, b string) (bool, error),
) error {
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
		same, err := sameFileSystem(real, root.To)
		if err != nil {
			return fmt.Errorf("plan the migration: %w", err)
		}
		if same {
			continue
		}
		target := filepath.Join(real, setApartDirName, name)
		if filepath.Base(real) == project.DefaultName && filepath.Base(filepath.Dir(real)) == setApartDirName {
			target = filepath.Join(filepath.Dir(real), name)
		}
		if err := fileutil.MkdirAll(filepath.Dir(root.To), dirPerm); err != nil {
			return err
		}
		if err := os.Symlink(target, root.To); err != nil {
			logger.Warn(ctx, "Copying a project's state off the disk a link leads to, since the link cannot be made; "+
				"on Windows, creating symbolic links needs Developer Mode or an elevated process",
				tag.Name(name), tag.Path(root.To), tag.Error(err))
			continue
		}
		if err := fileutil.MkdirAll(target, dirPerm); err != nil {
			return errors.Join(err, fileutil.Remove(root.To))
		}
	}
	return nil
}
