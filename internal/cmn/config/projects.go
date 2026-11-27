// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// projectsSwitchKey is the undocumented key that turns projects on.
const projectsSwitchKey = "experimental_projects"

// Project layout with projects on:
//
//	<projects_dir>/<name>/          DAG directory (paths.dags_dir for default)
//	  base.yaml                     base config (paths.base_config for default)
//	<data_dir>/projects/<name>/     state directory
//	  logs/ artifacts/ runs/        unless the matching path is set
//	  settings/                     secrets, profiles, routes, policies, ...
//	  state/                        the project's other stores, among them:
//	    dag-run-work/               unless paths.dag_run_work_dir is set
//	    events/                     whatever paths.event_store_dir says
//	    dag-state/ queue/ proc/ suspend/ views/
//	  library/                      the library DAGs that run in the project:
//	    logs/ artifacts/ runs/ state/   laid out as the project's own
//	<log_dir>/.projects/<name>/     when paths.log_dir is set; likewise for
//	                                artifact_dir, dag_runs_dir, dag_run_work_dir
//	<log_dir>/.projects/<name>.library/   the library DAGs' logs then
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
	projectLibraryDirName    = "library"
	// setApartLibrarySuffix names a project's library directory under a
	// path set apart. A project name has no ".", so no project takes it.
	setApartLibrarySuffix = ".library"
	// projectBaseConfigFileName is a file of the DAG directory, not a DAG.
	projectBaseConfigFileName = "base.yaml"
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

	name, err := projectDirName(name)
	if err != nil {
		return ProjectPaths{}, err
	}
	dagsDir := c.Paths.DAGsDir
	if !isDefault {
		dagsDir = filepath.Join(c.Paths.ProjectsDir, name)
	}

	stateDir := filepath.Join(c.Paths.DataDir, projectStatesDirName, name)
	paths := c.statePaths(stateDir, name)
	paths.DAGsDir = dagsDir
	paths.RecordDirs = []string{stateDir}
	// The library directories set apart are the project's too, so each
	// records the project as well.
	for _, setApart := range []string{name, name + setApartLibrarySuffix} {
		for _, dir := range c.setApartDirs(setApart) {
			if !slices.Contains(paths.RecordDirs, dir) {
				paths.RecordDirs = append(paths.RecordDirs, dir)
			}
		}
	}
	return paths, nil
}

// LibraryPaths returns the directories where the project called name keeps
// the runs, logs, artifacts, queues, and DAG state of the library DAGs it
// runs: library/ in its state directory, and .projects/<name>.library/ under
// each path set apart. DAGsDir, SettingsDir, WebhooksDir, and EventStoreDir
// are the project's, and RecordDirs is empty. It fails while projects are
// off.
func (c *Config) LibraryPaths(name string) (ProjectPaths, error) {
	if !c.ProjectsEnabled {
		return ProjectPaths{}, fmt.Errorf("project %q: projects are off", name)
	}
	project, err := c.ProjectPaths(name)
	if err != nil {
		return ProjectPaths{}, err
	}
	name, _ = projectDirName(name)
	paths := c.statePaths(filepath.Join(project.StateDir, projectLibraryDirName), name+setApartLibrarySuffix)
	paths.DAGsDir = project.DAGsDir
	paths.SettingsDir = project.SettingsDir
	paths.WebhooksDir = project.WebhooksDir
	paths.EventStoreDir = project.EventStoreDir
	return paths, nil
}

// statePaths lays out the stores of a project's state directory stateDir.
// A path that the configuration sets apart goes to <root>/.projects/<setApart>
// instead.
func (c *Config) statePaths(stateDir, setApart string) ProjectPaths {
	otherDir := filepath.Join(stateDir, projectOtherStateDirName)
	// The paths settings that place these stores only tell migration where
	// v2 kept their data.
	paths := ProjectPaths{
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
	}
	place := func(explicit bool, root, inState string) string {
		if !explicit {
			return inState
		}
		return filepath.Join(root, setApartProjectsDirName, setApart)
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
	return paths
}

// setApartDirs returns <root>/.projects/<setApart> for each root that the
// configuration sets apart.
func (c *Config) setApartDirs(setApart string) []string {
	var dirs []string
	explicit := c.Paths.Explicit
	for _, root := range []struct {
		set bool
		dir string
	}{
		{explicit.LogDir, c.Paths.LogDir},
		{explicit.ArtifactDir, c.Paths.ArtifactDir},
		{explicit.DAGRunsDir, c.Paths.DAGRunsDir},
		{explicit.DAGRunWorkDir, c.Paths.DAGRunWorkDir},
	} {
		if root.set {
			dirs = append(dirs, filepath.Join(root.dir, setApartProjectsDirName, setApart))
		}
	}
	return dirs
}

// projectDirName returns the name of the project called name as its
// directories spell it.
func projectDirName(name string) (string, error) {
	if strings.EqualFold(name, defaultProjectName) {
		return defaultProjectName, nil
	}
	if !isPathElement(name) {
		return "", fmt.Errorf("project name %q is not a directory name", name)
	}
	return name, nil
}

// isPathElement reports whether name is a single directory name that stays
// inside its parent on every platform. The registry enforces the full name
// rules.
func isPathElement(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\:`)
}

// ProjectBaseConfig returns the base config file of the project called
// name, "" naming the default project: paths.base_config for the default
// project, and base.yaml in the DAG directory of any other. A project whose
// file does not exist has no base config.
func (c *Config) ProjectBaseConfig(name string) (string, error) {
	if name == "" || strings.EqualFold(name, defaultProjectName) {
		return c.Paths.BaseConfig, nil
	}
	paths, err := c.ProjectPaths(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(paths.DAGsDir, projectBaseConfigFileName), nil
}

// WorkspaceBaseConfigDir returns the directory of the workspace base
// configs that a DAG's workspace label layers over its base config. It is
// empty while projects are on: a label then adds no layer.
func (c *Config) WorkspaceBaseConfigDir() string {
	if c.ProjectsEnabled {
		return ""
	}
	return workspace.BaseConfigDir(c.Paths.DAGsDir)
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
