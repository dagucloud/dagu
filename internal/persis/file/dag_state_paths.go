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
	"regexp"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/artifactpath"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis"
	fileartifact "github.com/dagucloud/dagu/v2/internal/persis/file/artifact"
	"github.com/dagucloud/dagu/v2/internal/persis/file/dag/dagindex"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis/file/runpath"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
)

// DAGStatePath is a file or directory that holds state of one DAG in the
// stores of one project, and the path where the stores of another project
// keep it.
type DAGStatePath struct {
	From string
	To   string
}

// DAGStateKeys names the state of one DAG.
type DAGStateKeys struct {
	// Name is the DAG's name, which keys its runs, logs, artifacts, DAG
	// state, and slot index.
	Name string
	// FileName is the name of the DAG's file without its extension, which
	// keys its suspend flag, DAG settings, scheduler record, and webhook.
	FileName string
	// OwnQueue reports that the DAG's runs queue in the queue of its own
	// name, which no other DAG uses.
	OwnQueue bool
	// Others names every other DAG, so a directory that a released version
	// named for one of them as well is left where it is.
	Others []string
}

// rootEntry is a path relative to a store directory.
type rootEntry struct {
	root *DAGStatePath
	rel  string
}

// legacyArtifactDirPattern matches the year directories of the artifacts
// kept by date, which no DAG's directory of artifacts is.
var legacyArtifactDirPattern = regexp.MustCompile(`^[0-9]{4}$`)

// DAGStatePaths returns each file and directory that holds state of the DAG
// keys names in the stores on from, paired with where the stores on to keep
// it: its runs, ephemeral runs, run work directories, logs, artifacts,
// queue, suspend flag, the DAG state it keeps for itself and for its
// sub-DAG runs, DAG settings, scheduler record, slot index, and webhook.
// Both are a project's paths while projects are on. A path that does not
// exist is left out.
func DAGStatePaths(ctx context.Context, from, to config.ProjectPaths, keys DAGStateKeys) ([]DAGStatePath, error) {
	name, fileName := keys.Name, keys.FileName
	roots := stateRoots(from, to)
	entries := []rootEntry{
		{&roots.runs, filedagrun.DAGDirName(roots.runs.From, name)},
		{&roots.ephemeral, filedagrun.DAGDirName(roots.ephemeral.From, name)},
		{&roots.work, filedagrun.DAGDirName(roots.work.From, name)},
		{&roots.logs, fileutil.SafeKey(name)},
		{&roots.artifacts, fileutil.SafeKey(name)},
		{&roots.suspend, dagindex.SuspendFlagName(fileName)},
		{&roots.slots, hex.EncodeToString([]byte(name))},
	}
	if keys.OwnQueue {
		entries = append(entries, rootEntry{&roots.queue, escapeSegment(name, true)})
	}
	// Released versions named a DAG's directory of logs, and of artifacts
	// before artifacts were kept by date, with SafeName, which other names
	// can share.
	if legacy, own := fileutil.LegacySafeName(name, keys.Others); own && legacy != fileutil.SafeKey(name) {
		entries = append(entries, rootEntry{&roots.logs, legacy}, rootEntry{&roots.artifacts, legacy})
	}
	// The DAG state of the dag and root_dag scopes is kept by DAG name.
	for _, scope := range []dagrun.StateScope{dagrun.StateScopeDAG, dagrun.StateScopeRootDAG} {
		prefix := strings.TrimSuffix(store.DAGStateNamespaceIDPrefix(scope, name), "/")
		entries = append(entries, rootEntry{&roots.dagState, recordDirPath(strings.Split(prefix, "/")...)})
	}

	var paths []DAGStatePath
	for _, entry := range entries {
		// A year directory holds the artifacts kept by date.
		if entry.root == &roots.artifacts && legacyArtifactDirPattern.MatchString(entry.rel) {
			continue
		}
		if err := appendExisting(&paths, entry.root.From, entry.root.To, entry.rel); err != nil {
			return nil, err
		}
	}
	artifacts, err := datedArtifactPaths(from.ArtifactDir, to.ArtifactDir, name)
	if err != nil {
		return nil, err
	}
	paths = append(paths, artifacts...)

	// The records the API and the scheduler keep by the DAG's file name.
	fromSpecs, toSpecs := projectCollectionSpecs(from), projectCollectionSpecs(to)
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

// DAGRunPaths returns the run directories of the runs of the DAG called
// name in the stores on from, the default project's, that dest places in
// another project by the labels each run recorded, paired with their places
// in that project's stores, together with each run's directories of logs
// and artifacts in from's own directories.
func DAGRunPaths(
	ctx context.Context, cfg *config.Config, from config.ProjectPaths, name string,
	dest func(labels []string) (config.ProjectPaths, bool),
) ([]DAGStatePath, error) {
	runsRoot := filepath.Join(from.DAGRunsDir, filedagrun.DAGDirName(from.DAGRunsDir, name))
	runDirs, err := filepath.Glob(filepath.Join(runsRoot, "dag-runs", "*", "*", "*", filedagrun.DAGRunDirPrefix+"*"))
	if err != nil {
		return nil, err
	}
	mapper := runpath.New(
		runpath.Dirs{Log: from.LogDir, Artifact: from.ArtifactDir},
		runpath.Dirs{Log: cfg.Paths.LogDir, Artifact: cfg.Paths.ArtifactDir},
		true,
	)
	var paths []DAGStatePath
	for _, runDir := range runDirs {
		run, err := filedagrun.NewDAGRun(runDir)
		if err != nil {
			return nil, err
		}
		attempt, err := run.LatestAttempt(ctx, nil)
		if err != nil {
			// A run without a readable attempt stays in from.
			continue
		}
		status, err := attempt.ReadStatus(ctx)
		if err != nil {
			continue
		}
		to, ok := dest(status.Labels)
		if !ok {
			continue
		}
		rel, err := filepath.Rel(from.DAGRunsDir, runDir)
		if err != nil {
			return nil, err
		}
		paths = append(paths, DAGStatePath{From: runDir, To: filepath.Join(to.DAGRunsDir, rel)})
		if status.Log != "" {
			if err := appendWithin(&paths, from.LogDir, to.LogDir, filepath.Dir(mapper.ResolveLog(status.Log))); err != nil {
				return nil, err
			}
		}
		if status.ArchiveDir != "" {
			dir := mapper.ResolveArtifact(status.ArchiveDir)
			if err := appendWithin(&paths, from.ArtifactDir, to.ArtifactDir, dir); err != nil {
				return nil, err
			}
			if meta, ok := artifactpath.MetaPath(from.ArtifactDir, dir); ok {
				if err := appendWithin(&paths, from.ArtifactDir, to.ArtifactDir, meta); err != nil {
					return nil, err
				}
			}
		}
	}
	return paths, nil
}

// DAGStateRoots returns the directories of the stores on from that DAG
// state lies in, paired with those of the stores on to.
func DAGStateRoots(from, to config.ProjectPaths) []DAGStatePath {
	roots := stateRoots(from, to)
	return []DAGStatePath{
		roots.runs, roots.ephemeral, roots.work, roots.logs, roots.artifacts, roots.queue,
		roots.suspend, roots.slots, roots.dagState, roots.settings, roots.scheduler,
		roots.webhooks, roots.claims,
	}
}

// dagStateRoots holds the directories of the stores that DAG state lies
// in, each with its place in the stores of another project.
type dagStateRoots struct {
	runs, ephemeral, work, logs, artifacts, queue, suspend, slots DAGStatePath
	dagState, settings, scheduler, webhooks, claims               DAGStatePath
}

func stateRoots(from, to config.ProjectPaths) dagStateRoots {
	fromSpecs, toSpecs := projectCollectionSpecs(from), projectCollectionSpecs(to)
	collection := func(name string) DAGStatePath {
		return DAGStatePath{From: fromSpecs[name].dir, To: toSpecs[name].dir}
	}
	other := func(name string) DAGStatePath {
		return DAGStatePath{From: filepath.Join(from.OtherStateDir, name), To: filepath.Join(to.OtherStateDir, name)}
	}
	return dagStateRoots{
		runs:      DAGStatePath{From: from.DAGRunsDir, To: to.DAGRunsDir},
		ephemeral: other(ephemeralRunsDirName),
		work:      DAGStatePath{From: from.DAGRunWorkDir, To: to.DAGRunWorkDir},
		logs:      DAGStatePath{From: from.LogDir, To: to.LogDir},
		artifacts: DAGStatePath{From: from.ArtifactDir, To: to.ArtifactDir},
		queue:     collection(persis.CollectionQueue),
		suspend:   DAGStatePath{From: from.SuspendFlagsDir, To: to.SuspendFlagsDir},
		slots:     other(slotIndexDirName),
		dagState:  collection(persis.CollectionDAGState),
		settings:  collection(persis.CollectionDAGSettings),
		scheduler: collection(persis.CollectionSchedulerState),
		webhooks:  collection(persis.CollectionWebhooks),
		claims:    collection(persis.CollectionWebhookDAGClaims),
	}
}

// appendExisting adds rel in fromDir to paths, paired with rel in toDir,
// when it exists.
func appendExisting(paths *[]DAGStatePath, fromDir, toDir, rel string) error {
	found, err := pathExists(filepath.Join(fromDir, rel))
	if found {
		*paths = append(*paths, DAGStatePath{From: filepath.Join(fromDir, rel), To: filepath.Join(toDir, rel)})
	}
	return err
}

// appendWithin adds path to paths, paired with its place in toDir, when it
// exists and lies inside fromDir. A path outside, which a DAG set, stays.
func appendWithin(paths *[]DAGStatePath, fromDir, toDir, path string) error {
	rel, err := filepath.Rel(fromDir, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	if slices.ContainsFunc(*paths, func(p DAGStatePath) bool { return p.From == path }) {
		return nil
	}
	return appendExisting(paths, fromDir, toDir, rel)
}

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
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("read the artifact record %s: %w", filepath.Join(day, entry.Name()), err)
			}
			if record.Name != name {
				continue
			}
			for _, item := range []string{entry.Name(), runDir} {
				if err := appendExisting(&paths, fromDir, toDir, filepath.Join(rel, item)); err != nil {
					return nil, err
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
