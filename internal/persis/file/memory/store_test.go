// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("creates directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		store, err := New(dir)
		require.NoError(t, err)
		require.NotNil(t, store)

		info, err := os.Stat(filepath.Join(dir, agentMemoryDir))
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("rejects empty dagsDir", func(t *testing.T) {
		t.Parallel()
		store, err := New("")
		require.Error(t, err)
		assert.Nil(t, store)
		assert.Contains(t, err.Error(), "cannot be empty")
	})

	t.Run("with file cache option", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cache := fileutil.NewCache[string]("memory_test", 10, time.Hour)
		store, err := New(dir, WithFileCache(cache))
		require.NoError(t, err)
		require.NotNil(t, store)
		assert.Equal(t, cache, store.fileCache)
	})
}

func TestLoadGlobalMemory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("no file returns empty", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		content, err := store.LoadGlobalMemory(ctx)
		require.NoError(t, err)
		assert.Empty(t, content)
	})

	t.Run("reads existing file", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		expected := "# Global Memory\n\nUser prefers concise output."
		writeTestFile(t, store.globalMemoryPath(), expected)

		content, err := store.LoadGlobalMemory(ctx)
		require.NoError(t, err)
		assert.Equal(t, expected, content)
	})

	t.Run("truncates at max lines", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)

		// Create content with more than maxLines
		lines := make([]string, maxLines+50)
		for i := range lines {
			lines[i] = "line content"
		}
		writeTestFile(t, store.globalMemoryPath(), strings.Join(lines, "\n"))

		content, err := store.LoadGlobalMemory(ctx)
		require.NoError(t, err)
		assert.Contains(t, content, "truncated at 200 lines")

		// Count lines before truncation notice
		resultLines := strings.Split(content, "\n")
		// maxLines of content + 1 truncation notice line
		assert.Equal(t, maxLines+1, len(resultLines))
	})
}

func TestLoadDAGMemory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("no file returns empty", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		content, err := store.LoadDAGMemory(ctx, "my-dag")
		require.NoError(t, err)
		assert.Empty(t, content)
	})

	t.Run("reads existing file", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		expected := "# DAG Memory\n\nThis DAG runs hourly."
		dagDir := filepath.Join(store.baseDir, dagSubDir, "my-dag")
		require.NoError(t, os.MkdirAll(dagDir, 0750))
		writeTestFile(t, filepath.Join(dagDir, memoryFileName), expected)

		content, err := store.LoadDAGMemory(ctx, "my-dag")
		require.NoError(t, err)
		assert.Equal(t, expected, content)
	})

	t.Run("truncates at max lines", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)

		lines := make([]string, maxLines+10)
		for i := range lines {
			lines[i] = "dag line"
		}
		dagDir := filepath.Join(store.baseDir, dagSubDir, "big-dag")
		require.NoError(t, os.MkdirAll(dagDir, 0750))
		writeTestFile(t, filepath.Join(dagDir, memoryFileName), strings.Join(lines, "\n"))

		content, err := store.LoadDAGMemory(ctx, "big-dag")
		require.NoError(t, err)
		assert.Contains(t, content, "truncated at 200 lines")
	})

	t.Run("rejects path traversal", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)

		_, err := store.LoadDAGMemory(ctx, "../etc/passwd")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid dagName")
	})

	t.Run("rejects empty dagName", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)

		_, err := store.LoadDAGMemory(ctx, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})
}

func TestSaveGlobalMemory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("creates file", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		content := "# Memory\n\nSome info."
		require.NoError(t, store.SaveGlobalMemory(ctx, content))

		data, err := os.ReadFile(store.globalMemoryPath())
		require.NoError(t, err)
		assert.Equal(t, content, string(data))
	})

	t.Run("overwrites existing", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		require.NoError(t, store.SaveGlobalMemory(ctx, "old content"))
		require.NoError(t, store.SaveGlobalMemory(ctx, "new content"))

		data, err := os.ReadFile(store.globalMemoryPath())
		require.NoError(t, err)
		assert.Equal(t, "new content", string(data))
	})
}

func TestSaveDAGMemory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("creates file and parent dirs", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		content := "# DAG Memory\n\nPipeline details."
		require.NoError(t, store.SaveDAGMemory(ctx, "my-pipeline", content))

		data, err := os.ReadFile(store.dagMemoryPath("my-pipeline"))
		require.NoError(t, err)
		assert.Equal(t, content, string(data))
	})

	t.Run("overwrites existing", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		require.NoError(t, store.SaveDAGMemory(ctx, "my-dag", "old"))
		require.NoError(t, store.SaveDAGMemory(ctx, "my-dag", "new"))

		data, err := os.ReadFile(store.dagMemoryPath("my-dag"))
		require.NoError(t, err)
		assert.Equal(t, "new", string(data))
	})

	t.Run("rejects path traversal", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		err := store.SaveDAGMemory(ctx, "../escape", "bad")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid dagName")
	})
}

func TestMemoryDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := New(dir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, agentMemoryDir), store.MemoryDir())
}

func TestListDAGMemories(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("empty when no DAG memories", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		names, err := store.ListDAGMemories(ctx)
		require.NoError(t, err)
		assert.Empty(t, names)
	})

	t.Run("returns DAGs with memory files", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		require.NoError(t, store.SaveDAGMemory(ctx, "dag-a", "memory A"))
		require.NoError(t, store.SaveDAGMemory(ctx, "dag-b", "memory B"))

		names, err := store.ListDAGMemories(ctx)
		require.NoError(t, err)
		assert.Len(t, names, 2)
		assert.Contains(t, names, "dag-a")
		assert.Contains(t, names, "dag-b")
	})

	t.Run("ignores empty directories", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		require.NoError(t, store.SaveDAGMemory(ctx, "has-memory", "content"))

		// Create empty directory (no MEMORY.md)
		emptyDir := filepath.Join(store.baseDir, dagSubDir, "no-memory")
		require.NoError(t, os.MkdirAll(emptyDir, 0750))

		names, err := store.ListDAGMemories(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"has-memory"}, names)
	})
}

func TestDeleteGlobalMemory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("deletes existing file", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		require.NoError(t, store.SaveGlobalMemory(ctx, "some content"))

		require.NoError(t, store.DeleteGlobalMemory(ctx))

		content, err := store.LoadGlobalMemory(ctx)
		require.NoError(t, err)
		assert.Empty(t, content)
	})

	t.Run("no error when file does not exist", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		require.NoError(t, store.DeleteGlobalMemory(ctx))
	})
}

func TestDeleteDAGMemory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("deletes existing DAG memory", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		require.NoError(t, store.SaveDAGMemory(ctx, "my-dag", "content"))

		require.NoError(t, store.DeleteDAGMemory(ctx, "my-dag"))

		content, err := store.LoadDAGMemory(ctx, "my-dag")
		require.NoError(t, err)
		assert.Empty(t, content)
	})

	t.Run("no error when DAG memory does not exist", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		require.NoError(t, store.DeleteDAGMemory(ctx, "nonexistent"))
	})

	t.Run("rejects path traversal", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		err := store.DeleteDAGMemory(ctx, "../escape")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid dagName")
	})
}

func TestConcurrentAccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = store.SaveGlobalMemory(ctx, "concurrent write")
		}()
		go func() {
			defer wg.Done()
			_, _ = store.LoadGlobalMemory(ctx)
		}()
	}
	wg.Wait()

	// Verify file is valid after concurrent access
	content, err := store.LoadGlobalMemory(ctx)
	require.NoError(t, err)
	assert.Equal(t, "concurrent write", content)
}

func TestCacheInvalidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("save global invalidates cache", func(t *testing.T) {
		t.Parallel()
		cache := fileutil.NewCache[string]("memory_test", 10, time.Hour)
		store := newTestStoreWithOptions(t, WithFileCache(cache))

		require.NoError(t, store.SaveGlobalMemory(ctx, "v1"))
		_, err := store.LoadGlobalMemory(ctx)
		require.NoError(t, err)

		_, found := cache.Load(store.globalMemoryPath())
		require.True(t, found)

		require.NoError(t, store.SaveGlobalMemory(ctx, "v2"))
		_, found = cache.Load(store.globalMemoryPath())
		assert.False(t, found)
	})

	t.Run("delete global invalidates cache", func(t *testing.T) {
		t.Parallel()
		cache := fileutil.NewCache[string]("memory_test", 10, time.Hour)
		store := newTestStoreWithOptions(t, WithFileCache(cache))

		require.NoError(t, store.SaveGlobalMemory(ctx, "v1"))
		_, err := store.LoadGlobalMemory(ctx)
		require.NoError(t, err)

		_, found := cache.Load(store.globalMemoryPath())
		require.True(t, found)

		require.NoError(t, store.DeleteGlobalMemory(ctx))
		_, found = cache.Load(store.globalMemoryPath())
		assert.False(t, found)
	})

	t.Run("save DAG invalidates cache", func(t *testing.T) {
		t.Parallel()
		cache := fileutil.NewCache[string]("memory_test", 10, time.Hour)
		store := newTestStoreWithOptions(t, WithFileCache(cache))
		path := store.dagMemoryPath("my-dag")

		require.NoError(t, store.SaveDAGMemory(ctx, "my-dag", "v1"))
		_, err := store.LoadDAGMemory(ctx, "my-dag")
		require.NoError(t, err)

		_, found := cache.Load(path)
		require.True(t, found)

		require.NoError(t, store.SaveDAGMemory(ctx, "my-dag", "v2"))
		_, found = cache.Load(path)
		assert.False(t, found)
	})

	t.Run("delete DAG invalidates cache", func(t *testing.T) {
		t.Parallel()
		cache := fileutil.NewCache[string]("memory_test", 10, time.Hour)
		store := newTestStoreWithOptions(t, WithFileCache(cache))
		path := store.dagMemoryPath("my-dag")

		require.NoError(t, store.SaveDAGMemory(ctx, "my-dag", "v1"))
		_, err := store.LoadDAGMemory(ctx, "my-dag")
		require.NoError(t, err)

		_, found := cache.Load(path)
		require.True(t, found)

		require.NoError(t, store.DeleteDAGMemory(ctx, "my-dag"))
		_, found = cache.Load(path)
		assert.False(t, found)
	})
}

// newTestStore creates a Store backed by a temporary directory.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	return newTestStoreWithOptions(t)
}

func newTestStoreWithOptions(t *testing.T, opts ...Option) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := New(dir, opts...)
	require.NoError(t, err)
	return store
}

// writeTestFile writes content to a file, failing the test on error.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
}
