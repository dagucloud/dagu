// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	indexv1 "github.com/dagucloud/dagu/v2/proto/index/v1"
	"google.golang.org/protobuf/proto"
)

const (
	// IndexFileName is the name of the DAG definition index file.
	IndexFileName = ".dag.index"
	// IndexVersion is the current index format version.
	IndexVersion = 8
)

// YAMLFileMeta holds stat metadata for a single YAML file.
type YAMLFileMeta struct {
	Name     string // Slash-normalized path relative to the DAG directory.
	LoadPath string // Canonical path used to read the current file.
	Size     int64
	ModTime  int64 // UnixNano
}

// SuspendFlags is the set of suspend flag filenames present in flagsBaseDir.
type SuspendFlags map[string]struct{}

// ReadEntries returns the entries of the index without checking them against
// the DAG files. It reports false if the index is missing, corrupt, or of
// another version.
func ReadEntries(indexPath string) ([]*indexv1.DAGIndexEntry, bool) {
	data, err := fileutil.ReadFile(indexPath)
	if err != nil {
		return nil, false
	}
	entries, _, ok := Decode(data)
	return entries, ok
}

// Decode returns the entries of a marshaled index and when the scan that
// checked them against the DAG files began. It reports false if data is not
// an index of this version.
func Decode(data []byte) ([]*indexv1.DAGIndexEntry, time.Time, bool) {
	var idx indexv1.DAGIndex
	if err := proto.Unmarshal(data, &idx); err != nil {
		return nil, time.Time{}, false
	}
	if idx.Version != IndexVersion {
		return nil, time.Time{}, false
	}
	// An older Dagu recorded when it built the index, after its scan.
	return idx.Entries, time.Unix(idx.BuiltAtUnix, 0), true
}

// Refresh returns an entry for each of yamlFiles, in order. It keeps the
// entry of saved for a file whose size, modification time, and load path are
// unchanged, with the suspension that flags record, and loads every other
// file. It also loads a file whose saved entry records a load error, since
// the error may come from an older parser, or that was modified after
// trustBefore, since a later write may have kept its size and modification
// time. Refresh reports whether the entries differ from saved. It returns the
// error of ctx when ctx ends before every file that needs a load is loaded.
func Refresh(
	ctx context.Context,
	dagDir string,
	saved []*indexv1.DAGIndexEntry,
	trustBefore time.Time,
	yamlFiles []YAMLFileMeta,
	flags SuspendFlags,
	loadOpts ...spec.LoadOption,
) ([]*indexv1.DAGIndexEntry, bool, error) {
	savedByPath := make(map[string]*indexv1.DAGIndexEntry, len(saved))
	for _, entry := range saved {
		savedByPath[entry.FilePath] = entry
	}

	entries := make([]*indexv1.DAGIndexEntry, 0, len(yamlFiles))
	changed := len(saved) != len(yamlFiles)
	for _, file := range yamlFiles {
		old := savedByPath[file.Name]
		if old != nil && old.LoadError == "" && !modifiedAfter(old, trustBefore) && describes(old, file) {
			entry := withSuspension(old, flags)
			changed = changed || entry != old
			entries = append(entries, entry)
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		entry := newEntry(ctx, dagDir, file, flags, loadOpts...)
		// A load that the end of ctx cut short records that as a load error.
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		changed = changed || old == nil || !proto.Equal(old, entry)
		entries = append(entries, entry)
	}
	return entries, changed, nil
}

// modifiedAfter reports whether the file of entry was modified after t.
func modifiedAfter(entry *indexv1.DAGIndexEntry, t time.Time) bool {
	return time.Unix(0, entry.ModTime).After(t)
}

// describes reports whether entry was made from file as it is now.
func describes(entry *indexv1.DAGIndexEntry, file YAMLFileMeta) bool {
	return entry.FileSize == file.Size && entry.ModTime == file.ModTime && entry.LoadPath == file.LoadPath
}

// withSuspension returns entry with the suspension that flags record, as a
// copy when that differs.
func withSuspension(entry *indexv1.DAGIndexEntry, flags SuspendFlags) *indexv1.DAGIndexEntry {
	suspended := isSuspended(entry.FilePath, flags)
	if entry.Suspended == suspended {
		return entry
	}
	updated := proto.CloneOf(entry)
	updated.Suspended = suspended
	return updated
}

func isSuspended(filePath string, flags SuspendFlags) bool {
	_, flagged := flags[SuspendFlagName(entryFileName(filePath))]
	return flagged
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

		idx.Entries = append(idx.Entries, newEntry(ctx, dagDir, f, flags, loadOpts...))
	}

	return idx
}

// newEntry loads one DAG file into a new entry.
func newEntry(
	ctx context.Context,
	dagDir string,
	file YAMLFileMeta,
	flags SuspendFlags,
	loadOpts ...spec.LoadOption,
) *indexv1.DAGIndexEntry {
	entry := &indexv1.DAGIndexEntry{
		FilePath: file.Name,
		FileSize: file.Size,
		ModTime:  file.ModTime,
		LoadPath: file.LoadPath,
	}
	buildEntry(ctx, yamlFilePath(dagDir, file), entry, flags, loadOpts...)
	return entry
}

// buildEntry loads one DAG file and fills the rest of a freshly allocated entry,
// recording why the file could not be read when that happens.
func buildEntry(
	ctx context.Context,
	filePath string,
	entry *indexv1.DAGIndexEntry,
	flags SuspendFlags,
	loadOpts ...spec.LoadOption,
) {
	opts := make([]spec.LoadOption, 0, len(loadOpts)+5)
	opts = append(opts, loadOpts...)
	opts = append(opts,
		spec.WithDefaultName(entryFileName(entry.FilePath)),
		spec.OnlyMetadata(),
		spec.WithoutEval(),
		spec.SkipSchemaValidation(),
		spec.WithAllowBuildErrors(),
	)

	dag, err := spec.Load(ctx, filePath, opts...)
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
	entry.Queue = dag.Queue
	if dag.RestartPolicy.Enabled() {
		entry.RestartPolicy = string(dag.RestartPolicy)
		entry.RestartWaitSec = int64(dag.RestartWait / time.Second)
	}

	if len(dag.BuildErrors) > 0 {
		entry.LoadError = joinErrors(dag.BuildErrors)
	}

	entry.Suspended = isSuspended(entry.FilePath, flags)
}

func yamlFilePath(dagDir string, file YAMLFileMeta) string {
	if file.LoadPath != "" {
		return file.LoadPath
	}
	return filepath.Join(dagDir, filepath.FromSlash(file.Name))
}

// Encode marshals an index of entries that a scan begun at scannedAt checked
// against the DAG files, ready to be written.
func Encode(entries []*indexv1.DAGIndexEntry, scannedAt time.Time) ([]byte, error) {
	data, err := proto.Marshal(&indexv1.DAGIndex{
		Version:     IndexVersion,
		BuiltAtUnix: scannedAt.Unix(),
		Entries:     entries,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal DAG index: %w", err)
	}
	return data, nil
}

// Write atomically writes a marshaled index to disk, creating its directory if
// needed.
func Write(indexPath string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(indexPath), 0750); err != nil {
		return fmt.Errorf("failed to create DAG index directory: %w", err)
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
		Queue:       entry.Queue,
	}

	if entry.LoadError != "" {
		dag.BuildErrors = []error{errors.New(entry.LoadError)}
	}

	if entry.Schedule != "" {
		dag.Schedule = parseScheduleExpressions(entry.Schedule)
	}

	if policy, err := ir.ParseRestartPolicy(entry.RestartPolicy); err == nil {
		dag.RestartPolicy = policy
		dag.RestartWait = time.Duration(entry.RestartWaitSec) * time.Second
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
