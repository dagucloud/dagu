// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	filebackfill "github.com/dagucloud/dagu/v2/internal/persis/file/backfill"
	"github.com/dagucloud/dagu/v2/internal/project"
)

// V2StateDir is a directory where v2 keeps state that a store now keeps in
// Dir.
type V2StateDir struct {
	V2  string
	Dir string
	// Holds, when set, reports whether an entry of V2 is state that belongs
	// in Dir. The other entries are not this store's.
	Holds func(entry fs.DirEntry) bool
}

// DefaultProjectV2StateDirs returns each directory, or file, where v2 keeps
// state of the default project, paired with where its store keeps it while
// projects are on. It is empty while projects are off, since the stores then
// keep the v2 directories. The items of every queue, those defined in
// config.yaml included, stay with the default project's queue.
func DefaultProjectV2StateDirs(cfg *config.Config) []V2StateDir {
	if !cfg.ProjectsEnabled {
		return nil
	}
	v2 := v2ProjectPaths(cfg.Paths)
	// The default project always resolves.
	to, _ := ProjectStorePaths(cfg, project.DefaultName)

	var dirs []V2StateDir
	add := func(from, dir string, holds func(fs.DirEntry) bool) {
		if from != "" && dir != "" && filepath.Clean(from) != filepath.Clean(dir) {
			dirs = append(dirs, V2StateDir{V2: from, Dir: dir, Holds: holds})
		}
	}
	add(v2.DAGRunsDir, to.DAGRunsDir, nil)
	add(EphemeralDAGRunsDir(v2.DAGRunsDir), filepath.Join(to.OtherStateDir, ephemeralRunsDirName), nil)
	add(v2.DAGRunWorkDir, to.DAGRunWorkDir, nil)
	add(v2.LogDir, to.LogDir, nil)
	add(v2.ArtifactDir, to.ArtifactDir, nil)
	add(v2.EventStoreDir, to.EventStoreDir, nil)
	add(v2.ProcDir, to.ProcDir, nil)
	// With a legacy directory, the flags of both directories move one by
	// one into the same directory.
	var primaryFlags func(fs.DirEntry) bool
	if v2.LegacySuspendFlagsDir != "" {
		primaryFlags = func(fs.DirEntry) bool { return true }
	}
	add(v2.SuspendFlagsDir, to.SuspendFlagsDir, primaryFlags)
	// A legacy flag that v2 copied into the primary directory is left
	// behind; projects read no legacy directory.
	add(v2.LegacySuspendFlagsDir, to.SuspendFlagsDir, func(entry fs.DirEntry) bool {
		_, err := os.Lstat(filepath.Join(v2.SuspendFlagsDir, entry.Name()))
		return !entry.IsDir() && strings.HasSuffix(entry.Name(), ".suspend") && err != nil
	})
	add(filepath.Join(v2.OtherStateDir, slotIndexDirName), filepath.Join(to.OtherStateDir, slotIndexDirName), nil)
	add(filebackfill.Dir(v2.OtherStateDir), filebackfill.Dir(to.OtherStateDir), nil)
	add(filepath.Join(v2.OtherStateDir, materializationsDirName), filepath.Join(to.OtherStateDir, materializationsDirName), nil)
	add(dagIndexDir(v2.OtherStateDir), dagIndexDir(to.OtherStateDir), nil)
	for _, dir := range []string{notificationsDirName, incidentsDirName} {
		add(filepath.Join(v2.OtherStateDir, dir, monitorStateFileName), filepath.Join(to.OtherStateDir, dir, monitorStateFileName), nil)
	}

	v2Specs, toSpecs := projectCollectionSpecs(v2), projectCollectionSpecs(to)
	for _, name := range slices.Sorted(maps.Keys(v2Specs)) {
		spec := v2Specs[name]
		var holds func(fs.DirEntry) bool
		if len(spec.idPrefixes) > 0 {
			// The directory also holds server-wide records.
			holds = holdsRecords(spec.idPrefixes)
		}
		add(spec.dir, toSpecs[name].dir, holds)
	}
	return dirs
}

// holdsRecords reports whether an entry of a collection directory holds
// records whose IDs begin with one of prefixes.
func holdsRecords(prefixes []string) func(fs.DirEntry) bool {
	return func(entry fs.DirEntry) bool {
		var id string
		switch {
		case entry.IsDir():
			id = segmentFromName(entry.Name(), true) + "/"
		case strings.HasSuffix(entry.Name(), ".json"):
			id = relPathToID(entry.Name())
		default:
			return false
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix(id, prefix) {
				return true
			}
		}
		return false
	}
}
