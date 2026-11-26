// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverRecursive(t *testing.T) {
	baseDir := t.TempDir()
	root := filepath.Join(baseDir, "dags")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "team", "nested"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".hidden"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "team", ".private"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspaces", "ops"), 0750))

	for _, path := range []string{
		filepath.Join(root, "root.yaml"),
		filepath.Join(root, "team", "nested.yml"),
		filepath.Join(root, "team", "nested", "deep.yaml"),
		filepath.Join(root, ".hidden", "hidden.yaml"),
		filepath.Join(root, "team", ".private", "private.yaml"),
		filepath.Join(root, "workspaces", "ops", "base.yaml"),
	} {
		require.NoError(t, os.WriteFile(path, []byte("steps: []\n"), 0600))
	}

	externalDir := filepath.Join(baseDir, "external")
	require.NoError(t, os.MkdirAll(externalDir, 0750))
	externalFile := filepath.Join(externalDir, "linked.yaml")
	require.NoError(t, os.WriteFile(externalFile, []byte("steps: []\n"), 0600))
	if err := os.Symlink(externalDir, filepath.Join(root, "linked-dir")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	require.NoError(t, os.Symlink(externalFile, filepath.Join(root, "linked-file.yaml")))

	result, err := Discover(root, DiscoveryOptions{Recursive: true})
	require.NoError(t, err)
	require.Len(t, result.Errors, 1)
	assert.ErrorIs(t, result.Errors[0], ErrExternalSymlinkDisabled)

	var files []string
	for _, file := range result.Files {
		files = append(files, file.RelPath)
	}
	assert.Equal(t, []string{
		"root.yaml",
		"team/nested.yml",
		"team/nested/deep.yaml",
	}, files)
	assert.Equal(t, []string{
		root,
		filepath.Join(root, "team"),
		filepath.Join(root, "team", "nested"),
	}, result.Dirs)

	result, err = Discover(root, DiscoveryOptions{Recursive: true, Symlinks: true})
	require.NoError(t, err)
	require.Empty(t, result.Errors)
	files = files[:0]
	for _, file := range result.Files {
		files = append(files, file.RelPath)
	}
	assert.Equal(t, []string{
		"linked-file.yaml",
		"root.yaml",
		"team/nested.yml",
		"team/nested/deep.yaml",
	}, files)
	expectedExternalFile, err := filepath.EvalSymlinks(externalFile)
	require.NoError(t, err)
	assert.Equal(t, expectedExternalFile, result.Files[0].ResolvedPath)
	assert.Equal(t, []string{
		root,
		filepath.Join(root, "team"),
		filepath.Join(root, "team", "nested"),
	}, result.Dirs)
}

func TestDiscoverRecursiveSkipsConfiguredDirectories(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "jobs", "unit-a"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pipelines", "jobs"), 0750))

	for _, path := range []string{
		filepath.Join(root, "entrypoint.yaml"),
		filepath.Join(root, "pipelines", "batch.yaml"),
		filepath.Join(root, "pipelines", "jobs", "nested.yaml"),
		filepath.Join(root, "jobs", "unit-a", "child.yaml"),
	} {
		require.NoError(t, os.WriteFile(path, []byte("steps: []\n"), 0600))
	}

	// Without a skip list the child subtree is indexed.
	result, err := Discover(root, DiscoveryOptions{Recursive: true})
	require.NoError(t, err)
	assert.Len(t, result.Files, 4)

	// With skip_directories the child subtree is omitted from discovery but
	// its files stay resolvable by path (Discover is read separately).
	result, err = Discover(root, DiscoveryOptions{Recursive: true, SkipDirs: []string{"jobs"}})
	require.NoError(t, err)
	var files []string
	for _, file := range result.Files {
		files = append(files, file.RelPath)
	}
	// A directory deeper in the tree with a skipped name is still read.
	assert.Equal(t, []string{"entrypoint.yaml", "pipelines/batch.yaml", "pipelines/jobs/nested.yaml"}, files)
}

// The skills directory at the root holds skill folders, not DAGs, so
// discovery neither lists nor returns for watching anything below it. A
// directory named skills deeper in the tree is read as usual.
func TestDiscoverRecursiveSkipsSkillsDir(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		filepath.Join(root, "root.yaml"),
		filepath.Join(root, "skills", "notes.yaml"),
		filepath.Join(root, "skills", "guide", "assets", "example.yaml"),
		filepath.Join(root, "team", "skills", "flow.yaml"),
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0750))
		require.NoError(t, os.WriteFile(path, []byte("steps: []\n"), 0600))
	}

	result, err := Discover(root, DiscoveryOptions{Recursive: true})
	require.NoError(t, err)

	var files []string
	for _, file := range result.Files {
		files = append(files, file.RelPath)
	}
	assert.Equal(t, []string{"root.yaml", "team/skills/flow.yaml"}, files)
	assert.Equal(t, []string{
		root,
		filepath.Join(root, "team"),
		filepath.Join(root, "team", "skills"),
	}, result.Dirs)
}

// A file system that ignores case serves a root directory spelled Skills for
// the path root/skills, so discovery skips it too. Where case matters, it is
// an ordinary directory.
func TestDiscoverRecursiveSkipsSkillsDirInOtherCase(t *testing.T) {
	root := t.TempDir()
	writeDAGFiles(t,
		filepath.Join(root, "root.yaml"),
		filepath.Join(root, "Skills", "guide", "assets", "example.yaml"),
	)

	result, err := Discover(root, DiscoveryOptions{Recursive: true})
	require.NoError(t, err)

	if fileutil.IsCaseInsensitiveFS(root) {
		assert.Equal(t, []string{"root.yaml"}, relPaths(result))
		assert.Equal(t, []string{root}, result.Dirs)
		return
	}
	assert.Equal(t, []string{"Skills/guide/assets/example.yaml", "root.yaml"}, relPaths(result))
}

// A skip root is omitted wherever it lies below root, and matched by
// identity, so another spelling of its path still matches.
func TestDiscoverRecursiveSkipsNestedRoots(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "dags")
	nested := filepath.Join(root, "team", "projects")
	writeDAGFiles(t,
		filepath.Join(root, "root.yaml"),
		filepath.Join(nested, "weather", "nested.yaml"),
		filepath.Join(root, "other", "projects", "kept.yaml"),
	)
	discovered := func(t *testing.T, skipRoots ...string) []string {
		t.Helper()
		result, err := Discover(root, DiscoveryOptions{Recursive: true, SkipRoots: skipRoots})
		require.NoError(t, err)
		require.Empty(t, result.Errors)
		return relPaths(result)
	}
	want := []string{"other/projects/kept.yaml", "root.yaml"}

	assert.Equal(t, want, discovered(t, nested, filepath.Join(base, "missing")))

	t.Run("RootItself", func(t *testing.T) {
		assert.Equal(t, append(want, "team/projects/weather/nested.yaml"), discovered(t, root))
	})

	t.Run("SymlinkedPath", func(t *testing.T) {
		link := filepath.Join(base, "link")
		if err := os.Symlink(nested, link); err != nil {
			t.Skipf("symlinks are unavailable: %v", err)
		}
		assert.Equal(t, want, discovered(t, link))
	})

	t.Run("OtherCase", func(t *testing.T) {
		if !fileutil.IsCaseInsensitiveFS(root) {
			t.Skip("file system distinguishes case")
		}
		assert.Equal(t, want, discovered(t, filepath.Join(root, "TEAM", "Projects")))
	})
}

// A file symlink whose target lies in a nested skip root is omitted, in
// recursive and root-only discovery alike. One into another directory stays.
func TestDiscoverSkipsSymlinksIntoNestedRoots(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "projects")
	writeDAGFiles(t,
		filepath.Join(nested, "weather", "beta.yaml"),
		filepath.Join(root, "team", "kept.yaml"),
	)
	if err := os.Symlink(filepath.Join(nested, "weather", "beta.yaml"), filepath.Join(root, "alias.yaml")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	require.NoError(t, os.Symlink(filepath.Join(root, "team", "kept.yaml"), filepath.Join(root, "link.yaml")))

	for _, recursive := range []bool{true, false} {
		result, err := Discover(root, DiscoveryOptions{Recursive: recursive, Symlinks: true, SkipRoots: []string{nested}})
		require.NoError(t, err)
		require.Empty(t, result.Errors)
		files := relPaths(result)
		assert.NotContains(t, files, "alias.yaml", "recursive=%v", recursive)
		assert.Contains(t, files, "link.yaml", "recursive=%v", recursive)
	}
}

// A skip root that cannot be looked up is reported, and discovery goes on.
func TestDiscoverReportsUnreadableSkipRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not deny lookups on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	base := t.TempDir()
	root := filepath.Join(base, "dags")
	writeDAGFiles(t, filepath.Join(root, "root.yaml"))
	locked := filepath.Join(base, "locked")
	require.NoError(t, os.MkdirAll(filepath.Join(locked, "projects"), 0750))
	require.NoError(t, os.Chmod(locked, 0))
	t.Cleanup(func() { _ = os.Chmod(locked, 0750) })

	result, err := Discover(root, DiscoveryOptions{
		Recursive: true,
		SkipRoots: []string{filepath.Join(locked, "projects"), filepath.Join(base, "missing")},
	})
	require.NoError(t, err)
	require.Len(t, result.Errors, 1)
	assert.ErrorIs(t, result.Errors[0], fs.ErrPermission)
	assert.Equal(t, []string{"root.yaml"}, relPaths(result))
}

// writeDAGFiles writes a minimal DAG file at each path, creating its
// directory.
func writeDAGFiles(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0750))
		require.NoError(t, os.WriteFile(path, []byte("steps: []\n"), 0600))
	}
}

// relPaths returns the relative paths of the files in result.
func relPaths(result DiscoveryResult) []string {
	var paths []string
	for _, file := range result.Files {
		paths = append(paths, file.RelPath)
	}
	return paths
}

func TestDiscoverNonRecursiveOnlyReadsRoot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "nested"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "root.yaml"), []byte("steps: []\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "nested", "child.yaml"), []byte("steps: []\n"), 0600))

	result, err := Discover(root, DiscoveryOptions{})
	require.NoError(t, err)
	require.Empty(t, result.Errors)
	require.Len(t, result.Files, 1)
	assert.Equal(t, "root.yaml", result.Files[0].RelPath)
	assert.Equal(t, []string{root}, result.Dirs)
}

// A test file is never discovered, with either extension and at any depth,
// while a DAG whose name merely contains "test" still is.
func TestDiscoverSkipsTestFiles(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "etl"), 0750))
	for _, name := range []string{"release.yaml", "release.test.yaml", "latest.yaml", "etl/daily.yaml", "etl/daily.test.yml"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte("steps: []\n"), 0600))
	}

	for recursive, want := range map[bool][]string{
		true:  {"etl/daily.yaml", "latest.yaml", "release.yaml"},
		false: {"latest.yaml", "release.yaml"},
	} {
		result, err := Discover(root, DiscoveryOptions{Recursive: recursive})
		require.NoError(t, err)
		var files []string
		for _, file := range result.Files {
			files = append(files, file.RelPath)
		}
		assert.Equal(t, want, files, "recursive=%t", recursive)
	}
}

func TestDiscoverNonRecursiveAllowsInternalFileSymlinkByDefault(t *testing.T) {
	root := t.TempDir()
	nestedDir := filepath.Join(root, "nested")
	require.NoError(t, os.MkdirAll(nestedDir, 0750))
	targetPath := filepath.Join(nestedDir, "source.yaml")
	require.NoError(t, os.WriteFile(targetPath, []byte("steps: []\n"), 0600))
	if err := os.Symlink(targetPath, filepath.Join(root, "linked.yaml")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	result, err := Discover(root, DiscoveryOptions{})
	require.NoError(t, err)
	require.Empty(t, result.Errors)
	require.Len(t, result.Files, 1)
	assert.Equal(t, "linked.yaml", result.Files[0].RelPath)
}

func TestDiscoverNonRecursiveExternalFileSymlink(t *testing.T) {
	root := t.TempDir()
	externalDir := t.TempDir()
	externalFile := filepath.Join(externalDir, "external.yaml")
	require.NoError(t, os.WriteFile(externalFile, []byte("steps: []\n"), 0600))
	if err := os.Symlink(externalFile, filepath.Join(root, "external.yaml")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	result, err := Discover(root, DiscoveryOptions{})
	require.NoError(t, err)
	require.Empty(t, result.Files)
	require.Len(t, result.Errors, 1)
	assert.ErrorIs(t, result.Errors[0], ErrExternalSymlinkDisabled)

	result, err = Discover(root, DiscoveryOptions{Symlinks: true})
	require.NoError(t, err)
	require.Empty(t, result.Errors)
	require.Len(t, result.Files, 1)
	assert.Equal(t, "external.yaml", result.Files[0].RelPath)
	expectedExternalFile, err := filepath.EvalSymlinks(externalFile)
	require.NoError(t, err)
	assert.Equal(t, expectedExternalFile, result.Files[0].ResolvedPath)
}

func TestDiscoverRecursiveAllowsConfiguredSymlinkRoot(t *testing.T) {
	baseDir := t.TempDir()
	realRoot := filepath.Join(baseDir, "real")
	require.NoError(t, os.MkdirAll(filepath.Join(realRoot, "nested"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(realRoot, "nested", "child.yaml"), []byte("steps: []\n"), 0600))

	linkRoot := filepath.Join(baseDir, "dags")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	result, err := Discover(linkRoot, DiscoveryOptions{Recursive: true})
	require.NoError(t, err)
	require.Empty(t, result.Errors)
	require.Len(t, result.Files, 1)
	assert.Equal(t, "nested/child.yaml", result.Files[0].RelPath)
	assert.Equal(t, []string{linkRoot, filepath.Join(linkRoot, "nested")}, result.Dirs)
}

// A confined discovery follows file symlinks inside its root as Symlinks
// says, and none out of it.
func TestDiscoverConfinedSymlinks(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "team", "inside.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(inside), 0o750))
	require.NoError(t, os.WriteFile(inside, []byte("steps: []\n"), 0o600))
	external := filepath.Join(t.TempDir(), "external.yaml")
	require.NoError(t, os.WriteFile(external, []byte("steps: []\n"), 0o600))
	symlinkOrSkip(t, inside, filepath.Join(root, "inside-link.yaml"))
	symlinkOrSkip(t, external, filepath.Join(root, "external-link.yaml"))

	for _, recursive := range []bool{false, true} {
		result, err := Discover(root, DiscoveryOptions{Recursive: recursive, Symlinks: true, ConfineSymlinks: true})
		require.NoError(t, err)
		var files []string
		for _, file := range result.Files {
			files = append(files, file.RelPath)
		}
		assert.Contains(t, files, "inside-link.yaml")
		assert.NotContains(t, files, "external-link.yaml")
		require.Len(t, result.Errors, 1)
		assert.ErrorIs(t, result.Errors[0], ErrExternalSymlinkDisabled)
		assert.ErrorContains(t, result.Errors[0], "a project never follows a symbolic link out of its DAG directory")
	}
}
