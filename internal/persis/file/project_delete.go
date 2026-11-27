// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectrecord"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// gitDirName marks a git checkout.
const gitDirName = ".git"

var (
	// releaseTimeout bounds the wait for live processes to release a
	// project. It outlasts the time a process that died stays in the
	// service registry, and stays under the server's HTTP write timeout.
	releaseTimeout = 45 * time.Second
	// releasePollInterval is how often the service registry is read while
	// a delete waits.
	releasePollInterval = time.Second
)

// releasingServices are the services whose processes open project stores
// and record the projects they release.
var releasingServices = []serviceregistry.ServiceName{
	serviceregistry.ServiceNameServer,
	serviceregistry.ServiceNameScheduler,
	serviceregistry.ServiceNameCoordinator,
}

// unfinishedStatuses are the statuses of a run that is not finished.
var unfinishedStatuses = []ir.Status{ir.Queued, ir.Running, ir.Waiting}

// DeleteProjectOptions holds what DeleteProject reads besides the server's
// stores.
type DeleteProjectOptions struct {
	// Wiki is the part of the default wiki that migrated projects use. Nil
	// means no project uses any.
	Wiki ProjectWiki
	// Users and APIKeys hold the grants on projects. Nil opens the server's.
	Users   auth.UserStore
	APIKeys auth.APIKeyStore
}

// ProjectWiki is the part of the default wiki directory and its stores
// that projects made by migration use.
type ProjectWiki interface {
	// InPlacePages reports whether the wiki folder that p, a migrated
	// project, reads in place holds pages.
	InPlacePages(ctx context.Context, p project.Project) (bool, error)
	// SettleHistory leaves no attachment, revision, or page metadata of p's
	// pages in the default wiki's stores, so that removing p's directories
	// removes them all. p is released.
	SettleHistory(ctx context.Context, p project.Project) error
}

// DeleteProject deletes the project called name, matched without regard to
// case, and returns it as it was. It marks the project deleting and waits
// until every live server, scheduler, and coordinator has released it. It
// then removes the project's DAG directory, its state directories, its
// grants, each API key left with no role, and last its registry entry,
// which holds its Dagu Cloud binding.
//
// It returns an error wrapping project.ErrNotFound when no project has the
// name, project.ErrDefaultProject for the default project, and
// project.ErrDeleteRefused while the project has a run that is not
// finished, its DAG directory is in or holds a git checkout or holds a
// protected path, the registry records a file that runs in place as the
// project's, its in-place wiki folder holds pages, or a live process does
// not release it in time. A refused delete changes nothing, and removes the
// mark it set. A delete that fails while removing keeps the mark, and a
// later delete of the project finishes it.
func DeleteProject(
	ctx context.Context,
	cfg *config.Config,
	server *ServerStores,
	registry project.Registry,
	name string,
	opts DeleteProjectOptions,
) (project.Project, error) {
	if strings.EqualFold(name, project.DefaultName) {
		return project.Project{}, project.ErrDefaultProject
	}
	p, err := registry.Get(ctx, name)
	if err != nil {
		return project.Project{}, err
	}
	paths, err := cfg.ProjectPaths(p.Name)
	if err != nil {
		return p, err
	}
	if err := checkDeletable(ctx, cfg, server, paths, p, opts); err != nil {
		return p, err
	}

	marked := !p.Deleting
	if marked {
		if p, err = registry.SetDeleting(ctx, p.ID, true); err != nil {
			return p, err
		}
	}
	if err := checkReleased(ctx, cfg, server, p); err != nil {
		if marked {
			if _, unmarkErr := registry.SetDeleting(ctx, p.ID, false); unmarkErr != nil {
				err = errors.Join(err, unmarkErr)
			}
		}
		return p, err
	}
	return p, removeProject(ctx, server, registry, paths, p, opts)
}

// checkDeletable refuses a delete for what the project's files and the
// registry tell, before anything changes.
func checkDeletable(
	ctx context.Context,
	cfg *config.Config,
	server *ServerStores,
	paths config.ProjectPaths,
	p project.Project,
	opts DeleteProjectOptions,
) error {
	if err := cfg.CheckProjectDAGsDir(paths.DAGsDir); err != nil {
		return fmt.Errorf("%w: %w", project.ErrDeleteRefused, err)
	}
	checkout, err := gitCheckout(paths.DAGsDir)
	if err != nil {
		return err
	}
	if checkout != "" {
		return fmt.Errorf("%w: its DAG directory is in or holds the git checkout %s; remove its files there first",
			project.ErrDeleteRefused, checkout)
	}
	if server.inPlace != nil {
		files, err := server.inPlace.InPlaceFiles(ctx, p.Name)
		if err != nil {
			return err
		}
		if len(files) > 0 {
			return fmt.Errorf("%w: %s runs in place as its file; move or remove it first",
				project.ErrDeleteRefused, files[0])
		}
	}
	if p.MigratedFrom != "" && opts.Wiki != nil {
		holds, err := opts.Wiki.InPlacePages(ctx, p)
		if err != nil {
			return err
		}
		if holds {
			return fmt.Errorf("%w: the wiki folder %s it reads in place holds pages; move them to its wiki/ or remove them first",
				project.ErrDeleteRefused, p.MigratedFrom)
		}
	}
	return nil
}

// gitCheckout returns the git checkout that dir lies in or holds, or ""
// when dir does not exist or there is none.
func gitCheckout(dir string) (string, error) {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	for d := filepath.Clean(dir); ; {
		if _, err := os.Lstat(filepath.Join(d, gitDirName)); err == nil {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	var found string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == gitDirName {
			found = filepath.Dir(path)
			return fs.SkipAll
		}
		return nil
	})
	return found, err
}

// checkReleased waits until every live process has released p, which is
// marked deleting, and then refuses the delete while p has a run that is
// not finished. No run of p starts once every process has released it.
func checkReleased(ctx context.Context, cfg *config.Config, server *ServerStores, p project.Project) error {
	if err := waitReleased(ctx, server.ServiceRegistry, p.ID); err != nil {
		return err
	}
	stores, err := NewProjectStores(ctx, cfg, server, p, ProjectStoreOptions{})
	if err != nil {
		return err
	}
	scopes := []*ProjectStores{stores}
	if stores.library != nil {
		scopes = append(scopes, stores.library)
	}
	for _, s := range scopes {
		if err := refuseUnfinishedRun(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

// waitReleased waits until no live process of the services that open
// project stores holds the project with the given ID.
func waitReleased(ctx context.Context, registry serviceregistry.ServiceRegistry, id string) error {
	deadline := time.Now().Add(releaseTimeout)
	for {
		holders, err := projectHolders(ctx, registry, id)
		if err != nil {
			return err
		}
		if len(holders) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: %s did not release it; try again", project.ErrDeleteRefused, strings.Join(holders, ", "))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(releasePollInterval):
		}
	}
}

// projectHolders names the live processes that have not recorded the
// release of the project with the given ID.
func projectHolders(ctx context.Context, registry serviceregistry.ServiceRegistry, id string) ([]string, error) {
	var holders []string
	for _, service := range releasingServices {
		members, err := registry.GetServiceMembers(ctx, service)
		if err != nil {
			return nil, fmt.Errorf("read the %s members of the service registry: %w", service, err)
		}
		for _, member := range members {
			if !slices.Contains(member.ReleasedProjects, id) {
				holders = append(holders, fmt.Sprintf("%s %s", service, member.ID))
			}
		}
	}
	return holders, nil
}

// refuseUnfinishedRun refuses the delete while s records a run that is
// queued, running, or waiting.
func refuseUnfinishedRun(ctx context.Context, s *ProjectStores) error {
	refuse := func(ref ir.DAGRunRef, state string) error {
		return fmt.Errorf("%w: run %s of DAG %s is %s", project.ErrDeleteRefused, ref.ID, ref.Name, state)
	}
	items, err := s.QueueStore.All(ctx)
	if err != nil {
		return fmt.Errorf("read the queues: %w", err)
	}
	if len(items) > 0 {
		ref, err := items[0].Data()
		if err != nil {
			return fmt.Errorf("read a queued run: %w", err)
		}
		return refuse(*ref, "queued")
	}
	alive, err := s.ProcRepository.ListAllAlive(ctx)
	if err != nil {
		return fmt.Errorf("read the running processes: %w", err)
	}
	for _, refs := range alive {
		if len(refs) > 0 {
			return refuse(refs[0], "running")
		}
	}
	page, err := s.DAGRunRepository.ListStatusesPage(ctx, persis.DAGRunListOptions{
		Statuses:   unfinishedStatuses,
		AllHistory: true,
		Limit:      1,
	})
	if err != nil {
		return fmt.Errorf("read the runs: %w", err)
	}
	if len(page.Items) > 0 {
		status := page.Items[0]
		return refuse(status.DAGRun(), status.Status.String())
	}
	return nil
}

// removeProject removes p, which every live process has released, in the
// order a later delete can resume from: its files first, its registry
// entry last.
func removeProject(
	ctx context.Context,
	server *ServerStores,
	registry project.Registry,
	paths config.ProjectPaths,
	p project.Project,
	opts DeleteProjectOptions,
) error {
	if p.MigratedFrom != "" && opts.Wiki != nil {
		if err := opts.Wiki.SettleHistory(ctx, p); err != nil {
			return fmt.Errorf("move the wiki history of project %s: %w", p.Name, err)
		}
	}
	if err := fileutil.RemoveAll(paths.DAGsDir); err != nil {
		return fmt.Errorf("remove the DAG directory %s: %w", paths.DAGsDir, err)
	}
	for _, dir := range paths.RecordDirs {
		if err := removeStateDir(dir, p); err != nil {
			return err
		}
	}
	if err := removeGrants(ctx, server, p.ID, opts); err != nil {
		return err
	}
	return registry.Delete(ctx, p.ID)
}

// removeStateDir removes dir, a state directory of p, unless it records
// another project.
func removeStateDir(dir string, p project.Project) error {
	record, err := projectrecord.Read(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	case record.ID != p.ID:
		return nil
	}
	if err := fileutil.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove the state directory %s: %w", dir, err)
	}
	return nil
}

// removeGrants removes every grant on the project with the given ID, and
// each API key that has no role without its grant.
func removeGrants(ctx context.Context, server *ServerStores, id string, opts DeleteProjectOptions) error {
	users, keys := opts.Users, opts.APIKeys
	var err error
	if users == nil {
		if users, err = server.UserStore(); err != nil {
			return err
		}
	}
	if keys == nil {
		if keys, err = server.APIKeyStore(); err != nil {
			return err
		}
	}
	onProject := func(grant auth.ProjectGrant) bool { return grant.ProjectID == id }

	all, err := users.List(ctx)
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}
	for _, user := range all {
		if !slices.ContainsFunc(user.ProjectGrants, onProject) {
			continue
		}
		user.ProjectGrants = slices.DeleteFunc(user.ProjectGrants, onProject)
		if err := users.Update(ctx, user); err != nil {
			return fmt.Errorf("remove the grant of user %s: %w", user.Username, err)
		}
	}

	apiKeys, err := keys.List(ctx)
	if err != nil {
		return fmt.Errorf("list API keys: %w", err)
	}
	for _, key := range apiKeys {
		if !slices.ContainsFunc(key.ProjectGrants, onProject) {
			continue
		}
		key.ProjectGrants = slices.DeleteFunc(key.ProjectGrants, onProject)
		if hasRole(key) {
			err = keys.Update(ctx, key)
		} else {
			err = keys.Delete(ctx, key.ID)
		}
		if err != nil && !errors.Is(err, auth.ErrAPIKeyNotFound) {
			return fmt.Errorf("remove the grant of API key %s: %w", key.Name, err)
		}
	}
	return nil
}

// hasRole reports whether key has a role in some project: a project grant,
// or a server-wide role that its workspace access does not take away.
func hasRole(key *auth.APIKey) bool {
	if len(key.ProjectGrants) > 0 {
		return true
	}
	access := auth.NormalizeWorkspaceAccess(key.WorkspaceAccess)
	return access.All || len(access.Grants) > 0
}
