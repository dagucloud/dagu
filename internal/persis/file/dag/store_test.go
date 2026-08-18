// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file/dag/dagindex"
	"github.com/dagucloud/dagu/v2/internal/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	// Register executor capabilities for testing.
	// In production, this is done by runtime/builtin init functions.
	for _, t := range []string{"", "shell", "command"} {
		registry.RegisterExecutorCapabilities(t, registry.ExecutorCapabilities{
			Command: true, MultipleCommands: true, Script: true, Shell: true,
		})
	}
	os.Exit(m.Run())
}

func newRepository(baseDir string, opts ...Option) *persis.DAGRepository {
	store := NewStore(baseDir, opts...)
	return persis.NewDAGRepository(store, persis.DAGRepositoryOptions{
		BaseConfigPath:         store.baseConfigPath,
		WorkspaceBaseConfigDir: store.workspaceBaseConfigDir,
	})
}

func TestStore(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-suspend-checker")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	dr := NewStore(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	suspended, err := dr.IsSuspended(ctx, "test")
	require.NoError(t, err)
	require.False(t, suspended)

	err = dr.SetSuspended(ctx, "test", true)
	require.NoError(t, err)

	suspended, err = dr.IsSuspended(ctx, "test")
	require.NoError(t, err)
	require.True(t, suspended)
}

func TestStoreReturnsSuspendFlagReadErrors(t *testing.T) {
	baseDir := t.TempDir()
	flagsPath := filepath.Join(baseDir, "flags")
	require.NoError(t, os.WriteFile(flagsPath, []byte("not a directory"), 0600))

	store := NewStore(
		baseDir,
		WithFlagsBaseDir(flagsPath),
		WithSkipExamples(true),
	)

	_, err := store.IsSuspended(context.Background(), "test")
	require.Error(t, err)

	_, err = store.Catalog(context.Background())
	require.Error(t, err)
}

func TestListDAGsInSubdirectories(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-list-subdirs")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Create DAG files in different directory levels
	// Root level DAG
	rootDAG := `name: root-dag
steps:
  - name: step1
    run: echo "root"`
	err := os.WriteFile(filepath.Join(tmpDir, "root-dag.yaml"), []byte(rootDAG), 0600)
	require.NoError(t, err)

	// Create subdirectory and DAG
	subDir := filepath.Join(tmpDir, "subdir")
	err = os.MkdirAll(subDir, 0750)
	require.NoError(t, err)

	subDAG := `name: sub-dag
steps:
  - name: step1
    run: echo "sub"`
	err = os.WriteFile(filepath.Join(subDir, "sub-dag.yaml"), []byte(subDAG), 0600)
	require.NoError(t, err)

	// Create nested subdirectory and DAG
	nestedDir := filepath.Join(tmpDir, "subdir", "nested")
	err = os.MkdirAll(nestedDir, 0750)
	require.NoError(t, err)

	nestedDAG := `name: nested-dag
steps:
  - name: step1
    run: echo "nested"`
	err = os.WriteFile(filepath.Join(nestedDir, "nested-dag.yaml"), []byte(nestedDAG), 0600)
	require.NoError(t, err)

	// List all DAGs
	opts := persis.DAGListOptions{}
	result, errList, err := store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)

	// Should find only 1 DAG (the root-level one)
	require.Len(t, result.Items, 1, "Should only find DAGs in root directory, not subdirectories")

	// Verify only the root DAG is found
	require.Equal(t, "root-dag", result.Items[0].Name, "Should only find root-dag")
}

func TestRecursiveDiscoverySupportsNestedDAGOperations(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "team", "services"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".hidden"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "workspaces", "ops"), 0750))

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "root.yaml"), []byte(`
name: root
steps:
  - run: echo root
`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "team", "services", "nested.yaml"), []byte(`
name: nested-effective-name
labels:
  - team=platform
steps:
  - run: echo needle
`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".hidden", "ignored.yaml"), []byte(`
name: ignored-hidden
steps:
  - run: echo needle
`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "workspaces", "ops", "ignored.yaml"), []byte(`
name: ignored-workspace
steps:
  - run: echo needle
`), 0600))

	store := newRepository(tmpDir, WithSkipExamples(true), WithRecursiveDiscovery(true))
	ctx := context.Background()

	result, errs, err := store.List(ctx, persis.DAGListOptions{})
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Len(t, result.Items, 2)
	assert.ElementsMatch(t, []string{"root", "nested-effective-name"}, []string{
		result.Items[0].Name,
		result.Items[1].Name,
	})

	nestedSpec, err := store.GetSpec(ctx, "nested")
	require.NoError(t, err)
	assert.Contains(t, nestedSpec, "nested-effective-name")

	search, errs, err := store.SearchCursor(ctx, persis.DAGSearchOptions{
		Query:      "needle",
		Limit:      10,
		MatchLimit: 10,
	})
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Len(t, search.Items, 1)
	assert.Equal(t, "nested", search.Items[0].FileName)

	labels, errs, err := store.LabelList(ctx)
	require.NoError(t, err)
	require.Empty(t, errs)
	assert.Contains(t, labels, "team=platform")

	require.NoError(t, store.Rename(ctx, "nested", "renamed"))
	assert.NoFileExists(t, filepath.Join(tmpDir, "renamed.yaml"))
	assert.FileExists(t, filepath.Join(tmpDir, "team", "services", "renamed.yaml"))
	require.NoError(t, store.UpdateSpec(ctx, "renamed", []byte(`
name: nested-effective-name
steps:
  - run: echo updated
`)))
	updatedSpec, err := store.GetSpec(ctx, "renamed")
	require.NoError(t, err)
	assert.Contains(t, updatedSpec, "updated")

	require.NoError(t, store.Create(ctx, "created", []byte(`
name: created
steps:
  - run: echo created
`)))
	assert.FileExists(t, filepath.Join(tmpDir, "created.yaml"))

	require.NoError(t, store.Delete(ctx, "renamed"))
	assert.NoFileExists(t, filepath.Join(tmpDir, "team", "services", "renamed.yaml"))
}

func TestRecursiveSearchCursorPaginatesByFileName(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "a"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "b"), 0750))
	for path, name := range map[string]string{
		filepath.Join(tmpDir, "a", "zebra.yaml"): "zebra",
		filepath.Join(tmpDir, "b", "apple.yaml"): "apple",
	} {
		require.NoError(t, os.WriteFile(path, fmt.Appendf(nil, `
name: %s
steps:
  - run: echo needle
`, name), 0600))
	}

	store := newRepository(tmpDir, WithSkipExamples(true), WithRecursiveDiscovery(true))
	opts := persis.DAGSearchOptions{Query: "needle", Limit: 1, MatchLimit: 1}

	first, errs, err := store.SearchCursor(context.Background(), opts)
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Len(t, first.Items, 1)
	require.Equal(t, "apple", first.Items[0].FileName)
	require.True(t, first.HasMore)
	require.NotEmpty(t, first.NextCursor)

	opts.Cursor = first.NextCursor
	second, errs, err := store.SearchCursor(context.Background(), opts)
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Len(t, second.Items, 1)
	require.Equal(t, "zebra", second.Items[0].FileName)
	require.False(t, second.HasMore)
}

func TestRecursiveDiscoveryExcludesAndRecoversConflicts(t *testing.T) {
	tmpDir := t.TempDir()
	for _, dir := range []string{"a", "b", "c"} {
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, dir), 0750))
	}
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a", "shared.yaml"), []byte(`
name: one
steps:
  - run: echo a
`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "b", "shared.yml"), []byte(`
name: two
steps:
  - run: echo b
`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "c", "unique.yaml"), []byte(`
name: one
steps:
  - run: echo c
`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "safe.yaml"), []byte(`
name: safe
steps:
  - run: echo safe
`), 0600))

	store := newRepository(tmpDir, WithSkipExamples(true), WithRecursiveDiscovery(true))
	ctx := context.Background()

	result, errs, err := store.List(ctx, persis.DAGListOptions{})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "safe", result.Items[0].Name)
	assert.Equal(t, []string{
		`duplicate DAG file name "shared": a/shared.yaml, b/shared.yml`,
		`duplicate DAG name "one": a/shared.yaml, c/unique.yaml`,
	}, errs)

	_, err = store.GetSpec(ctx, "shared")
	assert.ErrorIs(t, err, persis.ErrDAGNotFound)
	_, err = store.GetSpec(ctx, "a/shared")
	require.NoError(t, err)

	err = store.Create(ctx, "shared", []byte("steps: []\n"))
	assert.ErrorIs(t, err, persis.ErrDAGAlreadyExists)

	require.NoError(t, store.Delete(ctx, "b/shared"))
	require.NoError(t, store.Delete(ctx, "c/unique"))

	result, errs, err = store.List(ctx, persis.DAGListOptions{})
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Len(t, result.Items, 2)
	assert.ElementsMatch(t, []string{"one", "safe"}, []string{
		result.Items[0].Name,
		result.Items[1].Name,
	})
}

func TestGetMetadata(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-get-metadata")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Test successful metadata retrieval
	dagContent := `name: test-dag
labels: ["tag1", "tag2"]
steps:
  - name: step1
    run: echo "hello"`
	err := os.WriteFile(filepath.Join(tmpDir, "test-dag.yaml"), []byte(dagContent), 0600)
	require.NoError(t, err)

	dag, err := store.GetMetadata(ctx, "test-dag")
	require.NoError(t, err)
	require.NotNil(t, dag)
	assert.Equal(t, "test-dag", dag.Name)
	assert.Equal(t, []string{"tag1", "tag2"}, dag.Labels.Strings())

	// Test DAG not found
	_, err = store.GetMetadata(ctx, "non-existent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to locate DAG non-existent")
}

func TestGetMetadata_InlineSchemaParamsPreserveMetadata(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-get-metadata-inline-schema")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	dagContent := `name: inline-schema-dag
params:
  type: object
  properties:
    batch_size:
      type: integer
      default: 10
    debug:
      type: boolean
      default: false
steps:
  - name: step1
    run: echo "hello"`
	err := os.WriteFile(filepath.Join(tmpDir, "inline-schema-dag.yaml"), []byte(dagContent), 0600)
	require.NoError(t, err)

	dag, err := store.GetMetadata(ctx, "inline-schema-dag")
	require.NoError(t, err)
	require.Len(t, dag.ParamDefs, 2)
	assert.Equal(t, `batch_size="10" debug="false"`, dag.DefaultParams)
}

func TestGetMetadataCachesSharedSymlinkTargetsByEntry(t *testing.T) {
	dagDir := t.TempDir()
	targetPath := filepath.Join(t.TempDir(), "shared.yaml")
	require.NoError(t, os.WriteFile(targetPath, []byte("steps: []\n"), 0600))
	for _, name := range []string{"first", "second"} {
		if err := os.Symlink(targetPath, filepath.Join(dagDir, name+".yaml")); err != nil {
			t.Skipf("symlink creation is unavailable: %v", err)
		}
	}

	store := newRepository(
		dagDir,
		WithFileCache(fileutil.NewCache[*ir.DAG]("dag_definition", 16, time.Hour)),
		WithSkipExamples(true),
		WithSymlinks(true),
	)
	ctx := context.Background()

	first, err := store.GetMetadata(ctx, "first")
	require.NoError(t, err)
	second, err := store.GetMetadata(ctx, "second")
	require.NoError(t, err)
	assert.Equal(t, "first", first.Name)
	assert.Equal(t, "second", second.Name)
}

func TestGetDetails(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-get-details")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Test successful details retrieval
	dagContent := `name: detailed-dag
schedule: "0 1 * * *"
steps:
  - name: step1
    run: echo "detailed"`
	err := os.WriteFile(filepath.Join(tmpDir, "detailed-dag.yaml"), []byte(dagContent), 0600)
	require.NoError(t, err)

	dag, err := store.GetDetails(ctx, "detailed-dag", persis.DAGLoadOptions{})
	require.NoError(t, err)
	require.NotNil(t, dag)
	assert.Equal(t, "detailed-dag", dag.Name)
	require.Len(t, dag.Schedule, 1)
	assert.Equal(t, "0 1 * * *", dag.Schedule[0].Expression)

	// Test DAG not found
	_, err = store.GetDetails(ctx, "non-existent", persis.DAGLoadOptions{})
	require.Error(t, err)
	assert.ErrorIs(t, err, persis.ErrDAGNotFound)
	assert.Contains(t, err.Error(), "failed to locate DAG non-existent")

	// A DAG that builds with errors fails unless the caller tolerates them, in
	// which case the partially built DAG is returned carrying the errors.
	buildErrContent := `name: build-error-dag
steps:
  - name: step1
    run: echo "build error"
    depends: [missing-step]`
	err = os.WriteFile(filepath.Join(tmpDir, "build-error-dag.yaml"), []byte(buildErrContent), 0600)
	require.NoError(t, err)

	_, err = store.GetDetails(ctx, "build-error-dag", persis.DAGLoadOptions{})
	require.Error(t, err)

	dag, err = store.GetDetails(ctx, "build-error-dag", persis.DAGLoadOptions{AllowBuildErrors: true})
	require.NoError(t, err)
	require.NotNil(t, dag)
	assert.Equal(t, "build-error-dag", dag.Name)
	assert.NotEmpty(t, dag.BuildErrors)

	// Dots remain part of the DAG's lookup identity.
	dottedDAGContent := `name: dagu.update-cloud-image
steps:
  - name: step1
    run: echo "dotted"`
	nestedDir := filepath.Join(tmpDir, "nested")
	require.NoError(t, os.Mkdir(nestedDir, 0750))
	err = os.WriteFile(filepath.Join(nestedDir, "dagu.update-cloud-image.yaml"), []byte(dottedDAGContent), 0600)
	require.NoError(t, err)

	recursiveStore := newRepository(tmpDir, WithSkipExamples(true), WithRecursiveDiscovery(true))
	dag, err = recursiveStore.GetDetails(ctx, "dagu.update-cloud-image", persis.DAGLoadOptions{})
	require.NoError(t, err)
	assert.Equal(t, "dagu.update-cloud-image", dag.Name)

	err = recursiveStore.Create(ctx, "dagu.update-cloud-image", []byte(dottedDAGContent))
	require.ErrorIs(t, err, persis.ErrDAGAlreadyExists)
}

func TestStoreGetPreservesDiscoveryError(t *testing.T) {
	basePath := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(basePath, []byte("content"), 0600))
	store := NewStore(basePath, WithRecursiveDiscovery(true), WithSkipExamples(true))

	_, err := store.Get(context.Background(), "example")
	require.Error(t, err)
	assert.NotErrorIs(t, err, persis.ErrDAGNotFound)
}

func TestGetSpec(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-get-spec")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Test successful spec retrieval
	dagContent := `name: spec-dag
steps:
  - name: step1
    run: echo "spec"`
	err := os.WriteFile(filepath.Join(tmpDir, "spec-dag.yaml"), []byte(dagContent), 0600)
	require.NoError(t, err)

	spec, err := store.GetSpec(ctx, "spec-dag")
	require.NoError(t, err)
	assert.Equal(t, dagContent, spec)

	// Test DAG not found
	_, err = store.GetSpec(ctx, "non-existent")
	require.Error(t, err)
	assert.Equal(t, persis.ErrDAGNotFound, err)
}

func TestGetSpecAllowsNestedPathsWithinConfiguredDirectories(t *testing.T) {
	baseDir := t.TempDir()
	nestedDir := filepath.Join(baseDir, "team", "jobs")
	require.NoError(t, os.MkdirAll(nestedDir, 0750))

	const dagContent = "name: nested-dag\nsteps: []\n"
	require.NoError(t, os.WriteFile(filepath.Join(nestedDir, "nested-dag.yaml"), []byte(dagContent), 0600))

	store := newRepository(baseDir, WithSkipExamples(true))
	spec, err := store.GetSpec(context.Background(), "team/jobs/nested-dag")

	require.NoError(t, err)
	assert.Equal(t, dagContent, spec)
}

func TestGetSpecRejectsPathsOutsideConfiguredDirectories(t *testing.T) {
	baseDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.yaml")
	require.NoError(t, os.WriteFile(outsidePath, []byte("name: outside\nsteps: []\n"), 0600))

	store := newRepository(baseDir, WithSkipExamples(true))
	relativePath, err := filepath.Rel(baseDir, outsidePath)
	require.NoError(t, err)

	for _, path := range []string{outsidePath, relativePath} {
		_, err := store.GetSpec(context.Background(), path)
		assert.ErrorIs(t, err, persis.ErrDAGNotFound)
	}
}

func TestGetSpecRejectsSymlinkOutsideConfiguredDirectories(t *testing.T) {
	baseDir := t.TempDir()
	outsideDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(outsideDir, "outside.yaml"),
		[]byte("name: outside\nsteps: []\n"),
		0600,
	))
	if err := os.Symlink(outsideDir, filepath.Join(baseDir, "linked")); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	store := newRepository(baseDir, WithSkipExamples(true))
	_, err := store.GetSpec(context.Background(), "linked/outside.yaml")

	assert.ErrorIs(t, err, persis.ErrDAGNotFound)
}

func TestExternalDAGFileSymlink(t *testing.T) {
	baseDir := t.TempDir()
	targetDir := t.TempDir()
	const dagContent = "steps:\n  - run: echo external\n"
	targetPath := filepath.Join(targetDir, "source.yaml")
	require.NoError(t, os.WriteFile(targetPath, []byte(dagContent), 0600))
	linkPath := filepath.Join(baseDir, "external.yaml")
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	t.Run("Disabled", func(t *testing.T) {
		store := newRepository(baseDir, WithSkipExamples(true))
		result, issues, err := store.List(context.Background(), persis.DAGListOptions{})
		require.NoError(t, err)
		require.Empty(t, result.Items)
		require.Len(t, issues, 1)
		assert.Contains(t, issues[0], "dag_discovery.symlinks")

		_, err = store.GetSpec(context.Background(), "external")
		assert.ErrorIs(t, err, persis.ErrDAGNotFound)
	})

	t.Run("Enabled", func(t *testing.T) {
		store := newRepository(baseDir, WithSkipExamples(true), WithSymlinks(true))
		result, issues, err := store.List(context.Background(), persis.DAGListOptions{})
		require.NoError(t, err)
		require.Empty(t, issues)
		require.Len(t, result.Items, 1)
		assert.Equal(t, "external", result.Items[0].Name)
		assert.Empty(t, result.Items[0].BuildErrors)

		metadata, err := store.GetMetadata(context.Background(), "external")
		require.NoError(t, err)
		assert.Equal(t, "external", metadata.Name)

		spec, err := store.GetSpec(context.Background(), "external")
		require.NoError(t, err)
		assert.Equal(t, dagContent, spec)
		details, err := store.GetDetails(context.Background(), "external", persis.DAGLoadOptions{})
		require.NoError(t, err)
		assert.Equal(t, "external", details.Name)

		assert.ErrorIs(t, store.UpdateSpec(context.Background(), "external", []byte(dagContent)), persis.ErrDAGReadOnly)
		assert.ErrorIs(t, store.Delete(context.Background(), "external"), persis.ErrDAGReadOnly)
		assert.ErrorIs(t, store.Rename(context.Background(), "external", "renamed"), persis.ErrDAGReadOnly)

		_, err = os.Lstat(linkPath)
		require.NoError(t, err)
		content, err := os.ReadFile(targetPath)
		require.NoError(t, err)
		assert.Equal(t, dagContent, string(content))
	})
}

func TestListRebuildsIndexAfterSymlinkRepoint(t *testing.T) {
	baseDir := t.TempDir()
	targetDir := t.TempDir()
	firstPath := filepath.Join(targetDir, "first.yaml")
	secondPath := filepath.Join(targetDir, "second.yaml")
	firstSpec := []byte("description: first\nsteps:\n  - run: echo one\n")
	secondSpec := []byte("description: other\nsteps:\n  - run: echo two\n")
	require.Len(t, secondSpec, len(firstSpec))
	require.NoError(t, os.WriteFile(firstPath, firstSpec, 0600))
	require.NoError(t, os.WriteFile(secondPath, secondSpec, 0600))
	modTime := time.Now().Add(-time.Hour).Truncate(time.Second)
	require.NoError(t, os.Chtimes(firstPath, modTime, modTime))
	require.NoError(t, os.Chtimes(secondPath, modTime, modTime))

	linkPath := filepath.Join(baseDir, "linked.yaml")
	if err := os.Symlink(firstPath, linkPath); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	store := newRepository(baseDir, WithSkipExamples(true), WithSymlinks(true))
	result, issues, err := store.List(context.Background(), persis.DAGListOptions{})
	require.NoError(t, err)
	require.Empty(t, issues)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "linked", result.Items[0].Name)
	assert.Equal(t, "first", result.Items[0].Description)

	require.NoError(t, os.Remove(linkPath))
	require.NoError(t, os.Symlink(secondPath, linkPath))
	result, issues, err = store.List(context.Background(), persis.DAGListOptions{})
	require.NoError(t, err)
	require.Empty(t, issues)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "linked", result.Items[0].Name)
	assert.Equal(t, "other", result.Items[0].Description)
}

func TestCatalogIncludingSearchPaths(t *testing.T) {
	baseDir := t.TempDir()
	searchDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "base-dag.yaml"), []byte("name: base-dag\nsteps: []\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(searchDir, "alt-dag.yaml"), []byte("name: alt-dag\nsteps: []\n"), 0600))

	store := NewStore(baseDir, WithSearchPaths([]string{searchDir}), WithSkipExamples(true))
	ctx := context.Background()

	baseOnly, err := store.Catalog(ctx)
	require.NoError(t, err)
	baseNames := make([]string, 0, len(baseOnly.Items))
	for _, item := range baseOnly.Items {
		baseNames = append(baseNames, item.ID)
	}
	require.Equal(t, []string{"base-dag"}, baseNames)

	combined, err := store.CatalogIncludingSearchPaths(ctx)
	require.NoError(t, err)
	combinedNames := make([]string, 0, len(combined.Items))
	for _, item := range combined.Items {
		combinedNames = append(combinedNames, item.ID)
	}
	require.ElementsMatch(t, []string{"base-dag", "alt-dag"}, combinedNames)

	// The alternate-directory entry must report a correct location.
	var altDAG *ir.DAG
	for _, item := range combined.Items {
		if item.ID == "alt-dag" {
			altDAG = item.DAG
		}
	}
	require.NotNil(t, altDAG)
	require.Equal(t, filepath.Join(searchDir, "alt-dag.yaml"), altDAG.Location)
}

func TestGetSpecAllowsExplicitSearchPaths(t *testing.T) {
	baseDir := t.TempDir()
	searchDir := t.TempDir()
	const dagContent = "name: searched\nsteps: []\n"
	searchPath := filepath.Join(searchDir, "searched.yaml")
	require.NoError(t, os.WriteFile(searchPath, []byte(dagContent), 0600))

	store := newRepository(baseDir, WithSearchPaths([]string{searchDir}), WithSkipExamples(true))
	spec, err := store.GetSpec(context.Background(), searchPath)

	require.NoError(t, err)
	assert.Equal(t, dagContent, spec)
}

func TestCreate(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-create")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Test successful creation
	dagContent := `name: new-dag
steps:
  - name: step1
    run: echo "new"`
	err := store.Create(ctx, "new-dag", []byte(dagContent))
	require.NoError(t, err)

	// Verify file was created
	filePath := filepath.Join(tmpDir, "new-dag.yaml")
	assert.True(t, fileExists(filePath))

	// Verify content
	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, dagContent, string(content))

	// Test creating duplicate DAG
	err = store.Create(ctx, "new-dag", []byte(dagContent))
	require.Error(t, err)
	assert.Equal(t, persis.ErrDAGAlreadyExists, err)
}

func TestGenerateFilePathPreventsTraversal(t *testing.T) {
	baseDir := filepath.FromSlash("/base/dir")
	store := NewStore(baseDir, WithSkipExamples(true))

	tests := []struct {
		name  string
		input string
	}{
		{"parent traversal", "../../tmp/pwned"},
		{"single parent", "../escape"},
		{"subdirectory", "foo/bar"},
		{"deep traversal", "../../../etc/malicious"},
		{"empty string", ""},
		{"dot dot", ".."},
		{"normal name", "my-dag"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := store.generateFilePath(tc.input)
			assert.Equal(t, filepath.Clean(baseDir), filepath.Dir(result),
				"generated path must be directly inside baseDir")
		})
	}
}

func TestSearchCursorFailsWhenBaseDirIsNotReadableDirectory(t *testing.T) {
	basePath := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(basePath, []byte("x"), 0600))

	store := newRepository(basePath, WithSkipExamples(true))

	result, errs, err := store.SearchCursor(context.Background(), persis.DAGSearchOptions{
		Query:      "needle",
		Limit:      1,
		MatchLimit: 1,
	})
	require.Nil(t, result)
	require.Nil(t, errs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read DAGs directory")
}

func TestSearchCursorFiltersByLabels(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-search-cursor-labels")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "ops-dag", []byte(`name: ops-dag
labels:
  - workspace=ops
steps:
  - name: step1
    run: echo needle
`)))
	require.NoError(t, store.Create(ctx, "prod-dag", []byte(`name: prod-dag
labels:
  - workspace=prod
steps:
  - name: step1
    run: echo needle
`)))
	require.NoError(t, store.Create(ctx, "bad-workspace-dag", []byte(`name: bad-workspace-dag
labels:
  - workspace=bad/name
steps:
  - name: step1
    run: echo needle
`)))

	result, errs, err := store.SearchCursor(ctx, persis.DAGSearchOptions{
		Query:      "needle",
		Limit:      10,
		MatchLimit: 1,
		Labels:     []string{"workspace=ops"},
	})
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "ops-dag", result.Items[0].FileName)
}

func TestListDAGsFiltersByWorkspaceBeforePagination(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-list-workspace-filter")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "aaa-global", []byte(`name: aaa-global
steps:
  - name: step1
    run: echo global
`)))
	require.NoError(t, store.Create(ctx, "bbb-ops", []byte(`name: bbb-ops
labels:
  - workspace=ops
steps:
  - name: step1
    run: echo ops
`)))
	require.NoError(t, store.Create(ctx, "ccc-prod", []byte(`name: ccc-prod
labels:
  - workspace=prod
steps:
  - name: step1
    run: echo prod
`)))

	firstPage := pagination.NewPaginator(1, 1)
	result, errs, err := store.List(ctx, persis.DAGListOptions{
		Paginator: &firstPage,
		WorkspaceFilter: &workspace.WorkspaceFilter{
			Enabled:           true,
			Workspaces:        []string{"ops"},
			IncludeUnlabelled: true,
		},
	})
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Equal(t, 2, result.TotalCount)
	require.True(t, result.HasNextPage)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "aaa-global", result.Items[0].Name)

	secondPage := pagination.NewPaginator(2, 1)
	result, errs, err = store.List(ctx, persis.DAGListOptions{
		Paginator: &secondPage,
		WorkspaceFilter: &workspace.WorkspaceFilter{
			Enabled:           true,
			Workspaces:        []string{"ops"},
			IncludeUnlabelled: true,
		},
	})
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Equal(t, 2, result.TotalCount)
	require.False(t, result.HasNextPage)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "bbb-ops", result.Items[0].Name)

	workspaceOnly := pagination.NewPaginator(1, 10)
	result, errs, err = store.List(ctx, persis.DAGListOptions{
		Paginator: &workspaceOnly,
		WorkspaceFilter: &workspace.WorkspaceFilter{
			Enabled:    true,
			Workspaces: []string{"ops"},
		},
	})
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Equal(t, 1, result.TotalCount)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "bbb-ops", result.Items[0].Name)
}

func TestSearchCursorFiltersByWorkspace(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-search-cursor-workspace")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "global-dag", []byte(`name: global-dag
steps:
  - name: step1
    run: echo needle
`)))
	require.NoError(t, store.Create(ctx, "ops-dag", []byte(`name: ops-dag
labels:
  - workspace=ops
steps:
  - name: step1
    run: echo needle
`)))
	require.NoError(t, store.Create(ctx, "prod-dag", []byte(`name: prod-dag
labels:
  - workspace=prod
steps:
  - name: step1
    run: echo needle
`)))

	result, errs, err := store.SearchCursor(ctx, persis.DAGSearchOptions{
		Query:      "needle",
		Limit:      10,
		MatchLimit: 1,
		WorkspaceFilter: &workspace.WorkspaceFilter{
			Enabled:           true,
			Workspaces:        []string{"ops"},
			IncludeUnlabelled: true,
		},
	})
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Len(t, result.Items, 2)
	assert.Equal(t, []string{"global-dag", "ops-dag"}, []string{
		result.Items[0].FileName,
		result.Items[1].FileName,
	})
	assert.Equal(t, []string{"", "ops"}, []string{
		result.Items[0].Workspace,
		result.Items[1].Workspace,
	})

	result, errs, err = store.SearchCursor(ctx, persis.DAGSearchOptions{
		Query:      "needle",
		Limit:      10,
		MatchLimit: 1,
		WorkspaceFilter: &workspace.WorkspaceFilter{
			Enabled:    true,
			Workspaces: []string{"ops"},
		},
	})
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "ops-dag", result.Items[0].FileName)
	assert.Equal(t, "ops", result.Items[0].Workspace)
}

func TestSearchMatchesFiltersByLabels(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-search-matches-labels")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "ops-dag", []byte(`name: ops-dag
labels:
  - workspace=ops
steps:
  - name: step1
    run: echo needle
  - name: step2
    run: echo needle
`)))

	result, err := store.SearchMatches(ctx, "ops-dag", persis.DAGMatchSearchOptions{
		Query:  "needle",
		Limit:  1,
		Labels: []string{"workspace=ops"},
	})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.True(t, result.HasMore)

	next, err := store.SearchMatches(ctx, "ops-dag", persis.DAGMatchSearchOptions{
		Query:  "needle",
		Limit:  1,
		Labels: []string{"workspace=ops"},
		Cursor: result.NextCursor,
	})
	require.NoError(t, err)
	require.Len(t, next.Items, 1)

	filtered, err := store.SearchMatches(ctx, "ops-dag", persis.DAGMatchSearchOptions{
		Query:  "needle",
		Limit:  1,
		Labels: []string{"workspace=prod"},
	})
	require.NoError(t, err)
	require.Empty(t, filtered.Items)

	_, err = store.SearchMatches(ctx, "ops-dag", persis.DAGMatchSearchOptions{
		Query:  "needle",
		Limit:  1,
		Labels: []string{"workspace=prod"},
		Cursor: result.NextCursor,
	})
	assert.ErrorIs(t, err, pagination.ErrInvalidCursor)
}

func TestUpdateSpec(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-update-spec")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Create initial DAG
	initialContent := `name: update-dag
steps:
  - name: step1
    run: echo "initial"`
	err := store.Create(ctx, "update-dag", []byte(initialContent))
	require.NoError(t, err)

	// Test successful update
	updatedContent := `name: update-dag
steps:
  - name: step1
    run: echo "updated"`
	err = store.UpdateSpec(ctx, "update-dag", []byte(updatedContent))
	require.NoError(t, err)

	// Verify content was updated
	spec, err := store.GetSpec(ctx, "update-dag")
	require.NoError(t, err)
	assert.Equal(t, updatedContent, spec)

	// Test updating non-existent DAG
	err = store.UpdateSpec(ctx, "non-existent", []byte(updatedContent))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to locate DAG non-existent")

	// Test updating with invalid spec
	invalidContent := `invalid: yaml: content`
	err = store.UpdateSpec(ctx, "update-dag", []byte(invalidContent))
	require.Error(t, err)
}

func TestDelete(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-delete")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Create DAG to delete
	dagContent := `name: delete-dag
steps:
  - name: step1
    run: echo "delete"`
	err := store.Create(ctx, "delete-dag", []byte(dagContent))
	require.NoError(t, err)

	// Verify file exists
	filePath := filepath.Join(tmpDir, "delete-dag.yaml")
	assert.True(t, fileExists(filePath))

	// Test successful deletion
	err = store.Delete(ctx, "delete-dag")
	require.NoError(t, err)

	// Verify file was deleted
	assert.False(t, fileExists(filePath))

	// Test deleting non-existent DAG (should not error)
	err = store.Delete(ctx, "non-existent")
	require.NoError(t, err)

	targetDir := filepath.Join(tmpDir, "targets")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	targetPath := filepath.Join(targetDir, "linked-delete.yaml")
	require.NoError(t, os.WriteFile(targetPath, []byte(dagContent), 0600))
	linkPath := filepath.Join(tmpDir, "linked-delete.yaml")
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	require.NoError(t, store.Delete(ctx, "linked-delete"))
	_, err = os.Lstat(linkPath)
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.FileExists(t, targetPath)
}

func TestRename(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-rename")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Create DAG to rename
	dagContent := `name: old-name
steps:
  - name: step1
    run: echo "rename"`
	err := store.Create(ctx, "old-name", []byte(dagContent))
	require.NoError(t, err)

	// Test successful rename
	err = store.Rename(ctx, "old-name", "new-name")
	require.NoError(t, err)

	// Verify old file doesn't exist
	oldPath := filepath.Join(tmpDir, "old-name.yaml")
	assert.False(t, fileExists(oldPath))

	// Verify new file exists
	newPath := filepath.Join(tmpDir, "new-name.yaml")
	assert.True(t, fileExists(newPath))

	// Verify content is preserved
	spec, err := store.GetSpec(ctx, "new-name")
	require.NoError(t, err)
	assert.Equal(t, dagContent, spec)

	// Test renaming non-existent DAG
	err = store.Rename(ctx, "non-existent", "new-name")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to locate DAG non-existent")

	// Test renaming to existing name
	err = store.Create(ctx, "another-dag", []byte(dagContent))
	require.NoError(t, err)
	err = store.Rename(ctx, "new-name", "another-dag")
	require.Error(t, err)
	assert.Equal(t, persis.ErrDAGAlreadyExists, err)

	targetDir := filepath.Join(tmpDir, "targets")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	targetPath := filepath.Join(targetDir, "linked-target.yaml")
	require.NoError(t, os.WriteFile(targetPath, []byte(dagContent), 0600))
	linkPath := filepath.Join(tmpDir, "linked-old.yaml")
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	require.NoError(t, store.Rename(ctx, "linked-old", "linked-new"))
	_, err = os.Lstat(linkPath)
	assert.ErrorIs(t, err, os.ErrNotExist)
	renamedPath := filepath.Join(tmpDir, "linked-new.yaml")
	info, err := os.Lstat(renamedPath)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink)
	assert.FileExists(t, targetPath)
}

func TestGrep(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-grep")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Create DAGs with different content
	dag1Content := `name: search-dag-1
steps:
  - name: step1
    run: echo "search pattern here"`
	err := store.Create(ctx, "search-dag-1", []byte(dag1Content))
	require.NoError(t, err)

	dag2Content := `name: search-dag-2
steps:
  - name: step1
    run: echo "no match here"`
	err = store.Create(ctx, "search-dag-2", []byte(dag2Content))
	require.NoError(t, err)

	dag3Content := `name: search-dag-3
description: "This contains the search pattern"
steps:
  - name: step1
    run: echo "hello"`
	err = store.Create(ctx, "search-dag-3", []byte(dag3Content))
	require.NoError(t, err)

	// Test successful grep
	results, errList, err := store.Grep(ctx, "search pattern")
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, results, 2)

	// Verify correct DAGs are found
	foundNames := make(map[string]bool)
	for _, result := range results {
		foundNames[result.Name] = true
		assert.NotEmpty(t, result.Matches)
	}
	assert.True(t, foundNames["search-dag-1"])
	assert.True(t, foundNames["search-dag-3"])
	assert.False(t, foundNames["search-dag-2"])

	// Test grep with no pattern (should return empty)
	results, errList, err = store.Grep(ctx, "")
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Empty(t, results)

	// Test grep with non-matching pattern
	results, errList, err = store.Grep(ctx, "nonexistentpattern")
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Empty(t, results)
}

func TestLabelList(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-label-list")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Create DAGs with different labels
	dag1Content := `name: label-dag-1
labels: ["web", "daily"]
steps:
  - name: step1
    run: echo "tag1"`
	err := store.Create(ctx, "label-dag-1", []byte(dag1Content))
	require.NoError(t, err)

	dag2Content := `name: label-dag-2
labels: ["batch", "daily", "weekly"]
steps:
  - name: step1
    run: echo "tag2"`
	err = store.Create(ctx, "label-dag-2", []byte(dag2Content))
	require.NoError(t, err)

	dag3Content := `name: label-dag-3
steps:
  - name: step1
    run: echo "no labels"`
	err = store.Create(ctx, "label-dag-3", []byte(dag3Content))
	require.NoError(t, err)

	// Test label list
	labels, errList, err := store.LabelList(ctx)
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, labels, 4)

	// Verify all unique labels are present
	tagSet := make(map[string]bool)
	for _, label := range labels {
		tagSet[label] = true
	}
	assert.True(t, tagSet["web"])
	assert.True(t, tagSet["daily"])
	assert.True(t, tagSet["batch"])
	assert.True(t, tagSet["weekly"])
}

func TestLoadSpec(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-load-spec")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Test valid spec
	validSpec := `name: load-spec-dag
steps:
  - name: step1
    run: echo "load spec"`
	dag, err := store.LoadSpec(ctx, []byte(validSpec), "", persis.DAGLoadOptions{})
	require.NoError(t, err)
	require.NotNil(t, dag)
	assert.Equal(t, "load-spec-dag", dag.Name)

	// Test invalid spec
	invalidSpec := `invalid: yaml: content: [unclosed`
	_, err = store.LoadSpec(ctx, []byte(invalidSpec), "", persis.DAGLoadOptions{})
	require.Error(t, err)

	// An explicit name takes precedence over the one the spec declares.
	dag, err = store.LoadSpec(ctx, []byte(validSpec), "explicit-name", persis.DAGLoadOptions{})
	require.NoError(t, err)
	assert.Equal(t, "explicit-name", dag.Name)

	// A spec that builds with errors fails unless the caller tolerates them, in
	// which case the partially built DAG is returned carrying the errors.
	buildErrSpec := `steps:
  - name: step1
    run: echo "load spec"
    depends: [missing-step]`
	_, err = store.LoadSpec(ctx, []byte(buildErrSpec), "build-error-dag", persis.DAGLoadOptions{})
	require.Error(t, err)

	dag, err = store.LoadSpec(ctx, []byte(buildErrSpec), "build-error-dag", persis.DAGLoadOptions{AllowBuildErrors: true})
	require.NoError(t, err)
	require.NotNil(t, dag)
	assert.Equal(t, "build-error-dag", dag.Name)
	assert.NotEmpty(t, dag.BuildErrors)
}

func TestLoadSpecWithBaseGraphType(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-load-spec-base-graph")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	baseConfig := filepath.Join(tmpDir, "base.yaml")
	require.NoError(t, os.WriteFile(baseConfig, []byte("type: graph\n"), 0600))

	store := newRepository(tmpDir, WithBaseConfig(baseConfig), WithSkipExamples(true))
	ctx := context.Background()

	dag, err := store.LoadSpec(ctx, []byte(`name: base-graph-dag
steps:
  - name: build
    run: echo build
  - name: test
    run: echo test
    depends: [build]
`), "", persis.DAGLoadOptions{})
	require.NoError(t, err)
	require.Equal(t, ir.TypeGraph, dag.Type)
	require.Len(t, dag.Steps, 2)
	require.Equal(t, []string{"build"}, dag.Steps[1].Depends)
}

func TestLoadSpecWithWorkspaceBaseConfig(t *testing.T) {
	rootDir := t.TempDir()
	dagDir := filepath.Join(rootDir, "dags")
	workspaceConfigDir := filepath.Join(rootDir, "workspaces")
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceConfigDir, "ops"), 0750))

	baseConfig := filepath.Join(rootDir, "base.yaml")
	require.NoError(t, os.WriteFile(baseConfig, []byte(`
env:
  GLOBAL_ONLY: "1"
  SHARED: "global"
log_dir: "/global/logs"
`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceConfigDir, "ops", "base.yaml"), []byte(`
env:
  WORKSPACE_ONLY: "1"
  SHARED: "workspace"
log_dir: "/workspace/logs"
`), 0600))

	store := newRepository(
		dagDir,
		WithBaseConfig(baseConfig),
		WithWorkspaceBaseConfigDir(workspaceConfigDir),
		WithSkipExamples(true),
	)
	ctx := context.Background()

	dag, err := store.LoadSpec(ctx, []byte(`name: workspace-config-dag
labels:
  - workspace=ops
steps:
  - name: step1
    run: echo "hello"
`), "", persis.DAGLoadOptions{})
	require.NoError(t, err)
	assert.Contains(t, dag.Env, "GLOBAL_ONLY=1")
	assert.Contains(t, dag.Env, "WORKSPACE_ONLY=1")
	assert.Contains(t, dag.Env, "SHARED=workspace")
	assert.Equal(t, "/workspace/logs", dag.LogDir)
}

func TestGetMetadataRefreshesCacheWhenBaseConfigChanges(t *testing.T) {
	rootDir := t.TempDir()
	dagDir := filepath.Join(rootDir, "dags")
	require.NoError(t, os.MkdirAll(dagDir, 0750))

	baseConfig := filepath.Join(rootDir, "base.yaml")
	require.NoError(t, os.WriteFile(baseConfig, []byte("type: graph\n"), 0600))

	cache := fileutil.NewCache[*ir.DAG]("dag_definition", 16, time.Hour)
	store := newRepository(
		dagDir,
		WithBaseConfig(baseConfig),
		WithFileCache(cache),
		WithSkipExamples(true),
	)
	ctx := context.Background()

	require.NoError(t, os.WriteFile(filepath.Join(dagDir, "cache-refresh.yaml"), []byte(`name: cache-refresh
steps:
  - name: step1
    run: echo "hello"
`), 0600))

	dag, err := store.GetMetadata(ctx, "cache-refresh")
	require.NoError(t, err)
	require.Equal(t, 1, cache.Size())

	time.Sleep(10 * time.Millisecond)
	require.NoError(t, os.WriteFile(baseConfig, []byte("type: chain\n"), 0600))

	reloaded, err := store.GetMetadata(ctx, "cache-refresh")
	require.NoError(t, err)
	require.NotSame(t, dag, reloaded)
	require.Equal(t, 1, cache.Size())
}

func TestGetMetadataRefreshesCacheWhenWorkspaceBaseConfigChanges(t *testing.T) {
	rootDir := t.TempDir()
	dagDir := filepath.Join(rootDir, "dags")
	workspaceConfigDir := filepath.Join(rootDir, "workspaces")
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceConfigDir, "ops"), 0750))
	require.NoError(t, os.MkdirAll(dagDir, 0750))

	baseConfig := filepath.Join(rootDir, "base.yaml")
	require.NoError(t, os.WriteFile(baseConfig, []byte("type: graph\n"), 0600))
	workspaceBaseConfig := filepath.Join(workspaceConfigDir, "ops", "base.yaml")
	require.NoError(t, os.WriteFile(workspaceBaseConfig, []byte("max_active_steps: 1\n"), 0600))

	cache := fileutil.NewCache[*ir.DAG]("dag_definition", 16, time.Hour)
	store := newRepository(
		dagDir,
		WithBaseConfig(baseConfig),
		WithWorkspaceBaseConfigDir(workspaceConfigDir),
		WithFileCache(cache),
		WithSkipExamples(true),
	)
	ctx := context.Background()

	require.NoError(t, os.WriteFile(filepath.Join(dagDir, "workspace-cache-refresh.yaml"), []byte(`name: workspace-cache-refresh
labels:
  - workspace=ops
steps:
  - name: step1
    run: echo "hello"
`), 0600))

	dag, err := store.GetMetadata(ctx, "workspace-cache-refresh")
	require.NoError(t, err)
	require.Equal(t, 1, cache.Size())

	time.Sleep(10 * time.Millisecond)
	require.NoError(t, os.WriteFile(workspaceBaseConfig, []byte("max_active_steps: 2\n"), 0600))

	reloaded, err := store.GetMetadata(ctx, "workspace-cache-refresh")
	require.NoError(t, err)
	require.NotSame(t, dag, reloaded)
	require.Equal(t, 1, cache.Size())
}

func TestListRebuildsIndexWhenBaseConfigChanges(t *testing.T) {
	rootDir := t.TempDir()
	dagDir := filepath.Join(rootDir, "dags")
	require.NoError(t, os.MkdirAll(dagDir, 0750))

	baseConfig := filepath.Join(rootDir, "base.yaml")
	require.NoError(t, os.WriteFile(baseConfig, []byte("type: graph\n"), 0600))

	store := newRepository(dagDir, WithBaseConfig(baseConfig), WithSkipExamples(true))
	ctx := context.Background()

	require.NoError(t, os.WriteFile(filepath.Join(dagDir, "index-refresh.yaml"), []byte(`name: index-refresh
labels:
  - env=dev
steps:
  - name: step1
    run: echo "hello"
`), 0600))

	result, errList, err := store.List(ctx, persis.DAGListOptions{})
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 1)

	indexPath := filepath.Join(dagDir, dagindex.IndexFileName)
	indexInfoBefore, err := os.Stat(indexPath)
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)
	require.NoError(t, os.WriteFile(baseConfig, []byte("type: chain\n"), 0600))

	result, errList, err = store.List(ctx, persis.DAGListOptions{})
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 1)

	indexInfoAfter, err := os.Stat(indexPath)
	require.NoError(t, err)
	require.True(t, indexInfoAfter.ModTime().After(indexInfoBefore.ModTime()))
}

func TestListRebuildsIndexWhenWorkspaceBaseConfigChanges(t *testing.T) {
	rootDir := t.TempDir()
	dagDir := filepath.Join(rootDir, "dags")
	workspaceConfigDir := filepath.Join(rootDir, "workspaces")
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceConfigDir, "ops"), 0750))
	require.NoError(t, os.MkdirAll(dagDir, 0750))

	baseConfig := filepath.Join(rootDir, "base.yaml")
	require.NoError(t, os.WriteFile(baseConfig, []byte("type: graph\n"), 0600))
	workspaceBaseConfig := filepath.Join(workspaceConfigDir, "ops", "base.yaml")
	require.NoError(t, os.WriteFile(workspaceBaseConfig, []byte("max_active_steps: 1\n"), 0600))

	store := newRepository(
		dagDir,
		WithBaseConfig(baseConfig),
		WithWorkspaceBaseConfigDir(workspaceConfigDir),
		WithSkipExamples(true),
	)
	ctx := context.Background()

	require.NoError(t, os.WriteFile(filepath.Join(dagDir, "workspace-index-refresh.yaml"), []byte(`name: workspace-index-refresh
labels:
  - workspace=ops
steps:
  - name: step1
    run: echo "hello"
`), 0600))

	result, errList, err := store.List(ctx, persis.DAGListOptions{})
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 1)

	indexPath := filepath.Join(dagDir, dagindex.IndexFileName)
	indexInfoBefore, err := os.Stat(indexPath)
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)
	require.NoError(t, os.WriteFile(workspaceBaseConfig, []byte("max_active_steps: 2\n"), 0600))

	result, errList, err = store.List(ctx, persis.DAGListOptions{})
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 1)

	indexInfoAfter, err := os.Stat(indexPath)
	require.NoError(t, err)
	require.True(t, indexInfoAfter.ModTime().After(indexInfoBefore.ModTime()))
}

func TestListWithPagination(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-list-pagination")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Create multiple DAGs
	for i := 1; i <= 5; i++ {
		dagContent := fmt.Sprintf(`name: paginate-dag-%d
steps:
  - name: step1
    run: echo "dag %d"`, i, i)
		err := store.Create(ctx, fmt.Sprintf("paginate-dag-%d", i), []byte(dagContent))
		require.NoError(t, err)
	}

	// Test pagination
	paginator := pagination.NewPaginator(2, 2)
	opts := persis.DAGListOptions{Paginator: &paginator}
	result, errList, err := store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)

	assert.Equal(t, 5, result.TotalCount)
	assert.Equal(t, 2, len(result.Items))
	assert.True(t, result.HasNextPage)
	assert.True(t, result.HasPrevPage)
}

func TestListAlphabeticalSorting(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-list-alphabetical")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Create DAGs in non-alphabetical order
	dags := []struct {
		fileName string
		name     string
	}{
		{"zebra-dag", "zebra-dag"},
		{"alpha-dag", "alpha-dag"},
		{"beta-dag", "beta-dag"},
		{"CAPITAL-dag", "CAPITAL-dag"},
		{"123-numeric-dag", "123-numeric-dag"},
	}

	for _, dag := range dags {
		content := fmt.Sprintf(`name: %s
steps:
  - name: step1
    run: echo "%s"`, dag.name, dag.name)
		err := store.Create(ctx, dag.fileName, []byte(content))
		require.NoError(t, err)
	}

	// List all DAGs
	opts := persis.DAGListOptions{}
	result, errList, err := store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 5)

	// Verify alphabetical order (case-insensitive)
	expectedOrder := []string{
		"123-numeric-dag",
		"alpha-dag",
		"beta-dag",
		"CAPITAL-dag",
		"zebra-dag",
	}

	for i, dag := range result.Items {
		assert.Equal(t, expectedOrder[i], dag.Name,
			"DAG at position %d should be %s but was %s",
			i, expectedOrder[i], dag.Name)
	}
}

func TestListWithFiltering(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-list-filtering")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Create DAGs with different names and labels
	dag1Content := `name: filter-web-dag
labels: ["web", "frontend"]
steps:
  - name: step1
    run: echo "web"`
	err := store.Create(ctx, "filter-web-dag", []byte(dag1Content))
	require.NoError(t, err)

	dag2Content := `name: filter-batch-dag
labels: ["batch", "backend"]
steps:
  - name: step1
    run: echo "batch"`
	err = store.Create(ctx, "filter-batch-dag", []byte(dag2Content))
	require.NoError(t, err)

	// Test name filtering
	opts := persis.DAGListOptions{Name: "web"}
	result, errList, err := store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "filter-web-dag", result.Items[0].Name)

	// File name filtering should work even when the DAG-level name differs.
	fileNameOnlyContent := `name: display-name-only
steps:
  - name: step1
    run: echo "file-name-filter"`
	err = store.Create(ctx, "file-name-only-match", []byte(fileNameOnlyContent))
	require.NoError(t, err)

	opts = persis.DAGListOptions{Name: "file-name-only-match"}
	result, errList, err = store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "display-name-only", result.Items[0].Name)
	assert.Equal(t, "file-name-only-match", result.Items[0].FileName())

	// Test label filtering
	opts = persis.DAGListOptions{Labels: []string{"frontend"}}
	result, errList, err = store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "filter-web-dag", result.Items[0].Name)

	// Test case-insensitive label filtering
	opts = persis.DAGListOptions{Labels: []string{"FRONTEND"}}
	result, errList, err = store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "filter-web-dag", result.Items[0].Name)

	// Test multi-label AND filtering (all labels must match)
	opts = persis.DAGListOptions{Labels: []string{"web", "frontend"}}
	result, errList, err = store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "filter-web-dag", result.Items[0].Name)

	// Negative case: missing one label should return nothing
	opts = persis.DAGListOptions{Labels: []string{"web", "backend"}}
	result, errList, err = store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 0)

	for _, name := range []string{"active-alpha", "active-beta", "active-suspended"} {
		content := fmt.Sprintf(`name: %s
schedule: "0 * * * *"
steps:
  - name: step1
    run: echo "active"`, name)
		require.NoError(t, store.Create(ctx, name, []byte(content)))
	}
	require.NoError(t, store.SetSuspended(ctx, "active-suspended", true))

	paginator := pagination.NewPaginator(1, 1)
	result, errList, err = store.List(ctx, persis.DAGListOptions{
		Paginator:  &paginator,
		ActiveOnly: true,
		Sort:       "name",
		Order:      "asc",
	})
	require.NoError(t, err)
	require.Empty(t, errList)
	assert.Equal(t, 2, result.TotalCount)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "active-alpha", result.Items[0].Name)

	paginator = pagination.NewPaginator(2, 1)
	result, errList, err = store.List(ctx, persis.DAGListOptions{
		Paginator:  &paginator,
		ActiveOnly: true,
		Sort:       "name",
		Order:      "asc",
	})
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "active-beta", result.Items[0].Name)
}

func TestListWithSortAndOrder(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-list-sort-order")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Create DAGs with different names
	dags := []struct {
		fileName string
		name     string
	}{
		{"zebra-dag", "zebra-dag"},
		{"alpha-dag", "alpha-dag"},
		{"beta-dag", "beta-dag"},
		{"GAMMA-dag", "GAMMA-dag"},
	}

	// Create files for sorting tests (these tests assert name-based sorting)
	for _, dag := range dags {
		content := fmt.Sprintf(`name: %s
steps:
  - name: step1
    run: echo "%s"`, dag.name, dag.name)
		err := store.Create(ctx, dag.fileName, []byte(content))
		require.NoError(t, err)
	}

	// Test 1: Sort by name ascending (default)
	opts := persis.DAGListOptions{
		Sort:  "name",
		Order: "asc",
	}
	result, errList, err := store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 4)

	assert.Equal(t, "alpha-dag", result.Items[0].Name)
	assert.Equal(t, "beta-dag", result.Items[1].Name)
	assert.Equal(t, "GAMMA-dag", result.Items[2].Name)
	assert.Equal(t, "zebra-dag", result.Items[3].Name)

	// Test 2: Sort by name descending
	opts = persis.DAGListOptions{
		Sort:  "name",
		Order: "desc",
	}
	result, errList, err = store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 4)

	assert.Equal(t, "zebra-dag", result.Items[0].Name)
	assert.Equal(t, "GAMMA-dag", result.Items[1].Name)
	assert.Equal(t, "beta-dag", result.Items[2].Name)
	assert.Equal(t, "alpha-dag", result.Items[3].Name)

	// Test 3: Sort by updated_at should fall back to name sorting in storage layer
	opts = persis.DAGListOptions{
		Sort:  "updated_at",
		Order: "asc",
	}
	result, errList, err = store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 4)

	// Should be sorted by name since updated_at is handled in API layer
	assert.Equal(t, "alpha-dag", result.Items[0].Name)
	assert.Equal(t, "beta-dag", result.Items[1].Name)
	assert.Equal(t, "GAMMA-dag", result.Items[2].Name)
	assert.Equal(t, "zebra-dag", result.Items[3].Name)

	// Test 4: Sort by updated_at desc should also fall back to name
	opts = persis.DAGListOptions{
		Sort:  "updated_at",
		Order: "desc",
	}
	result, errList, err = store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 4)

	// Should be sorted by name descending (fallback behavior)
	assert.Equal(t, "zebra-dag", result.Items[0].Name)
	assert.Equal(t, "GAMMA-dag", result.Items[1].Name)
	assert.Equal(t, "beta-dag", result.Items[2].Name)
	assert.Equal(t, "alpha-dag", result.Items[3].Name)

	// Test 5: Default sort (empty sort field) should sort by name
	opts = persis.DAGListOptions{
		Sort:  "",
		Order: "asc",
	}
	result, errList, err = store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 4)

	assert.Equal(t, "alpha-dag", result.Items[0].Name)
	assert.Equal(t, "beta-dag", result.Items[1].Name)
	assert.Equal(t, "GAMMA-dag", result.Items[2].Name)
	assert.Equal(t, "zebra-dag", result.Items[3].Name)

	// Test 6: Unknown sort field falls back to name
	opts = persis.DAGListOptions{
		Sort:  "unknown",
		Order: "asc",
	}
	result, errList, err = store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 4)

	assert.Equal(t, "alpha-dag", result.Items[0].Name)
	assert.Equal(t, "beta-dag", result.Items[1].Name)
	assert.Equal(t, "GAMMA-dag", result.Items[2].Name)
	assert.Equal(t, "zebra-dag", result.Items[3].Name)
}

func TestListSortByNextRunUsesProjection(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-list-next-run-projection")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	oneOffTime := time.Date(2026, 3, 29, 2, 10, 0, 0, time.UTC)
	now := time.Date(2026, 3, 29, 2, 30, 0, 0, time.UTC)

	oneOffContent := fmt.Sprintf(`name: overdue-one-off
schedule:
  start:
    - at: "%s"
steps:
  - name: step1
    run: echo "one-off"`, oneOffTime.Format(time.RFC3339))
	require.NoError(t, store.Create(ctx, "overdue-one-off", []byte(oneOffContent)))

	cronContent := `name: future-cron
schedule: "0 3 * * *"
steps:
  - name: step1
    run: echo "cron"`
	require.NoError(t, store.Create(ctx, "future-cron", []byte(cronContent)))

	defaultResult, errList, err := store.List(ctx, persis.DAGListOptions{
		Sort:  "nextRun",
		Order: "asc",
		Time:  &now,
	})
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, defaultResult.Items, 2)
	assert.Equal(t, "future-cron", defaultResult.Items[0].Name)

	result, errList, err := store.List(ctx, persis.DAGListOptions{
		Sort:  "nextRun",
		Order: "asc",
		Time:  &now,
		NextRunProjection: func(dag *ir.DAG, at time.Time) time.Time {
			if dag.Name == "overdue-one-off" {
				return oneOffTime
			}
			return dag.NextRun(at)
		},
	})
	require.NoError(t, err)
	require.Empty(t, errList)
	require.Len(t, result.Items, 2)
	assert.Equal(t, "overdue-one-off", result.Items[0].Name)
	assert.Equal(t, "future-cron", result.Items[1].Name)
}

func TestListWithSortingAndPagination(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-list-sort-pagination")
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Create multiple DAGs for pagination testing
	dagNames := []string{
		"zulu-dag", "yankee-dag", "x-ray-dag", "whiskey-dag",
		"victor-dag", "uniform-dag", "tango-dag", "sierra-dag",
		"romeo-dag", "quebec-dag", "papa-dag", "oscar-dag",
	}

	for _, name := range dagNames {
		content := fmt.Sprintf(`name: %s
steps:
  - name: step1
    run: echo "%s"`, name, name)
		err := store.Create(ctx, name, []byte(content))
		require.NoError(t, err)
	}

	// Test 1: Name sort ascending with pagination
	// Page 1
	paginator := pagination.NewPaginator(1, 5) // page=1, perPage=5
	opts := persis.DAGListOptions{
		Paginator: &paginator,
		Sort:      "name",
		Order:     "asc",
	}
	result, errList, err := store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	assert.Equal(t, 12, result.TotalCount)
	assert.Len(t, result.Items, 5)
	assert.True(t, result.HasNextPage)
	assert.False(t, result.HasPrevPage)

	// First page should have first 5 alphabetically
	expectedPage1 := []string{"oscar-dag", "papa-dag", "quebec-dag", "romeo-dag", "sierra-dag"}
	for i, expected := range expectedPage1 {
		assert.Equal(t, expected, result.Items[i].Name)
	}

	// Page 2
	paginator = pagination.NewPaginator(2, 5) // page=2, perPage=5
	opts.Paginator = &paginator
	result, errList, err = store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	assert.Len(t, result.Items, 5)
	assert.True(t, result.HasNextPage)
	assert.True(t, result.HasPrevPage)

	// Second page should have next 5 alphabetically
	expectedPage2 := []string{"tango-dag", "uniform-dag", "victor-dag", "whiskey-dag", "x-ray-dag"}
	for i, expected := range expectedPage2 {
		assert.Equal(t, expected, result.Items[i].Name)
	}

	// Page 3
	paginator = pagination.NewPaginator(3, 5) // page=3, perPage=5
	opts.Paginator = &paginator
	result, errList, err = store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	assert.Len(t, result.Items, 2) // Only 2 items left
	assert.False(t, result.HasNextPage)
	assert.True(t, result.HasPrevPage)

	// Third page should have last 2 alphabetically
	assert.Equal(t, "yankee-dag", result.Items[0].Name)
	assert.Equal(t, "zulu-dag", result.Items[1].Name)

	// Test 2: Name sort descending with pagination
	paginator = pagination.NewPaginator(1, 5) // page=1, perPage=5
	opts = persis.DAGListOptions{
		Paginator: &paginator,
		Sort:      "name",
		Order:     "desc",
	}
	result, errList, err = store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	assert.Len(t, result.Items, 5)

	// First page descending should start with zulu
	expectedDesc := []string{"zulu-dag", "yankee-dag", "x-ray-dag", "whiskey-dag", "victor-dag"}
	for i, expected := range expectedDesc {
		assert.Equal(t, expected, result.Items[i].Name)
	}

	// Test 3: Non-name sort fields fall back to name sorting in storage layer
	paginator = pagination.NewPaginator(1, 5) // page=1, perPage=5
	opts = persis.DAGListOptions{
		Paginator: &paginator,
		Sort:      "updated_at",
		Order:     "desc", // This will fall back to name desc
	}
	result, errList, err = store.List(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, errList)
	assert.Len(t, result.Items, 5)

	// Should be sorted by name descending (fallback)
	expectedDesc = []string{"zulu-dag", "yankee-dag", "x-ray-dag", "whiskey-dag", "victor-dag"}
	for i, expected := range expectedDesc {
		assert.Equal(t, expected, result.Items[i].Name)
	}
}

func TestListIncludesDAGsWithErrors(t *testing.T) {
	tmpDir := t.TempDir()
	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Create a valid DAG
	validDAG := `
steps:
  - name: step1
    run: echo hello
`
	err := os.WriteFile(filepath.Join(tmpDir, "valid.yaml"), []byte(validDAG), 0644)
	require.NoError(t, err)

	// Create a DAG with errors (references non-existent step)
	errorDAG := `
nonexistent: error-dag
steps:
  - name: step1
    run: echo hello
`
	err = os.WriteFile(filepath.Join(tmpDir, "error.yaml"), []byte(errorDAG), 0644)
	require.NoError(t, err)

	// List all DAGs
	result, errList, err := store.List(ctx, persis.DAGListOptions{})
	require.NoError(t, err)

	// Should include both DAGs
	assert.Equal(t, 2, len(result.Items))

	// Check that both DAGs are present
	dagNames := make(map[string]bool)
	hasErrors := false
	for _, dag := range result.Items {
		t.Logf("Found DAG: %s, BuildErrors: %v", dag.Name, dag.BuildErrors)
		dagNames[dag.Name] = true
		if len(dag.BuildErrors) > 0 {
			hasErrors = true
		}
	}

	assert.True(t, dagNames["valid"], "valid should be in the list")
	assert.True(t, dagNames["error"], "error should be in the list")
	assert.True(t, hasErrors, "At least one DAG should have build errors")

	// Error list might contain warnings but should not fail
	t.Logf("Error list: %v", errList)
}

func TestListWithNextRunSorting(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-list-nextrun-sort")
	t.Cleanup(func() {
		_ = os.RemoveAll(tmpDir)
	})

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Create test DAG files directly
	createDAG := func(name, schedule string) {
		content := fmt.Sprintf("name: %s\nsteps:\n  - echo test", name)
		if schedule != "" {
			content = fmt.Sprintf("name: %s\nschedule: %s\nsteps:\n  - echo test", name, schedule)
		}
		err := os.WriteFile(filepath.Join(tmpDir, name+".yaml"), []byte(content), 0600)
		require.NoError(t, err)
	}

	// Create DAGs with different schedules
	createDAG("hourly-dag", "\"0 * * * *\"") // Runs at the top of every hour
	createDAG("daily-dag", "\"0 1 * * *\"")  // Runs at 01:00 every day
	createDAG("no-schedule", "")

	// Use a fixed time for deterministic testing: 2024-01-15 01:30:00
	// At this time:
	// - hourly-dag next run: 2024-01-15 02:00:00 (30 minutes away)
	// - daily-dag next run: 2024-01-16 01:00:00 (23.5 hours away)
	fixedTime := time.Date(2024, 1, 15, 1, 30, 0, 0, time.UTC)

	// Test ascending order
	result, _, err := store.List(ctx, persis.DAGListOptions{
		Sort:  "nextRun",
		Order: "asc",
		Time:  &fixedTime,
	})
	require.NoError(t, err)
	require.Len(t, result.Items, 3)

	// Verify order: hourly (runs soonest) -> daily -> no-schedule (last)
	assert.Equal(t, "hourly-dag", result.Items[0].Name)
	assert.Equal(t, "daily-dag", result.Items[1].Name)
	assert.Equal(t, "no-schedule", result.Items[2].Name)

	// Test descending order
	result, _, err = store.List(ctx, persis.DAGListOptions{
		Sort:  "nextRun",
		Order: "desc",
		Time:  &fixedTime,
	})
	require.NoError(t, err)
	require.Len(t, result.Items, 3)

	// Verify order: daily (runs latest) -> hourly -> no-schedule (still last)
	// Note: no-schedule always comes last because IsZero() time is treated as greater
	assert.Equal(t, "daily-dag", result.Items[0].Name)
	assert.Equal(t, "hourly-dag", result.Items[1].Name)
	assert.Equal(t, "no-schedule", result.Items[2].Name)
}

func TestListWithNextRunSortingPutsSuspendedDAGsLast(t *testing.T) {
	tmpDir := fileutil.MustTempDir("test-list-nextrun-suspended")
	t.Cleanup(func() {
		_ = os.RemoveAll(tmpDir)
	})

	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	createDAG := func(name, schedule string) {
		content := fmt.Sprintf(`name: %s
schedule: "%s"
steps:
  - name: step1
    run: echo "test"`, name, schedule)
		require.NoError(t, store.Create(ctx, name, []byte(content)))
	}

	createDAG("suspended-sooner", "0 2 * * *")
	createDAG("live-later", "0 3 * * *")
	require.NoError(t, store.SetSuspended(ctx, "suspended-sooner", true))

	fixedTime := time.Date(2024, 1, 15, 1, 30, 0, 0, time.UTC)

	result, _, err := store.List(ctx, persis.DAGListOptions{
		Sort:  "nextRun",
		Order: "asc",
		Time:  &fixedTime,
	})
	require.NoError(t, err)
	require.Len(t, result.Items, 2)

	assert.Equal(t, "live-later", result.Items[0].Name)
	assert.Equal(t, "suspended-sooner", result.Items[1].Name)
}

func TestConcurrentList(t *testing.T) {
	tmpDir := t.TempDir()
	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Create a few DAGs.
	for i := range 5 {
		content := fmt.Sprintf("name: concurrent-dag-%d\nsteps:\n  - name: s1\n    command: echo ok\n", i)
		err := store.Create(ctx, fmt.Sprintf("concurrent-dag-%d", i), []byte(content))
		require.NoError(t, err)
	}

	// Run 10 concurrent List calls.
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			result, _, err := store.List(ctx, persis.DAGListOptions{})
			assert.NoError(t, err)
			assert.Equal(t, 5, result.TotalCount)
		})
	}
	wg.Wait()
}

func TestIndexInvalidationOnMutations(t *testing.T) {
	tmpDir := t.TempDir()
	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	indexPath := filepath.Join(tmpDir, ".dag.index")

	// Create a DAG and build index via List.
	content := `name: mutation-dag
steps:
  - name: step1
    run: echo hello`
	require.NoError(t, store.Create(ctx, "mutation-dag", []byte(content)))

	// List to build index.
	_, _, err := store.List(ctx, persis.DAGListOptions{})
	require.NoError(t, err)
	assert.True(t, fileExists(indexPath), "index should exist after List")

	// Create invalidates index.
	require.NoError(t, store.Create(ctx, "another-dag", []byte(content)))
	assert.False(t, fileExists(indexPath), "index should be invalidated after Create")

	// Rebuild index.
	_, _, err = store.List(ctx, persis.DAGListOptions{})
	require.NoError(t, err)
	assert.True(t, fileExists(indexPath))

	// Delete invalidates index.
	require.NoError(t, store.Delete(ctx, "another-dag"))
	assert.False(t, fileExists(indexPath), "index should be invalidated after Delete")

	// Rebuild index.
	_, _, err = store.List(ctx, persis.DAGListOptions{})
	require.NoError(t, err)
	assert.True(t, fileExists(indexPath))

	// SetSuspended invalidates index.
	require.NoError(t, store.SetSuspended(ctx, "mutation-dag", true))
	assert.False(t, fileExists(indexPath), "index should be invalidated after SetSuspended")

	// Rebuild index.
	_, _, err = store.List(ctx, persis.DAGListOptions{})
	require.NoError(t, err)
	assert.True(t, fileExists(indexPath))

	// UpdateSpec invalidates index.
	updatedContent := `name: mutation-dag
steps:
  - name: step1
    run: echo updated`
	require.NoError(t, store.UpdateSpec(ctx, "mutation-dag", []byte(updatedContent)))
	assert.False(t, fileExists(indexPath), "index should be invalidated after UpdateSpec")

	// Rebuild index.
	_, _, err = store.List(ctx, persis.DAGListOptions{})
	require.NoError(t, err)
	assert.True(t, fileExists(indexPath))

	// Rename invalidates index.
	require.NoError(t, store.Rename(ctx, "mutation-dag", "renamed-dag"))
	assert.False(t, fileExists(indexPath), "index should be invalidated after Rename")
}

func TestListUsesIndex(t *testing.T) {
	tmpDir := t.TempDir()
	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Create DAGs.
	for i := range 3 {
		content := fmt.Sprintf("name: idx-dag-%d\nlabels:\n  - env=prod\nsteps:\n  - name: s1\n    command: echo ok\n", i)
		require.NoError(t, store.Create(ctx, fmt.Sprintf("idx-dag-%d", i), []byte(content)))
	}

	// First List builds index.
	result1, errList1, err := store.List(ctx, persis.DAGListOptions{})
	require.NoError(t, err)
	require.Empty(t, errList1)
	assert.Equal(t, 3, result1.TotalCount)

	// Verify index file exists.
	indexPath := filepath.Join(tmpDir, ".dag.index")
	assert.True(t, fileExists(indexPath))

	// Second List should use index and return same results.
	result2, errList2, err := store.List(ctx, persis.DAGListOptions{})
	require.NoError(t, err)
	require.Empty(t, errList2)
	assert.Equal(t, 3, result2.TotalCount)

	// Verify filtering works with index.
	result3, _, err := store.List(ctx, persis.DAGListOptions{Labels: []string{"env=prod"}})
	require.NoError(t, err)
	assert.Equal(t, 3, result3.TotalCount)
}

func TestList_NonExistentDir(t *testing.T) {
	store := newRepository("/nonexistent/path/that/does/not/exist", WithSkipExamples(true))
	ctx := context.Background()
	_, _, err := store.List(ctx, persis.DAGListOptions{})
	require.Error(t, err)
}

func TestList_NonExistentFlagsDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a valid DAG file so the index can try to build.
	dagContent := `name: flags-test
steps:
  - name: s1
    run: echo ok`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "flags-test.yaml"), []byte(dagContent), 0600))

	store := NewStore(tmpDir, WithSkipExamples(true))
	// Set flagsBaseDir to non-existent path to exercise the debug log branch.
	store.flagsBaseDir = filepath.Join(tmpDir, "nonexistent-flags-dir")

	ctx := context.Background()
	result, err := store.Catalog(ctx)
	require.NoError(t, err)
	require.Empty(t, result.Issues)
	assert.Len(t, result.Items, 1)
}

func TestInvalidateIndex_RemovesFile(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Create a DAG and build index.
	dagContent := `name: inv-test
steps:
  - name: s1
    run: echo ok`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "inv-test.yaml"), []byte(dagContent), 0600))

	_, err := store.Catalog(ctx)
	require.NoError(t, err)
	indexPath := filepath.Join(tmpDir, ".dag.index")
	assert.True(t, fileExists(indexPath), "index should exist after build")

	store.invalidateIndex()
	assert.False(t, fileExists(indexPath), "index should be removed after invalidation")
}

func TestLabelListUsesIndex(t *testing.T) {
	tmpDir := t.TempDir()
	store := newRepository(tmpDir, WithSkipExamples(true))
	ctx := context.Background()

	// Create DAGs with labels.
	dag1 := "name: label-idx-1\nlabels:\n  - env=prod\n  - team=backend\nsteps:\n  - name: s1\n    command: echo ok\n"
	dag2 := "name: label-idx-2\nlabels:\n  - env=staging\nsteps:\n  - name: s1\n    command: echo ok\n"
	require.NoError(t, store.Create(ctx, "label-idx-1", []byte(dag1)))
	require.NoError(t, store.Create(ctx, "label-idx-2", []byte(dag2)))

	// First call builds index.
	labels1, errList1, err := store.LabelList(ctx)
	require.NoError(t, err)
	require.Empty(t, errList1)
	expectedLabels := []string{"env", "env=prod", "env=staging", "team", "team=backend"}
	assert.ElementsMatch(t, expectedLabels, labels1)

	// Verify index exists.
	indexPath := filepath.Join(tmpDir, ".dag.index")
	assert.True(t, fileExists(indexPath))

	// Second call uses index.
	labels2, errList2, err := store.LabelList(ctx)
	require.NoError(t, err)
	require.Empty(t, errList2)
	assert.ElementsMatch(t, expectedLabels, labels2)
	assert.ElementsMatch(t, labels1, labels2)
}
