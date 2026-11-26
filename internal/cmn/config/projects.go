// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

// projectsSwitchKey is the undocumented key that turns projects on.
const projectsSwitchKey = "experimental_projects"

// Project layout with projects on:
//
//	<projects_dir>/<name>/          DAG directory (paths.dags_dir for default)
//	<data_dir>/projects/<name>/     state directory
//	  logs/ artifacts/ runs/        unless the matching path is set
//	  settings/                     secrets, profiles, routes, policies, ...
//	  state/                        the project's other stores, among them:
//	    dag-run-work/               unless paths.dag_run_work_dir is set
//	    events/                     whatever paths.event_store_dir says
//	    dag-state/ queue/ proc/ suspend/ views/
//	<log_dir>/.projects/<name>/     when paths.log_dir is set; likewise for
//	                                artifact_dir, dag_runs_dir, dag_run_work_dir
const (
	defaultProjectName       = "default"
	projectStatesDirName     = "projects"
	setApartProjectsDirName  = ".projects"
	projectLogsDirName       = "logs"
	projectArtifactsDirName  = "artifacts"
	projectRunsDirName       = "runs"
	projectSettingsDirName   = "settings"
	projectOtherStateDirName = "state"
	projectRunWorkDirName    = "dag-run-work"
	projectEventsDirName     = "events"
	projectDAGStateDirName   = "dag-state"
	projectQueueDirName      = "queue"
	projectProcDirName       = "proc"
	projectSuspendDirName    = "suspend"
	projectViewsDirName      = "views"
	projectWebhooksDirName   = "webhooks"
)

// ProjectPaths holds the directories of one project.
type ProjectPaths struct {
	// DAGsDir holds the project's DAG files, base config, and wiki.
	DAGsDir string
	// StateDir holds the project's state.
	StateDir      string
	LogDir        string
	ArtifactDir   string
	DAGRunsDir    string
	DAGRunWorkDir string
	// EventStoreDir holds the project's run events.
	EventStoreDir string
	// SettingsDir holds the stores of the project's settings, such as its
	// secrets and profiles, each in a directory of its own.
	SettingsDir string
	// OtherStateDir holds the project's stores that no other field places,
	// each in a directory of its own.
	OtherStateDir   string
	DAGStateDir     string
	QueueDir        string
	ProcDir         string
	SuspendFlagsDir string
	// LegacySuspendFlagsDir is where an older version kept suspend flags.
	// It is empty while projects are on.
	LegacySuspendFlagsDir string
	ViewsDir              string
	// WebhooksDir holds the project's DAG webhooks.
	WebhooksDir string
	// RecordDirs lists the state directories that record the project's ID
	// and name: StateDir, then each directory above that a set path places
	// outside it. It is empty while projects are off.
	RecordDirs []string
}

// ProjectPaths returns the directories of the project called name, spelled
// as the registry stores it. While projects are off, only the default
// project exists; its directories are the ones v2 uses, and its state
// directory is paths.data_dir.
func (c *Config) ProjectPaths(name string) (ProjectPaths, error) {
	isDefault := strings.EqualFold(name, defaultProjectName)
	if !c.ProjectsEnabled {
		if !isDefault {
			return ProjectPaths{}, fmt.Errorf("project %q: projects are off", name)
		}
		return ProjectPaths{
			DAGsDir:               c.Paths.DAGsDir,
			StateDir:              c.Paths.DataDir,
			LogDir:                c.Paths.LogDir,
			ArtifactDir:           c.Paths.ArtifactDir,
			DAGRunsDir:            c.Paths.DAGRunsDir,
			DAGRunWorkDir:         c.Paths.DAGRunWorkDir,
			EventStoreDir:         c.Paths.EventStoreDir,
			SettingsDir:           c.Paths.DataDir,
			OtherStateDir:         c.Paths.DataDir,
			DAGStateDir:           c.Paths.DAGStateDir,
			QueueDir:              c.Paths.QueueDir,
			ProcDir:               c.Paths.ProcDir,
			SuspendFlagsDir:       c.Paths.SuspendFlagsDir,
			LegacySuspendFlagsDir: c.Paths.SuspendFlagsDirLegacy,
			ViewsDir:              c.Paths.ViewsDir,
			WebhooksDir:           c.Paths.WebhooksDir,
		}, nil
	}

	dagsDir := c.Paths.DAGsDir
	if isDefault {
		name = defaultProjectName
	} else {
		if !isPathElement(name) {
			return ProjectPaths{}, fmt.Errorf("project name %q is not a directory name", name)
		}
		dagsDir = filepath.Join(c.Paths.ProjectsDir, name)
	}

	stateDir := filepath.Join(c.Paths.DataDir, projectStatesDirName, name)
	otherDir := filepath.Join(stateDir, projectOtherStateDirName)
	// The paths settings that place these stores only tell migration where
	// v2 kept their data.
	paths := ProjectPaths{
		DAGsDir:         dagsDir,
		StateDir:        stateDir,
		EventStoreDir:   filepath.Join(otherDir, projectEventsDirName),
		SettingsDir:     filepath.Join(stateDir, projectSettingsDirName),
		OtherStateDir:   otherDir,
		DAGStateDir:     filepath.Join(otherDir, projectDAGStateDirName),
		QueueDir:        filepath.Join(otherDir, projectQueueDirName),
		ProcDir:         filepath.Join(otherDir, projectProcDirName),
		SuspendFlagsDir: filepath.Join(otherDir, projectSuspendDirName),
		ViewsDir:        filepath.Join(otherDir, projectViewsDirName),
		WebhooksDir:     filepath.Join(stateDir, projectSettingsDirName, projectWebhooksDirName),
		RecordDirs:      []string{stateDir},
	}
	// place returns inState, or <root>/.projects/<name> when the
	// configuration sets root.
	place := func(explicit bool, root, inState string) string {
		if !explicit {
			return inState
		}
		dir := filepath.Join(root, setApartProjectsDirName, name)
		if !slices.Contains(paths.RecordDirs, dir) {
			paths.RecordDirs = append(paths.RecordDirs, dir)
		}
		return dir
	}
	explicit := c.Paths.Explicit
	paths.LogDir = place(explicit.LogDir, c.Paths.LogDir,
		filepath.Join(stateDir, projectLogsDirName))
	paths.ArtifactDir = place(explicit.ArtifactDir, c.Paths.ArtifactDir,
		filepath.Join(stateDir, projectArtifactsDirName))
	paths.DAGRunsDir = place(explicit.DAGRunsDir, c.Paths.DAGRunsDir,
		filepath.Join(stateDir, projectRunsDirName))
	paths.DAGRunWorkDir = place(explicit.DAGRunWorkDir, c.Paths.DAGRunWorkDir,
		filepath.Join(otherDir, projectRunWorkDirName))
	return paths, nil
}

// isPathElement reports whether name is a single directory name that stays
// inside its parent on every platform. The registry enforces the full name
// rules.
func isPathElement(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\:`)
}

// DiscoverySkipRoots returns paths.dags_dir, paths.projects_dir, and
// paths.alt_dags_dir. DAG discovery skips each of them that it finds below
// the directory it scans, so each DAG file belongs to one project or to the
// library. It is empty while projects are off.
func (c *Config) DiscoverySkipRoots() []string {
	if !c.ProjectsEnabled {
		return nil
	}
	var roots []string
	for _, dir := range []string{c.Paths.DAGsDir, c.Paths.ProjectsDir, c.Paths.AltDAGsDir} {
		if dir != "" {
			roots = append(roots, dir)
		}
	}
	return roots
}

// ProjectWarnings returns the warnings that a process serving projects logs
// at startup. It is empty while projects are off.
func (c *Config) ProjectWarnings() []string {
	if !c.ProjectsEnabled {
		return nil
	}

	var warnings []string
	if c.Paths.ProjectsDir != "" && c.Paths.DAGsDir != "" {
		same, err := fileutil.SameFileSystem(c.Paths.ProjectsDir, c.Paths.DAGsDir)
		switch {
		case err != nil:
			warnings = append(warnings, fmt.Sprintf(
				"cannot tell whether paths.projects_dir and paths.dags_dir share a file system: %v", err))
		case !same:
			warnings = append(warnings, fmt.Sprintf(
				"paths.projects_dir %q and paths.dags_dir %q are on different file systems; a container that mounts only the DAGs directory loses every project's DAG files when it is recreated",
				c.Paths.ProjectsDir, c.Paths.DAGsDir))
		}
	}

	for _, setting := range []struct {
		key string
		set bool
	}{
		{"paths.queue_dir", c.Paths.Explicit.QueueDir},
		{"paths.suspend_flags_dir", c.Paths.Explicit.SuspendFlagsDir},
		{"paths.proc_dir", c.Paths.Explicit.ProcDir},
	} {
		if setting.set {
			warnings = append(warnings, fmt.Sprintf(
				"%s only tells migration where v2 kept this data; projects do not use it", setting.key))
		}
	}
	return warnings
}
