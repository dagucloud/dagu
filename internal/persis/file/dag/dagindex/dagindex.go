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

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
	indexv1 "github.com/dagucloud/dagu/proto/index/v1"
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
	Name    string // filename, e.g. "my-dag.yaml"
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
		_, flagged := flags[SuspendFlagName(e.Name)]
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

		filePath := filepath.Join(dagDir, f.Name)
		entry := &indexv1.DAGIndexEntry{
			FilePath: f.Name,
			FileSize: f.Size,
			ModTime:  f.ModTime,
		}

		opts := make([]spec.LoadOption, 0, len(loadOpts)+4)
		opts = append(opts, loadOpts...)
		opts = append(opts,
			spec.OnlyMetadata(),
			spec.WithoutEval(),
			spec.SkipSchemaValidation(),
			spec.WithAllowBuildErrors(),
		)

		dag, err := spec.Load(ctx, filePath, opts...)
		if err != nil {
			entry.Name = strings.TrimSuffix(f.Name, filepath.Ext(f.Name))
			entry.LoadError = err.Error()
			idx.Entries = append(idx.Entries, entry)
			continue
		}

		entry.Name = dag.Name
		entry.Group = dag.Group
		entry.Description = dag.Description
		entry.Labels = labelsToStrings(dag.Labels)
		entry.Schedule = scheduleToString(dag.Schedule)

		if len(dag.BuildErrors) > 0 {
			entry.LoadError = joinErrors(dag.BuildErrors)
		}

		_, flagged := flags[SuspendFlagName(dag.Name)]
		entry.Suspended = flagged

		idx.Entries = append(idx.Entries, entry)
	}

	return idx
}

// Write atomically writes the index to disk.
func Write(indexPath string, idx *indexv1.DAGIndex) error {
	data, err := proto.Marshal(idx)
	if err != nil {
		return fmt.Errorf("failed to marshal DAG index: %w", err)
	}
	return fileutil.WriteFileAtomic(indexPath, data, 0600)
}

// DAGFromEntry reconstructs a minimal core.DAG from an index entry.
// The returned DAG is suitable for List/LabelList operations.
func DAGFromEntry(entry *indexv1.DAGIndexEntry, baseDir string) *core.DAG {
	dag := &core.DAG{
		Name:        entry.Name,
		Location:    filepath.Join(baseDir, entry.FilePath),
		Group:       entry.Group,
		Description: entry.Description,
		Labels:      core.NewLabels(entry.Labels),
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

func labelsToStrings(labels core.Labels) []string {
	if len(labels) == 0 {
		return nil
	}
	strs := make([]string, len(labels))
	for i, t := range labels {
		strs[i] = t.String()
	}
	return strs
}

func scheduleToString(schedules []core.Schedule) string {
	if len(schedules) == 0 {
		return ""
	}
	data, err := json.Marshal(schedules)
	if err != nil {
		return ""
	}
	return string(data)
}

func parseScheduleExpressions(s string) []core.Schedule {
	var schedules []core.Schedule
	if err := json.Unmarshal([]byte(s), &schedules); err == nil {
		return schedules
	}

	parts := strings.SplitSeq(s, "; ")
	for expr := range parts {
		expr = strings.TrimSpace(expr)
		if expr == "" {
			continue
		}
		if sched, err := core.NewCronSchedule(expr); err == nil {
			schedules = append(schedules, sched)
		} else {
			schedules = append(schedules, core.Schedule{Expression: expr})
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
