// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package projectregistry keeps the project registry in one file in the data
// directory.
//
//	<data_dir>/
//	├── projects.json    the registry
//	└── projects.lock/   the lock every writer holds
package projectregistry

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/google/uuid"
)

const (
	fileName    = "projects.json"
	lockDirName = "projects.lock"
	filePerm    = 0o600

	// schemaVersion is the file format this version writes. A newer format
	// is refused on read, so the file is never written back without the
	// fields this version does not know.
	schemaVersion = 1

	// lockStaleThreshold frees the lock of a process that died holding it.
	lockStaleThreshold = 30 * time.Second
	lockRetryInterval  = 20 * time.Millisecond
	// writeBudget is how long after the attempt that took the lock a writer
	// may still start to replace the file. The rest of lockStaleThreshold is
	// left for the replacement itself.
	writeBudget = lockStaleThreshold * 3 / 4
)

// Registry is a [project.Registry] kept in projects.json. Every process that
// shares the data directory takes the same lock to write it.
type Registry struct {
	path    string
	lockDir string
	// dirSeed is the slot seed of the default project and each migrated one.
	dirSeed string
	// pollTicks returns the ticks on which Watch checks the file, and a
	// function that stops them.
	pollTicks func() (<-chan time.Time, func())
	now       func() time.Time
}

var _ project.Registry = (*Registry)(nil)

// registryFile is the content of projects.json.
type registryFile struct {
	SchemaVersion int             `json:"schema_version"`
	Projects      []storedProject `json:"projects"`
}

type storedProject struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	DisplayName  string        `json:"display_name"`
	CreatedAt    time.Time     `json:"created_at"`
	MigratedFrom string        `json:"migrated_from,omitempty"`
	SlotSeed     string        `json:"slot_seed"`
	Binding      storedBinding `json:"binding,omitzero"`
	Deleting     bool          `json:"deleting,omitempty"`
	DeleteMark   string        `json:"delete_mark,omitempty"`
}

type storedBinding struct {
	CloudProjectID string `json:"cloud_project_id,omitempty"`
}

// New returns the registry kept in dataDir, which is paths.data_dir spelled
// as configured. That spelling is also the slot seed it gives the default
// project and each migrated one.
func New(dataDir string) *Registry {
	return &Registry{
		path:      filepath.Join(dataDir, fileName),
		lockDir:   filepath.Join(dataDir, lockDirName),
		dirSeed:   dataDir,
		pollTicks: newPollTicker,
		now:       time.Now,
	}
}

// List returns every registered project, ordered by name.
func (r *Registry) List(_ context.Context) ([]project.Project, error) {
	return r.load()
}

// Get returns the project whose name matches name without regard to case.
func (r *Registry) Get(_ context.Context, name string) (project.Project, error) {
	projects, err := r.load()
	if err != nil {
		return project.Project{}, err
	}
	i := indexByName(projects, name)
	if i < 0 {
		return project.Project{}, fmt.Errorf("%w: %q", project.ErrNotFound, name)
	}
	return projects[i], nil
}

// GetByID returns the project with the given ID.
func (r *Registry) GetByID(_ context.Context, id string) (project.Project, error) {
	projects, err := r.load()
	if err != nil {
		return project.Project{}, err
	}
	i, err := indexByID(projects, id)
	if err != nil {
		return project.Project{}, err
	}
	return projects[i], nil
}

// Create registers a project under a new ID.
func (r *Registry) Create(ctx context.Context, in project.CreateInput) (project.Project, error) {
	if err := project.ValidateName(in.Name); err != nil {
		return project.Project{}, err
	}
	id := uuid.NewString()
	created := project.Project{
		ID:           id,
		Name:         in.Name,
		DisplayName:  cmp.Or(in.DisplayName, in.Name),
		CreatedAt:    r.now().UTC(),
		MigratedFrom: in.MigratedFrom,
		SlotSeed:     id,
	}
	if in.MigratedFrom != "" {
		created.SlotSeed = r.dirSeed
	}
	if err := project.ValidateDisplayName(created.DisplayName); err != nil {
		return project.Project{}, err
	}
	err := r.update(ctx, func(projects []project.Project) ([]project.Project, bool, error) {
		if i := indexByName(projects, in.Name); i >= 0 {
			return nil, false, fmt.Errorf("%w: %q", project.ErrNameTaken, projects[i].Name)
		}
		return append(projects, created), true, nil
	})
	if err != nil {
		return project.Project{}, err
	}
	return created, nil
}

// EnsureDefault returns the default project, registering it first when the
// registry lacks it.
func (r *Registry) EnsureDefault(ctx context.Context) (project.Project, error) {
	var result project.Project
	err := r.update(ctx, func(projects []project.Project) ([]project.Project, bool, error) {
		if i := indexByName(projects, project.DefaultName); i >= 0 {
			result = projects[i]
			return nil, false, nil
		}
		result = project.Project{
			ID:          uuid.NewString(),
			Name:        project.DefaultName,
			DisplayName: project.DefaultName,
			CreatedAt:   r.now().UTC(),
			SlotSeed:    r.dirSeed,
		}
		return append(projects, result), true, nil
	})
	return result, err
}

// SetDisplayName changes a project's display name.
func (r *Registry) SetDisplayName(ctx context.Context, id, displayName string) (project.Project, error) {
	var result project.Project
	err := r.update(ctx, func(projects []project.Project) ([]project.Project, bool, error) {
		i, err := indexByID(projects, id)
		if err != nil {
			return nil, false, err
		}
		next := cmp.Or(displayName, projects[i].Name)
		if err := project.ValidateDisplayName(next); err != nil {
			return nil, false, err
		}
		changed := projects[i].DisplayName != next
		projects[i].DisplayName = next
		result = projects[i]
		return projects, changed, nil
	})
	return result, err
}

// SetDeleting marks or unmarks a project as being deleted.
func (r *Registry) SetDeleting(ctx context.Context, id string, deleting bool) (project.Project, error) {
	var result project.Project
	err := r.update(ctx, func(projects []project.Project) ([]project.Project, bool, error) {
		i, err := indexByID(projects, id)
		if err != nil {
			return nil, false, err
		}
		if deleting && projects[i].Name == project.DefaultName {
			return nil, false, project.ErrDefaultProject
		}
		changed := deleting || projects[i].Deleting
		projects[i].Deleting, projects[i].DeleteMark = deleting, ""
		if deleting {
			projects[i].DeleteMark = uuid.NewString()
		}
		result = projects[i]
		return projects, changed, nil
	})
	return result, err
}

// Delete removes a project from the registry.
func (r *Registry) Delete(ctx context.Context, id string) error {
	return r.update(ctx, func(projects []project.Project) ([]project.Project, bool, error) {
		i, err := indexByID(projects, id)
		if err != nil {
			return nil, false, err
		}
		if projects[i].Name == project.DefaultName {
			return nil, false, project.ErrDefaultProject
		}
		return slices.Delete(projects, i, i+1), true, nil
	})
}

// update runs change on the registered projects while holding the registry
// lock, and stores the projects it returns when it reports a change.
func (r *Registry) update(ctx context.Context, change func([]project.Project) ([]project.Project, bool, error)) error {
	lock := dirlock.New(r.lockDir, &dirlock.LockOptions{
		StaleThreshold: lockStaleThreshold,
		// Hosts that share the data directory can disagree on the time.
		ObserveHeartbeat: true,
	})
	lockedAt, err := r.acquire(ctx, lock)
	if err != nil {
		return fmt.Errorf("project registry: lock %s: %w", r.lockDir, err)
	}
	defer func() { _ = lock.Unlock() }()

	projects, err := r.load()
	if err != nil {
		return err
	}
	next, changed, err := change(projects)
	if err != nil || !changed {
		return err
	}
	if !lock.IsHeldByMe() {
		return fmt.Errorf("project registry: lock lost before the write: %w", dirlock.ErrLockNotHeld)
	}
	// Shared storage offers no compare-and-swap for a rename, so a writer
	// that lost the lock would replace a newer registry. A contender takes
	// the lock over only after watching it unchanged for lockStaleThreshold
	// from when it saw it first, which is after lockedAt. From this check,
	// the replacement thus has the rest of that time to finish; one that
	// stalls longer can still overwrite a newer registry.
	if r.now().Sub(lockedAt) > writeBudget {
		return fmt.Errorf("project registry: lock held too long before the write: %w", dirlock.ErrLockNotHeld)
	}
	return r.store(next)
}

// acquire takes lock and returns the time at which the attempt that took it
// started, before the lock existed.
func (r *Registry) acquire(ctx context.Context, lock dirlock.DirLock) (time.Time, error) {
	ticker := time.NewTicker(lockRetryInterval)
	defer ticker.Stop()
	for {
		attempt := r.now()
		err := lock.TryLock()
		if err == nil {
			return attempt, nil
		}
		if !errors.Is(err, dirlock.ErrLockConflict) {
			return time.Time{}, err
		}
		select {
		case <-ctx.Done():
			return time.Time{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// load reads the registered projects, ordered by name. A missing file holds
// no projects.
func (r *Registry) load() ([]project.Project, error) {
	data, err := r.readFile()
	if err != nil {
		return nil, err
	}
	return decode(data)
}

// readFile returns the content of the file, or nil when it is missing.
func (r *Registry) readFile() ([]byte, error) {
	data, err := fileutil.ReadFile(r.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("project registry: read %s: %w", r.path, err)
	}
	return data, nil
}

func (r *Registry) store(projects []project.Project) error {
	content := registryFile{
		SchemaVersion: schemaVersion,
		Projects:      make([]storedProject, len(projects)),
	}
	for i, p := range projects {
		content.Projects[i] = toStored(p)
	}
	data, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		return fmt.Errorf("project registry: encode: %w", err)
	}
	if err := fileutil.WriteFileAtomic(r.path, data, filePerm); err != nil {
		return fmt.Errorf("project registry: write %s: %w", r.path, err)
	}
	return nil
}

func decode(data []byte) ([]project.Project, error) {
	if data == nil {
		return nil, nil
	}
	var content registryFile
	if err := json.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("project registry: decode %s: %w", fileName, err)
	}
	if content.SchemaVersion > schemaVersion {
		return nil, fmt.Errorf("project registry: %s has schema version %d, newer than %d, which this version of Dagu reads",
			fileName, content.SchemaVersion, schemaVersion)
	}
	projects := make([]project.Project, len(content.Projects))
	for i, p := range content.Projects {
		projects[i] = p.toProject()
	}
	slices.SortFunc(projects, func(a, b project.Project) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return projects, nil
}

func indexByName(projects []project.Project, name string) int {
	return slices.IndexFunc(projects, func(p project.Project) bool {
		return strings.EqualFold(p.Name, name)
	})
}

func indexByID(projects []project.Project, id string) (int, error) {
	i := slices.IndexFunc(projects, func(p project.Project) bool { return p.ID == id })
	if i < 0 {
		return -1, fmt.Errorf("%w: ID %q", project.ErrNotFound, id)
	}
	return i, nil
}

func toStored(p project.Project) storedProject {
	return storedProject{
		ID:           p.ID,
		Name:         p.Name,
		DisplayName:  p.DisplayName,
		CreatedAt:    p.CreatedAt,
		MigratedFrom: p.MigratedFrom,
		SlotSeed:     p.SlotSeed,
		Binding:      storedBinding{CloudProjectID: p.Binding.CloudProjectID},
		Deleting:     p.Deleting,
		DeleteMark:   p.DeleteMark,
	}
}

func (s storedProject) toProject() project.Project {
	return project.Project{
		ID:           s.ID,
		Name:         s.Name,
		DisplayName:  s.DisplayName,
		CreatedAt:    s.CreatedAt,
		MigratedFrom: s.MigratedFrom,
		SlotSeed:     s.SlotSeed,
		Binding:      project.Binding{CloudProjectID: s.Binding.CloudProjectID},
		Deleting:     s.Deleting,
		DeleteMark:   s.DeleteMark,
	}
}
