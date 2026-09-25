// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
)

func TestDAGRepositoryPreservesLegacyFlags(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	homeDir := t.TempDir()
	dataDir := filepath.Join(homeDir, "data")
	flagsDir := filepath.Join(dataDir, "suspend")
	legacyDir := filepath.Join(homeDir, "suspend")

	require.NoError(t, os.MkdirAll(legacyDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "alpha.suspend"), []byte{}, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "beta.suspend"), []byte{}, 0o600))

	repo, err := persisfile.NewDAGRepository(
		suspendFlagsTestConfig(homeDir, dataDir, flagsDir, legacyDir),
		persisfile.WithDAGSkipExamples(true),
	)
	require.NoError(t, err)

	for _, id := range []string{"alpha", "beta"} {
		suspended, err := repo.IsSuspended(ctx, id)
		require.NoError(t, err)
		assert.True(t, suspended, "expected %q to stay suspended before scheduler startup", id)
	}
	assert.FileExists(t, filepath.Join(legacyDir, "alpha.suspend"))
	assert.NoFileExists(t, filepath.Join(flagsDir, "alpha.suspend"))

	// A flag written after construction must be visible without restarting.
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "gamma.suspend"), nil, 0o600))
	suspended, err := repo.IsSuspended(ctx, "gamma")
	require.NoError(t, err)
	assert.True(t, suspended)
	require.NoError(t, repo.SetSuspended(ctx, "gamma", false))
	assert.NoFileExists(t, filepath.Join(legacyDir, "gamma.suspend"))
}

func TestDAGRepositoryCopiesLegacyFlags(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	homeDir := t.TempDir()
	dataDir := filepath.Join(homeDir, "data")
	flagsDir := filepath.Join(dataDir, "suspend")
	legacyDir := filepath.Join(homeDir, "suspend")

	require.NoError(t, os.MkdirAll(legacyDir, 0o750))
	require.NoError(t, os.MkdirAll(flagsDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "alpha.suspend"), []byte{}, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(flagsDir, "alpha.suspend"), []byte("existing"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "beta.suspend"), []byte{}, 0o600))

	repo, err := persisfile.NewDAGRepository(
		suspendFlagsTestConfig(homeDir, dataDir, flagsDir, legacyDir),
		persisfile.WithDAGSkipExamples(true),
	)
	require.NoError(t, err)

	for _, id := range []string{"alpha", "beta"} {
		suspended, err := repo.IsSuspended(ctx, id)
		require.NoError(t, err)
		assert.True(t, suspended)
	}
	require.NoError(t, repo.MigrateSuspensionState(ctx))
	require.NoError(t, repo.MigrateSuspensionState(ctx))
	for _, id := range []string{"alpha", "beta"} {
		assert.FileExists(t, filepath.Join(legacyDir, id+".suspend"))
		assert.FileExists(t, filepath.Join(flagsDir, id+".suspend"))
	}
	contents, err := os.ReadFile(filepath.Join(flagsDir, "alpha.suspend"))
	require.NoError(t, err)
	assert.Equal(t, "existing", string(contents))

	require.NoError(t, repo.SetSuspended(ctx, "alpha", false))
	require.NoError(t, repo.SetSuspended(ctx, "beta", true))
	assert.NoFileExists(t, filepath.Join(legacyDir, "alpha.suspend"))
	assert.NoFileExists(t, filepath.Join(legacyDir, "beta.suspend"))
	require.NoError(t, repo.MigrateSuspensionState(ctx))
	suspended, err := repo.IsSuspended(ctx, "alpha")
	require.NoError(t, err)
	assert.False(t, suspended)
	suspended, err = repo.IsSuspended(ctx, "beta")
	require.NoError(t, err)
	assert.True(t, suspended)
}

func TestNewDAGRepositoryWithoutLegacySuspendFlags(t *testing.T) {
	t.Parallel()
	homeDir := t.TempDir()
	dataDir := filepath.Join(homeDir, "data")
	flagsDir := filepath.Join(dataDir, "suspend")

	repo, err := persisfile.NewDAGRepository(
		suspendFlagsTestConfig(homeDir, dataDir, flagsDir, filepath.Join(homeDir, "suspend")),
		persisfile.WithDAGSkipExamples(true),
	)
	require.NoError(t, err)

	require.NoError(t, repo.MigrateSuspensionState(context.Background()))
	suspended, err := repo.IsSuspended(context.Background(), "alpha")
	require.NoError(t, err)
	assert.False(t, suspended)
}

// The index is kept under the data directory so a read-only DAGs directory
// still gets a persisted index. An index left in the DAGs directory by an
// earlier version is removed.
func TestDAGIndexUnderDataDir(t *testing.T) {
	t.Parallel()
	cfg, legacyIndex := newDAGIndexTestConfig(t)

	repo, err := persisfile.NewDAGRepository(cfg, persisfile.WithDAGSkipExamples(true))
	require.NoError(t, err)
	assert.NoFileExists(t, legacyIndex)
	assertListUsesDataDirIndex(t, repo, cfg.Paths.DataDir)
	assert.NoFileExists(t, legacyIndex)
}

// A read-only DAGs directory keeps its legacy index, which is neither an error
// nor read back; listing still persists the index under the data directory.
func TestDAGIndexReadOnlyDAGsDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not prevent file removal on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	t.Parallel()
	cfg, legacyIndex := newDAGIndexTestConfig(t)
	require.NoError(t, os.Chmod(cfg.Paths.DAGsDir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(cfg.Paths.DAGsDir, 0o750) })

	repo, err := persisfile.NewDAGRepository(cfg, persisfile.WithDAGSkipExamples(true))
	require.NoError(t, err)
	assert.FileExists(t, legacyIndex)
	assertListUsesDataDirIndex(t, repo, cfg.Paths.DataDir)
}

// newDAGIndexTestConfig returns a config whose DAGs directory holds one DAG
// and a corrupt index left by an earlier version at the returned path.
func newDAGIndexTestConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	home := t.TempDir()
	data := filepath.Join(home, "data")
	cfg := suspendFlagsTestConfig(home, data, filepath.Join(data, "suspend"), "")
	legacyIndex := filepath.Join(cfg.Paths.DAGsDir, ".dag.index")
	require.NoError(t, os.MkdirAll(cfg.Paths.DAGsDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Paths.DAGsDir, "alpha.yaml"), []byte("steps: []\n"), 0o600))
	require.NoError(t, os.WriteFile(legacyIndex, []byte("stale"), 0o600))
	return cfg, legacyIndex
}

func assertListUsesDataDirIndex(t *testing.T, repo *persis.DAGRepository, dataDir string) {
	t.Helper()
	result, _, err := repo.List(context.Background(), persis.DAGListOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, result.TotalCount)
	indexes, err := filepath.Glob(filepath.Join(dataDir, "cache", "dag-index", "*.index"))
	require.NoError(t, err)
	assert.Len(t, indexes, 1)
}

func suspendFlagsTestConfig(homeDir, dataDir, flagsDir, legacyDir string) *config.Config {
	cfg := &config.Config{}
	cfg.Paths.DAGsDir = filepath.Join(homeDir, "dags")
	cfg.Paths.DataDir = dataDir
	cfg.Paths.SuspendFlagsDir = flagsDir
	cfg.Paths.SuspendFlagsDirLegacy = legacyDir
	return cfg
}

func TestLegacyFlagsInListings(t *testing.T) {
	t.Parallel()
	repo, cfg := newLegacyFlagsRepository(t)
	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, "alpha", []byte("steps: []\n")))
	for _, suspended := range []bool{true, false, true} {
		flag := filepath.Join(cfg.Paths.SuspendFlagsDirLegacy, "alpha.suspend")
		if suspended {
			require.NoError(t, os.WriteFile(flag, nil, 0o600))
		} else {
			require.NoError(t, os.Remove(flag))
		}
		result, _, err := repo.List(ctx, persis.DAGListOptions{})
		require.NoError(t, err)
		require.Len(t, result.Items, 1)
		assert.Equal(t, suspended, result.Items[0].Suspended)
	}
}

func TestSuspendChangesSerializeWithMigration(t *testing.T) {
	t.Parallel()
	repo, cfg := newLegacyFlagsRepository(t)
	other, err := persisfile.NewDAGRepository(cfg, persisfile.WithDAGSkipExamples(true))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Paths.SuspendFlagsDirLegacy, "alpha.suspend"), nil, 0o600))

	// Hold the same lock from an independent handle to exercise cancellation
	// while another process owns suspension mutations.
	require.NoError(t, os.MkdirAll(cfg.Paths.SuspendFlagsDir, 0o750))
	lock := flock.New(filepath.Join(cfg.Paths.SuspendFlagsDir, ".suspend.lock"))
	require.NoError(t, lock.Lock())
	t.Cleanup(func() { _ = lock.Unlock() })
	for _, operation := range []func(context.Context) error{
		repo.MigrateSuspensionState,
		func(ctx context.Context) error { return other.SetSuspended(ctx, "alpha", false) },
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		err := operation(ctx)
		cancel()
		require.ErrorIs(t, err, context.DeadlineExceeded)
	}
	require.NoError(t, lock.Unlock())

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Go(func() { errs <- repo.MigrateSuspensionState(context.Background()) })
	wg.Go(func() { errs <- other.SetSuspended(context.Background(), "alpha", false) })
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, repo.MigrateSuspensionState(context.Background()))
	suspended, err := repo.IsSuspended(context.Background(), "alpha")
	require.NoError(t, err)
	assert.False(t, suspended)
}

func TestSuspendWriteFailurePreservesLegacy(t *testing.T) {
	t.Parallel()
	repo, cfg := newLegacyFlagsRepository(t)
	flag := filepath.Join(cfg.Paths.SuspendFlagsDirLegacy, "alpha.suspend")
	require.NoError(t, os.WriteFile(flag, nil, 0o600))
	require.NoError(t, os.WriteFile(cfg.Paths.SuspendFlagsDir, nil, 0o600))
	require.Error(t, repo.MigrateSuspensionState(context.Background()))
	require.Error(t, repo.SetSuspended(context.Background(), "alpha", true))
	assert.FileExists(t, flag)
}

func TestSuspendCleanupFailure(t *testing.T) {
	t.Parallel()
	repo, cfg := newLegacyFlagsRepository(t)
	flag := filepath.Join(cfg.Paths.SuspendFlagsDirLegacy, "alpha.suspend")
	// A nonempty directory cannot be deleted on any supported platform.
	require.NoError(t, os.MkdirAll(flag, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(flag, "child"), nil, 0o600))
	for _, suspended := range []bool{true, false} {
		require.Error(t, repo.SetSuspended(context.Background(), "alpha", suspended))
		actual, err := repo.IsSuspended(context.Background(), "alpha")
		require.NoError(t, err)
		assert.True(t, actual)
	}
}

func TestMigrationIgnoresUnrelatedFiles(t *testing.T) {
	t.Parallel()
	repo, cfg := newLegacyFlagsRepository(t)
	legacy := cfg.Paths.SuspendFlagsDirLegacy
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "notes.txt"), []byte("keep"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(legacy, "nested.suspend"), 0o750))
	require.NoError(t, repo.MigrateSuspensionState(context.Background()))
	contents, err := os.ReadFile(filepath.Join(legacy, "notes.txt"))
	require.NoError(t, err)
	assert.Equal(t, "keep", string(contents))
	assert.DirExists(t, filepath.Join(legacy, "nested.suspend"))
	assert.NoDirExists(t, filepath.Join(cfg.Paths.SuspendFlagsDir, "nested.suspend"))
}

func TestSuspendFlagsSameDirectory(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	flags := filepath.Join(home, "suspend")
	cfg := suspendFlagsTestConfig(home, home, flags, flags)
	repo, err := persisfile.NewDAGRepository(cfg, persisfile.WithDAGSkipExamples(true))
	require.NoError(t, err)
	for _, suspended := range []bool{true, false} {
		require.NoError(t, repo.SetSuspended(context.Background(), "alpha", suspended))
		require.NoError(t, repo.MigrateSuspensionState(context.Background()))
		actual, err := repo.IsSuspended(context.Background(), "alpha")
		require.NoError(t, err)
		assert.Equal(t, suspended, actual)
	}
}

func TestLegacyReadError(t *testing.T) {
	t.Parallel()
	repo, cfg := newLegacyFlagsRepository(t)
	require.NoError(t, os.Remove(cfg.Paths.SuspendFlagsDirLegacy))
	require.NoError(t, os.WriteFile(cfg.Paths.SuspendFlagsDirLegacy, nil, 0o600))
	_, err := repo.IsSuspended(context.Background(), "alpha")
	require.Error(t, err)
	_, _, err = repo.List(context.Background(), persis.DAGListOptions{})
	require.Error(t, err)
	require.Error(t, repo.MigrateSuspensionState(context.Background()))
	assert.False(t, errors.Is(err, os.ErrNotExist))
}

func TestExplicitFlagsIgnoreLegacy(t *testing.T) {
	t.Parallel()
	_, cfg := newLegacyFlagsRepository(t)
	legacyFlag := filepath.Join(cfg.Paths.SuspendFlagsDirLegacy, "alpha.suspend")
	require.NoError(t, os.WriteFile(legacyFlag, nil, 0o600))
	cfg.Paths.SuspendFlagsDirLegacy = ""
	repo, err := persisfile.NewDAGRepository(cfg, persisfile.WithDAGSkipExamples(true))
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, repo.MigrateSuspensionState(ctx))
	suspended, err := repo.IsSuspended(ctx, "alpha")
	require.NoError(t, err)
	assert.False(t, suspended)
	require.NoError(t, repo.SetSuspended(ctx, "alpha", true))
	require.NoError(t, repo.SetSuspended(ctx, "alpha", false))
	assert.FileExists(t, legacyFlag)
}

func newLegacyFlagsRepository(t *testing.T) (*persis.DAGRepository, *config.Config) {
	t.Helper()
	home := t.TempDir()
	data := filepath.Join(home, "data")
	cfg := suspendFlagsTestConfig(home, data, filepath.Join(data, "suspend"), filepath.Join(home, "suspend"))
	require.NoError(t, os.MkdirAll(data, 0o750))
	require.NoError(t, os.MkdirAll(cfg.Paths.SuspendFlagsDirLegacy, 0o750))
	repo, err := persisfile.NewDAGRepository(cfg, persisfile.WithDAGSkipExamples(true))
	require.NoError(t, err)
	return repo, cfg
}
