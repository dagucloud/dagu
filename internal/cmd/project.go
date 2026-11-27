// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

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
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/spf13/cobra"
)

// projectFlag addresses a project. It stays hidden while projects are
// experimental.
var projectFlag = commandLineFlag{
	name:   "project",
	usage:  "Project to address (default: $DAGU_PROJECT, else the project of the current directory, else default)",
	hidden: true,
}

// errProjectFlagUnknown is how a command refuses --project while projects
// are off, as it did before the flag existed.
var errProjectFlagUnknown = errors.New("unknown flag: --project")

// checkProjectFlag refuses --project for a command that serves every
// project or that runs on a worker. While projects are off, it accepts only
// the default project, which a parent started while projects were on still
// names, and refuses any other as the unknown flag it was.
func checkProjectFlag(cmd *cobra.Command, cfg *config.Config) error {
	flag := cmd.Flags().Lookup(projectFlag.name)
	if flag == nil || !flag.Changed {
		return nil
	}
	if !cfg.ProjectsEnabled {
		if strings.EqualFold(strings.TrimSpace(flag.Value.String()), project.DefaultName) {
			return nil
		}
		return errProjectFlagUnknown
	}
	if servesProjects(cmd.Name()) || cmd.Name() == "worker" {
		return fmt.Errorf("dagu %s serves every project and takes no --project", cmd.Name())
	}
	return nil
}

// addressedProject returns the project a command addresses: the one named
// by --project, else by DAGU_PROJECT, else the one whose DAG directory
// holds the current directory, else the default project. A name that no
// registered project has is an error.
func addressedProject(ctx context.Context, cmd *cobra.Command, cfg *config.Config, owners *filedag.Owners) (project.Project, error) {
	name := ""
	if flag := cmd.Flags().Lookup(projectFlag.name); flag != nil {
		name = strings.TrimSpace(flag.Value.String())
	}
	if name == "" {
		name = strings.TrimSpace(os.Getenv(runenv.EnvKeyProject))
	}
	registry := projectregistry.New(cfg.Paths.DataDir)
	if name != "" {
		if strings.EqualFold(name, project.DefaultName) {
			return defaultProject(), nil
		}
		p, err := registry.Get(ctx, name)
		if errors.Is(err, project.ErrNotFound) {
			return project.Project{}, fmt.Errorf("project %q does not exist", name)
		}
		return p, err
	}

	dir, err := os.Getwd()
	if err != nil {
		return defaultProject(), nil
	}
	// Owners places files, so it is asked for a file the directory holds,
	// which places the directory itself too.
	owner, err := projectOf(ctx, cfg, owners, registry, filepath.Join(dir, directoryProbe))
	if err != nil || owner == "" || strings.EqualFold(owner, project.DefaultName) {
		return defaultProject(), err
	}
	return registry.Get(ctx, owner)
}

// directoryProbe names a file in a directory whose project is looked up.
const directoryProbe = "dagu-project.yaml"

// projectOf returns the name of the project the file at path belongs to, or
// "" when it belongs to the library or to no project. A file outside every
// DAG root belongs to the project whose DAG directory links to where it
// lies.
func projectOf(ctx context.Context, cfg *config.Config, owners *filedag.Owners, registry project.Registry, path string) (string, error) {
	owner, err := owners.Of(ctx, path)
	if err != nil || owner.Project != "" || owner.Library {
		return owner.Project, err
	}
	// One listing of paths.projects_dir finds the DAG directories that are
	// links, so a file in no DAG root costs no lookup per project.
	entries, err := os.ReadDir(cfg.Paths.ProjectsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var links []string
	for _, entry := range entries {
		if entry.Type()&fs.ModeSymlink != 0 {
			links = append(links, entry.Name())
		}
	}
	if len(links) == 0 {
		return "", nil
	}
	projects, err := registry.List(ctx)
	if err != nil {
		return "", err
	}
	for _, p := range projects {
		if strings.EqualFold(p.Name, project.DefaultName) || !slices.ContainsFunc(links, func(name string) bool {
			return strings.EqualFold(name, p.Name)
		}) {
			continue
		}
		linked, err := owners.OfIn(ctx, p.Name, path)
		if err != nil {
			return "", err
		}
		if linked.Is(p.Name) {
			return p.Name, nil
		}
	}
	return "", nil
}

// defaultProject is the default project as the stores open it.
func defaultProject() project.Project {
	return project.Project{Name: project.DefaultName}
}

// projectManagerOptions binds a Manager to the project p while projects are
// on.
func projectManagerOptions(cfg *config.Config, p project.Project) []runtime.ManagerOption {
	if !cfg.ProjectsEnabled {
		return nil
	}
	return []runtime.ManagerOption{runtime.WithManagerProject(p.Name)}
}

// takesDAGFile reports whether the first argument of the command names a
// DAG, by name or by the path of its file.
func takesDAGFile(commandName string) bool {
	switch commandName {
	case "start", "enqueue", "dry", "restart", "retry", "stop", "status", "history",
		"validate", "rm", "cleanup", "backfill start", "backfill ls", "calendar fires":
		return true
	default:
		return false
	}
}

// checkDAGFile refuses a DAG argument whose file, found as the loader finds
// it, lies in the DAG directory of a project other than the addressed one. A
// file in the library, or in no project's DAG directory, is the addressed
// project's to run.
func (c *Context) checkDAGFile(args []string) error {
	if !c.Config.ProjectsEnabled || c.projectStores == nil || len(args) == 0 ||
		!takesDAGFile(commandFamilyName(c.Command)) {
		return nil
	}
	path, err := spec.ResolveDAGFile(args[0], c.projectPaths().DAGsDir)
	// A name that finds no file loads from elsewhere, or fails as before.
	if err != nil || spec.IsTestFile(path) || !fileutil.FileExists(path) {
		return nil
	}
	owner, err := c.projectOf(path)
	if err != nil || owner == "" || strings.EqualFold(owner, c.project.Name) {
		return err
	}
	return fmt.Errorf("%s belongs to project %s, not to project %s that this command addresses; run it with --project %s",
		args[0], owner, c.project.Name, owner)
}

// projectOf returns the name of the project the file at path belongs to,
// or "" when it belongs to the library or to no project.
func (c *Context) projectOf(path string) (string, error) {
	return projectOf(c, c.Config, c.serverStores.DAGOwners(), projectregistry.New(c.Config.Paths.DataDir), path)
}

// managesProjects reports whether the command manages projects rather than
// acting in one, so it addresses none.
func managesProjects(commandName string) bool {
	return commandName == projectCommandName || strings.HasPrefix(commandName, projectCommandName+" ")
}

// projectName returns the addressed project's name while projects are on,
// and "" while they are off.
func (c *Context) projectName() string {
	if !c.Config.ProjectsEnabled {
		return ""
	}
	return c.project.Name
}

// checkProjectDispatch refuses to send a run of a project other than the
// default one to a worker: a dispatch task does not carry its project, so
// the coordinator records the run in the default project.
func (c *Context) checkProjectDispatch() error {
	if c.inLibrary() {
		return errLibraryDispatch
	}
	return project.RefuseOtherProject(c.projectName(), project.ErrWorkerRun)
}

// checkProjectEnqueue refuses to queue a run of a project other than the
// default one: the scheduler reads only the default project's queues, so
// the run would never start.
func (c *Context) checkProjectEnqueue() error {
	if c.inLibrary() {
		return errLibraryEnqueue
	}
	return project.RefuseOtherProject(c.projectName(), project.ErrQueuedRun)
}

// ownTestFiles keeps the test files that lie in the addressed project's DAG
// directory and in no project nested in it.
func (c *Context) ownTestFiles(files []testFileRef) ([]testFileRef, error) {
	if !c.Config.ProjectsEnabled || c.projectStores == nil {
		return files, nil
	}
	var err error
	return slices.DeleteFunc(files, func(f testFileRef) bool {
		owner, ownerErr := c.projectOf(f.path)
		err = errors.Join(err, ownerErr)
		return !strings.EqualFold(owner, c.project.Name)
	}), err
}

// inProjectOf returns c, or a copy of c that addresses the project whose
// DAG directory holds path when that is another project. A path in the
// library, or in no project's DAG directory, stays in the addressed project.
func (c *Context) inProjectOf(path string) (*Context, error) {
	if !c.Config.ProjectsEnabled || c.projectStores == nil {
		return c, nil
	}
	owner, err := c.projectOf(path)
	if err != nil || owner == "" || strings.EqualFold(owner, c.project.Name) {
		return c, err
	}
	p := defaultProject()
	if owner != project.DefaultName {
		if p, err = projectregistry.New(c.Config.Paths.DataDir).Get(c, owner); err != nil {
			return nil, err
		}
	}
	stores, err := openProject(c.Context, c.Config, c.serverStores, p, c.storeOptions)
	if err != nil {
		return nil, err
	}
	persistence := newPersistence(c.serverStores, stores)
	clone := *c
	clone.project = p
	clone.projectStores = stores
	clone.settings = stores.ProjectSettings
	clone.Persistence = persistence
	clone.DAGRunMgr = runtime.NewManager(persistence.DAGRunRepository, persistence.ProcRepository, c.Config,
		projectManagerOptions(c.Config, p)...)
	return &clone, nil
}

// projectPaths returns the directories of the addressed project.
func (c *Context) projectPaths() config.ProjectPaths {
	switch {
	case c.projectStores != nil:
		return c.projectStores.Paths
	case c.settings != nil:
		return c.settings.Paths
	}
	// A remote context opens no store; it gets the directories where the
	// default project's stores open. The default project always resolves.
	paths, _ := file.ProjectStorePaths(c.Config, project.DefaultName)
	return paths
}

// projectLoadOptions returns the options that load a DAG of the addressed
// project: its base config and its DAG directory.
func (c *Context) projectLoadOptions() []spec.LoadOption {
	dagsDir := c.Config.Paths.DAGsDir
	if c.projectStores != nil {
		dagsDir = c.projectStores.Paths.DAGsDir
	}
	return append(c.projectBaseConfigOptions(), spec.WithDAGsDir(dagsDir))
}

// projectBaseConfigOptions returns the options that apply the addressed
// project's base config to a DAG it loads.
func (c *Context) projectBaseConfigOptions() []spec.LoadOption {
	if c.projectStores == nil {
		return baseConfigOptions(c.Config)
	}
	baseConfig, workspaceDir := c.projectStores.BaseConfig()
	return []spec.LoadOption{
		spec.WithBaseConfig(baseConfig),
		spec.WithWorkspaceBaseConfigDir(workspaceDir),
	}
}

// baseConfigOptions returns the options that apply the default project's
// base config to a DAG it loads.
func baseConfigOptions(cfg *config.Config) []spec.LoadOption {
	return []spec.LoadOption{
		spec.WithBaseConfig(cfg.Paths.BaseConfig),
		spec.WithWorkspaceBaseConfigDir(cfg.WorkspaceBaseConfigDir()),
	}
}
