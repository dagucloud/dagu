// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package projectregistry_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCreate(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	reg := projectregistry.New(dir)

	before := time.Now()
	created, err := reg.Create(ctx, project.CreateInput{Name: "weather", MigratedFrom: "weather"})
	require.NoError(t, err)
	require.NoError(t, uuid.Validate(created.ID))
	require.Equal(t, "weather", created.Name)
	require.Equal(t, "weather", created.DisplayName, "the display name is the name until one is set")
	require.Equal(t, "weather", created.MigratedFrom)
	require.WithinRange(t, created.CreatedAt, before, time.Now())
	require.False(t, created.Deleting)
	require.Zero(t, created.Binding)
	require.FileExists(t, filepath.Join(dir, "projects.json"))

	// A registry opened on the same directory, as by another process, reads
	// what this one stored.
	other := projectregistry.New(dir)
	got, err := other.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, created, got)
	list, err := other.List(ctx)
	require.NoError(t, err)
	require.Equal(t, []project.Project{created}, list)

	named, err := reg.Create(ctx, project.CreateInput{Name: "grid", DisplayName: "Power Grid"})
	require.NoError(t, err)
	require.Equal(t, "Power Grid", named.DisplayName)
	require.NotEqual(t, created.ID, named.ID)
}

func TestCreateRefusesBrokenRules(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	reg := projectregistry.New(dir)

	for _, name := range []string{"", "-ops", "a.b", strings.Repeat("a", 65), "con", "LPT1", "default", "Default"} {
		_, err := reg.Create(ctx, project.CreateInput{Name: name})
		require.ErrorIs(t, err, project.ErrInvalidName, name)
	}
	_, err := reg.Create(ctx, project.CreateInput{Name: "weather", DisplayName: strings.Repeat("a", 201)})
	require.ErrorIs(t, err, project.ErrInvalidDisplayName)

	list, err := reg.List(ctx)
	require.NoError(t, err)
	require.Empty(t, list)
}

func TestNamesAreUniqueWithoutRegardToCase(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	reg := projectregistry.New(t.TempDir())

	created, err := reg.Create(ctx, project.CreateInput{Name: "Weather"})
	require.NoError(t, err)
	for _, name := range []string{"Weather", "weather", "WEATHER"} {
		_, err := reg.Create(ctx, project.CreateInput{Name: name})
		require.ErrorIs(t, err, project.ErrNameTaken, name)
	}

	got, err := reg.Get(ctx, "wEATHER")
	require.NoError(t, err)
	require.Equal(t, created, got, "a lookup in another case resolves to the stored spelling")

	list, err := reg.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
}

func TestNotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	reg := projectregistry.New(t.TempDir())

	_, err := reg.Get(ctx, "weather")
	require.ErrorIs(t, err, project.ErrNotFound)
	_, err = reg.GetByID(ctx, uuid.NewString())
	require.ErrorIs(t, err, project.ErrNotFound)
	_, err = reg.SetDisplayName(ctx, uuid.NewString(), "Weather")
	require.ErrorIs(t, err, project.ErrNotFound)
	_, err = reg.SetDeleting(ctx, uuid.NewString(), true)
	require.ErrorIs(t, err, project.ErrNotFound)
	require.ErrorIs(t, reg.Delete(ctx, uuid.NewString()), project.ErrNotFound)
}

func TestListOrdersByName(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	reg := projectregistry.New(t.TempDir())

	for _, name := range []string{"b", "C", "a"} {
		_, err := reg.Create(ctx, project.CreateInput{Name: name})
		require.NoError(t, err)
	}
	list, err := reg.List(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "C"}, names(list))
}

func TestSetDisplayName(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	reg := projectregistry.New(dir)
	created, err := reg.Create(ctx, project.CreateInput{Name: "weather"})
	require.NoError(t, err)

	displayName := strings.Repeat("天", 200)
	changed, err := reg.SetDisplayName(ctx, created.ID, displayName)
	require.NoError(t, err)
	want := created
	want.DisplayName = displayName
	require.Equal(t, want, changed, "only the display name changes")
	got, err := projectregistry.New(dir).GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, want, got)

	_, err = reg.SetDisplayName(ctx, created.ID, strings.Repeat("a", 201))
	require.ErrorIs(t, err, project.ErrInvalidDisplayName)

	restored, err := reg.SetDisplayName(ctx, created.ID, "")
	require.NoError(t, err)
	require.Equal(t, "weather", restored.DisplayName)
}

func TestSetDeleting(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	reg := projectregistry.New(dir)
	other := projectregistry.New(dir)
	created, err := reg.Create(ctx, project.CreateInput{Name: "weather"})
	require.NoError(t, err)

	marked, err := reg.SetDeleting(ctx, created.ID, true)
	require.NoError(t, err)
	require.True(t, marked.Deleting)
	require.NotEmpty(t, marked.DeleteMark)
	got, err := other.Get(ctx, "weather")
	require.NoError(t, err)
	require.True(t, got.Deleting)
	require.Equal(t, marked.DeleteMark, got.DeleteMark)

	// A release recorded for one mark must not count for the next.
	again, err := other.SetDeleting(ctx, created.ID, true)
	require.NoError(t, err)
	require.NotEqual(t, marked.DeleteMark, again.DeleteMark)

	unmarked, err := other.SetDeleting(ctx, created.ID, false)
	require.NoError(t, err)
	require.False(t, unmarked.Deleting)
	require.Empty(t, unmarked.DeleteMark)
	got, err = reg.Get(ctx, "weather")
	require.NoError(t, err)
	require.False(t, got.Deleting)
}

func TestDelete(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	reg := projectregistry.New(t.TempDir())
	created, err := reg.Create(ctx, project.CreateInput{Name: "weather"})
	require.NoError(t, err)

	require.NoError(t, reg.Delete(ctx, created.ID))
	_, err = reg.GetByID(ctx, created.ID)
	require.ErrorIs(t, err, project.ErrNotFound)

	again, err := reg.Create(ctx, project.CreateInput{Name: "Weather"})
	require.NoError(t, err)
	require.NotEqual(t, created.ID, again.ID, "a project created again under a deleted one's name gets a new ID")
}

func TestDefaultProject(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	reg := projectregistry.New(dir)

	def, err := reg.EnsureDefault(ctx)
	require.NoError(t, err)
	require.NoError(t, uuid.Validate(def.ID))
	require.Equal(t, project.DefaultName, def.Name)
	require.Equal(t, project.DefaultName, def.DisplayName)

	again, err := projectregistry.New(dir).EnsureDefault(ctx)
	require.NoError(t, err)
	require.Equal(t, def, again, "the default project is registered once")

	_, err = reg.Create(ctx, project.CreateInput{Name: "DEFAULT"})
	require.ErrorIs(t, err, project.ErrInvalidName)
	_, err = reg.SetDeleting(ctx, def.ID, true)
	require.ErrorIs(t, err, project.ErrDefaultProject)
	require.ErrorIs(t, reg.Delete(ctx, def.ID), project.ErrDefaultProject)

	renamed, err := reg.SetDisplayName(ctx, def.ID, "Main")
	require.NoError(t, err)
	require.Equal(t, "Main", renamed.DisplayName)
}

// A project's slot seed is fixed when it is registered, so a restart or a
// host that mounts the data directory under another path reads the same one.
func TestSlotSeed(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dataDir := t.TempDir()
	reg := projectregistry.New(dataDir)

	def, err := reg.EnsureDefault(ctx)
	require.NoError(t, err)
	require.Equal(t, dataDir, def.SlotSeed)
	migrated, err := reg.Create(ctx, project.CreateInput{Name: "billing", MigratedFrom: "billing"})
	require.NoError(t, err)
	require.Equal(t, dataDir, migrated.SlotSeed)
	created, err := reg.Create(ctx, project.CreateInput{Name: "weather"})
	require.NoError(t, err)
	require.Equal(t, created.ID, created.SlotSeed)

	elsewhere := projectregistry.New(dataDir + string(filepath.Separator))
	again, err := elsewhere.EnsureDefault(ctx)
	require.NoError(t, err)
	require.Equal(t, def, again)
	list, err := elsewhere.List(ctx)
	require.NoError(t, err)
	require.Equal(t, []project.Project{migrated, def, created}, list)
}

// v2 hashed paths.data_dir as configured, so the seed keeps that spelling:
// it is neither cleaned nor resolved through a link.
func TestSlotSeedKeepsSpelling(t *testing.T) {
	t.Parallel()

	for name, spell := range map[string]func(t *testing.T, dataDir string) string{
		"TrailingSeparator": func(_ *testing.T, dataDir string) string {
			return dataDir + string(filepath.Separator)
		},
		"Link": func(t *testing.T, dataDir string) string {
			link := filepath.Join(t.TempDir(), "link")
			if err := os.Symlink(dataDir, link); err != nil {
				t.Skipf("symlinks are unavailable: %v", err)
			}
			return link
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			spelled := spell(t, t.TempDir())
			reg := projectregistry.New(spelled)

			def, err := reg.EnsureDefault(t.Context())
			require.NoError(t, err)
			require.Equal(t, spelled, def.SlotSeed)
			migrated, err := reg.Create(t.Context(), project.CreateInput{Name: "billing", MigratedFrom: "billing"})
			require.NoError(t, err)
			require.Equal(t, spelled, migrated.SlotSeed)
		})
	}
}

// Writers in separate registries stand for separate processes, which only the
// file lock serializes.
func TestConcurrentWriters(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	const writers = 16

	t.Run("NoWriteIsLost", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make([]error, writers)
		for i := range writers {
			wg.Go(func() {
				_, errs[i] = projectregistry.New(dir).Create(ctx, project.CreateInput{Name: fmt.Sprintf("p%02d", i)})
			})
		}
		wg.Wait()
		for _, err := range errs {
			require.NoError(t, err)
		}
		list, err := projectregistry.New(dir).List(ctx)
		require.NoError(t, err)
		require.Len(t, list, writers)
	})

	t.Run("OneNameWinsAcrossCases", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make([]error, writers)
		for i := range writers {
			// Alternate the case of each letter, so every writer spells it
			// differently.
			name := []byte("shared")
			for j := range name {
				if i>>j&1 == 1 {
					name[j] -= 'a' - 'A'
				}
			}
			wg.Go(func() {
				_, errs[i] = projectregistry.New(dir).Create(ctx, project.CreateInput{Name: string(name)})
			})
		}
		wg.Wait()
		created := 0
		for _, err := range errs {
			if err == nil {
				created++
				continue
			}
			require.ErrorIs(t, err, project.ErrNameTaken)
		}
		require.Equal(t, 1, created)
	})
}

// A file this version cannot read is reported and never written over.
func TestUnreadableFileIsKept(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{
		"NewerSchema": `{"schema_version": 2, "projects": []}`,
		"Corrupt":     `{"schema_version": 1, "projects": [`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			dir := t.TempDir()
			path := filepath.Join(dir, "projects.json")
			require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
			reg := projectregistry.New(dir)

			_, err := reg.List(ctx)
			require.Error(t, err)
			_, err = reg.Create(ctx, project.CreateInput{Name: "weather"})
			require.Error(t, err)
			_, err = reg.EnsureDefault(ctx)
			require.Error(t, err)

			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, content, string(data))
		})
	}
}

func names(projects []project.Project) []string {
	out := make([]string, len(projects))
	for i, p := range projects {
		out[i] = p.Name
	}
	return out
}
