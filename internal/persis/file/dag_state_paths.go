// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/cmn/artifactpath"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis"
	fileartifact "github.com/dagucloud/dagu/v2/internal/persis/file/artifact"
	"github.com/dagucloud/dagu/v2/internal/persis/file/dag/dagindex"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
)

// DAGStatePath is a file or directory that holds state of one DAG in the
// stores of one project, and the path where the stores of another project
// keep it.
type DAGStatePath struct {
	From string
	To   string
}

// DAGStatePaths returns each file and directory that holds state of the DAG
// called name, whose file is named fileName without its extension, in the
// stores on from, paired with where the stores on to keep it: its runs,
// ephemeral runs, run work directories, logs, artifacts, suspend flag, the
// DAG state it keeps for itself and for its sub-DAG runs, DAG settings,
// scheduler record, slot index, and webhook. Both are a project's paths
// while projects are on. A path that does not exist is left out.
func DAGStatePaths(ctx context.Context, from, to config.ProjectPaths, name, fileName string) ([]DAGStatePath, error) {
	var paths []DAGStatePath
	// pair adds rel in fromDir, when it exists, paired with rel in toDir.
	pair := func(fromDir, toDir, rel string) error {
		found, err := pathExists(filepath.Join(fromDir, rel))
		if found {
			paths = append(paths, DAGStatePath{From: filepath.Join(fromDir, rel), To: filepath.Join(toDir, rel)})
		}
		return err
	}
	fromSpecs, toSpecs := projectCollectionSpecs(from), projectCollectionSpecs(to)
	dagState := persis.CollectionDAGState
	dirs := []struct{ from, to, rel string }{
		{from.DAGRunsDir, to.DAGRunsDir, filedagrun.DAGDirName(from.DAGRunsDir, name)},
		{
			filepath.Join(from.OtherStateDir, ephemeralRunsDirName), filepath.Join(to.OtherStateDir, ephemeralRunsDirName),
			filedagrun.DAGDirName(filepath.Join(from.OtherStateDir, ephemeralRunsDirName), name),
		},
		{from.DAGRunWorkDir, to.DAGRunWorkDir, filedagrun.DAGDirName(from.DAGRunWorkDir, name)},
		{from.SuspendFlagsDir, to.SuspendFlagsDir, dagindex.SuspendFlagName(fileName)},
		{
			filepath.Join(from.OtherStateDir, slotIndexDirName), filepath.Join(to.OtherStateDir, slotIndexDirName),
			hex.EncodeToString([]byte(name)),
		},
	}
	// Released versions named a DAG's directory of logs, and of artifacts
	// before artifacts were kept by date, with SafeName.
	for _, dirName := range slices.Compact([]string{fileutil.SafeKey(name), fileutil.SafeName(name)}) {
		dirs = append(dirs,
			struct{ from, to, rel string }{from.LogDir, to.LogDir, dirName},
			struct{ from, to, rel string }{from.ArtifactDir, to.ArtifactDir, dirName})
	}
	// The DAG state of the dag and root_dag scopes is kept by DAG name.
	for _, scope := range []dagrun.StateScope{dagrun.StateScopeDAG, dagrun.StateScopeRootDAG} {
		dirs = append(dirs, struct{ from, to, rel string }{
			fromSpecs[dagState].dir, toSpecs[dagState].dir,
			recordDirPath(dagStateVersionSegment, string(scope), hex.EncodeToString([]byte(name))),
		})
	}
	for _, dir := range dirs {
		if err := pair(dir.from, dir.to, dir.rel); err != nil {
			return nil, err
		}
	}
	artifacts, err := datedArtifactPaths(from.ArtifactDir, to.ArtifactDir, name)
	if err != nil {
		return nil, err
	}
	paths = append(paths, artifacts...)

	// The records the API and the scheduler keep by the DAG's file name.
	records := []struct{ collection, id string }{
		{persis.CollectionDAGSettings, fileName},
		{persis.CollectionSchedulerState, persis.SchedulerDAGRecordID(fileName)},
		{persis.CollectionWebhookDAGClaims, fileName},
	}
	webhook, err := webhookIDOf(ctx, fromSpecs[persis.CollectionWebhookDAGClaims].dir, fileName)
	if err != nil {
		return nil, err
	}
	if webhook != "" {
		records = append(records, struct{ collection, id string }{persis.CollectionWebhooks, webhook})
	}
	for _, rec := range records {
		file, ok, err := NewCollection(fromSpecs[rec.collection].dir).recordFile(rec.id)
		if err != nil {
			return nil, err
		}
		if ok {
			paths = append(paths, DAGStatePath{From: file, To: filepath.Join(toSpecs[rec.collection].dir, idToRelPath(rec.id))})
		}
	}
	return paths, nil
}

// dagStateVersionSegment is the first segment of every DAG state record ID.
const dagStateVersionSegment = "v1"

// recordDirPath returns the directory, relative to its collection, that
// holds the records whose IDs begin with the segments.
func recordDirPath(segments ...string) string {
	for i, segment := range segments {
		segments[i] = escapeSegment(segment, true)
	}
	return filepath.Join(segments...)
}

// webhookIDOf returns the ID of the webhook that holds the claim of the DAG
// called name in the claims collection in dir, or "" when none does.
func webhookIDOf(ctx context.Context, dir, name string) (string, error) {
	rec, err := NewCollection(dir).Get(ctx, name)
	if errors.Is(err, persis.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var claim struct {
		WebhookID string `json:"webhookId"`
	}
	if err := persis.Decode(rec, &claim); err != nil {
		return "", fmt.Errorf("read the webhook claim of DAG %s: %w", name, err)
	}
	return claim.WebhookID, nil
}

// datedArtifactPaths returns the run directories and sidecars, kept by date
// in fromDir, of the runs of the DAG called name, paired with their places
// in toDir. A run directory that a DAG placed elsewhere stays there; its
// sidecar moves.
func datedArtifactPaths(fromDir, toDir, name string) ([]DAGStatePath, error) {
	segment := artifactpath.DAGNameSegment(name)
	days, err := filepath.Glob(filepath.Join(fromDir, "[0-9][0-9][0-9][0-9]", "[0-9][0-9]", "[0-9][0-9]"))
	if err != nil {
		return nil, err
	}
	var paths []DAGStatePath
	for _, day := range days {
		entries, err := os.ReadDir(day)
		if err != nil {
			return nil, fmt.Errorf("read the artifacts of %s: %w", day, err)
		}
		rel, err := filepath.Rel(fromDir, day)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !artifactpath.IsMetaName(entry.Name()) {
				continue
			}
			runDir := artifactpath.TrimMetaSuffix(entry.Name())
			parsed, ok := artifactpath.ParseRunDirName(runDir)
			if !ok || parsed.DAGName != segment {
				continue
			}
			record, err := fileartifact.ReadRecord(filepath.Join(day, entry.Name()))
			if err != nil || record.Name != name {
				continue
			}
			for _, item := range []string{entry.Name(), runDir} {
				from := filepath.Join(day, item)
				found, err := pathExists(from)
				if err != nil {
					return nil, err
				}
				if found {
					paths = append(paths, DAGStatePath{From: from, To: filepath.Join(toDir, rel, item)})
				}
			}
		}
	}
	return paths, nil
}

// recordFile returns the file that holds the record id, and false when no
// file does.
func (c *Collection) recordFile(id string) (string, bool, error) {
	path, err := c.filePath(id)
	if errors.Is(err, persis.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	path = c.existingPath(id, path)
	found, err := pathExists(path)
	return path, found, err
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}
