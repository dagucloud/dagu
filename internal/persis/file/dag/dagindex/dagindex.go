// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/core/spec"
	"github.com/dagucloud/dagu/v2/internal/ir"
	indexv1 "github.com/dagucloud/dagu/v2/proto/index/v1"
	"google.golang.org/protobuf/proto"
)

const (
	// IndexFileName is the name of the DAG definition index file.
	IndexFileName = ".dag.index"
	// IndexVersion is the current index format version.
	IndexVersion = 2
)

// YAMLFileMeta holds stat metadata for a single YAML file.
type YAMLFileMeta struct {
	Name    string // Slash-normalized path relative to the DAG directory.
	Size    int64
	ModTime int64 // UnixNano
}

// SuspendFlags is the set of suspend flag filenames present in flagsBaseDir.
type SuspendFlags map[string]struct{}

// Load reads and validates the index against the current filesystem state.
// Returns nil if the index is missing, corrupt, version-mismatched, or stale.
func Load(indexPath string, yamlFiles []YAMLFileMeta, flags SuspendFlags) []*indexv1.DAGIndexEntry {
	data, err := fileutil.ReadFile(indexPath)
	if err != nil {
		return nil
	}

	var idx indexv1.DAGIndex
	if err := proto.Unmarshal(data, &idx); err != nil {
		return nil
	}

	if idx.Version != IndexVersion {
		return nil
	}

	if len(idx.Entries) != len(yamlFiles) {
		return nil
	}

	// Build lookup by file_path for O(n) comparison.
	entryMap := make(map[string]*indexv1.DAGIndexEntry, len(idx.Entries))
	for _, e := range idx.Entries {
		entryMap[e.FilePath] = e
	}

	for _, f := range yamlFiles {
		e, ok := entryMap[f.Name]
		if !ok {
			return nil
		}
		if e.FileSize != f.Size || e.ModTime != f.ModTime {
			return nil
		}
	}

	// Validate suspend flags.
	for _, e := range idx.Entries {
		_, flagged := flags[SuspendFlagName(entryFileName(e.FilePath))]
		if e.Suspended != flagged {
			return nil
		}
	}

	return idx.Entries
}

// Build constructs a fresh index by loading every YAML file with metadata-only semantics.
func Build(
	ctx context.Context,
	dagDir string,
	yamlFiles []YAMLFileMeta,
	flags SuspendFlags,
	loadOpts ...spec.LoadOption,
) *indexv1.DAGIndex {
	idx := &indexv1.DAGIndex{
		Version:     IndexVersion,
		BuiltAtUnix: time.Now().Unix(),
		Entries:     make([]*indexv1.DAGIndexEntry, 0, len(yamlFiles)),
	}

	for _, f := range yamlFiles {
		if ctx.Err() != nil {
			break
		}

		entry := &indexv1.DAGIndexEntry{
			FilePath: f.Name,
			FileSize: f.Size,
			ModTime:  f.ModTime,
		}
		buildEntry(ctx, dagDir, entry, flags, loadOpts...)
		idx.Entries = append(idx.Entries, entry)
	}

	return idx
}

// buildEntry loads one DAG file and fills the rest of a freshly allocated entry,
// recording why the file could not be read when that happens.
func buildEntry(
	ctx context.Context,
	dagDir string,
	entry *indexv1.DAGIndexEntry,
	flags SuspendFlags,
	loadOpts ...spec.LoadOption,
) {
	opts := make([]spec.LoadOption, 0, len(loadOpts)+4)
	opts = append(opts, loadOpts...)
	opts = append(opts,
		spec.OnlyMetadata(),
		spec.WithoutEval(),
		spec.SkipSchemaValidation(),
		spec.WithAllowBuildErrors(),
	)

	dag, err := spec.Load(ctx, filepath.Join(dagDir, filepath.FromSlash(entry.FilePath)), opts...)
	if err != nil {
		base := filepath.Base(filepath.FromSlash(entry.FilePath))
		entry.Name = strings.TrimSuffix(base, filepath.Ext(base))
		entry.LoadError = err.Error()
		return
	}

	entry.Name = dag.Name
	entry.Group = dag.Group
	entry.Description = dag.Description
	entry.Labels = labelsToStrings(dag.Labels)
	entry.Schedule = scheduleToString(dag.Schedule)

	if len(dag.BuildErrors) > 0 {
		entry.LoadError = joinErrors(dag.BuildErrors)
	}

	_, flagged := flags[SuspendFlagName(entryFileName(entry.FilePath))]
	entry.Suspended = flagged
}

// RefreshFailures re-reads the files whose cached entry records a load error and
// reports whether any of them changed.
//
// A cached success stays valid as long as the file is untouched, but a cached
// failure does not: the error describes the parser that produced it, so a DAG
// using syntax a newer binary understands would keep showing the old error until
// its file happened to change.
func RefreshFailures(
	ctx context.Context,
	dagDir string,
	entries []*indexv1.DAGIndexEntry,
	flags SuspendFlags,
	loadOpts ...spec.LoadOption,
) bool {
	var changed bool
	for i, entry := range entries {
		if entry.LoadError == "" {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		refreshed := &indexv1.DAGIndexEntry{
			FilePath: entry.FilePath,
			FileSize: entry.FileSize,
			ModTime:  entry.ModTime,
		}
		buildEntry(ctx, dagDir, refreshed, flags, loadOpts...)
		if proto.Equal(entry, refreshed) {
			continue
		}
		entries[i] = refreshed
		changed = true
	}
	return changed
}

// NewIndex wraps entries in an index ready to be written.
func NewIndex(entries []*indexv1.DAGIndexEntry) *indexv1.DAGIndex {
	return &indexv1.DAGIndex{
		Version:     IndexVersion,
		BuiltAtUnix: time.Now().Unix(),
		Entries:     entries,
	}
}

// Write atomically writes the index to disk.
func Write(indexPath string, idx *indexv1.DAGIndex) error {
	data, err := proto.Marshal(idx)
	if err != nil {
		return fmt.Errorf("failed to marshal DAG index: %w", err)
	}
	return fileutil.WriteFileAtomic(indexPath, data, 0600)
}

// DAGFromEntry reconstructs a minimal ir.DAG from an index entry.
// The returned DAG is suitable for List/LabelList operations.
func DAGFromEntry(entry *indexv1.DAGIndexEntry, baseDir string) *ir.DAG {
	dag := &ir.DAG{
		Name:        entry.Name,
		Location:    filepath.Join(baseDir, filepath.FromSlash(entry.FilePath)),
		Group:       entry.Group,
		Description: entry.Description,
		Labels:      ir.NewLabels(entry.Labels),
	}

	if entry.LoadError != "" {
		dag.BuildErrors = []error{errors.New(entry.LoadError)}
	}

	if entry.Schedule != "" {
		dag.Schedule = parseScheduleExpressions(entry.Schedule)
	}

	return dag
}

// SuspendFlagName returns the flag filename for a DAG name.
func SuspendFlagName(dagName string) string {
	return fileutil.NormalizeFilename(dagName, "-") + ".suspend"
}

func entryFileName(filePath string) string {
	base := filepath.Base(filepath.FromSlash(filePath))
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func labelsToStrings(labels ir.Labels) []string {
	if len(labels) == 0 {
		return nil
	}
	strs := make([]string, len(labels))
	for i, t := range labels {
		strs[i] = t.String()
	}
	return strs
}

func scheduleToString(schedules []ir.Schedule) string {
	if len(schedules) == 0 {
		return ""
	}
	data, err := json.Marshal(schedules)
	if err != nil {
		return ""
	}
	return string(data)
}

func parseScheduleExpressions(s string) []ir.Schedule {
	var schedules []ir.Schedule
	if err := json.Unmarshal([]byte(s), &schedules); err == nil {
		return schedules
	}

	parts := strings.SplitSeq(s, "; ")
	for expr := range parts {
		expr = strings.TrimSpace(expr)
		if expr == "" {
			continue
		}
		if sched, err := ir.NewCronSchedule(expr); err == nil {
			schedules = append(schedules, sched)
		} else {
			schedules = append(schedules, ir.Schedule{Expression: expr})
		}
	}
	return schedules
}

func joinErrors(errs []error) string {
	strs := make([]string, len(errs))
	for i, e := range errs {
		strs[i] = e.Error()
	}
	return strings.Join(strs, "; ")
}
