// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
)

// CacheMetrics provides observability into cache state
type CacheMetrics interface {
	// Size returns the current number of entries in the cache
	Size() int
	// Name returns a human-readable name for the cache
	Name() string
}

// entry holds cached data alongside file metadata for staleness detection.
type entry[T any] struct {
	data         T
	size         int64
	lastModified int64
}

// Cache is a generic file cache backed by an LRU with TTL-based expiration.
// It stores entries with file metadata (size, modification time) to detect
// when cached data is stale relative to the file on disk.
type Cache[T any] struct {
	name string
	lru  *expirable.LRU[string, entry[T]]
}

// Ensure Cache implements CacheMetrics
var _ CacheMetrics = (*Cache[any])(nil)

// NewCache creates a new cache with the specified capacity and time-to-live duration.
// A capacity of 0 means unlimited size.
func NewCache[T any](name string, capacity int, ttl time.Duration) *Cache[T] {
	return &Cache[T]{
		name: name,
		lru:  expirable.NewLRU[string, entry[T]](capacity, nil, ttl),
	}
}

// Size returns the current number of entries in the cache
func (c *Cache[T]) Size() int {
	return c.lru.Len()
}

// Name returns the cache name for metrics
func (c *Cache[T]) Name() string {
	return c.name
}

// StartEviction is a no-op retained for API compatibility.
// The underlying LRU handles TTL-based eviction automatically.
func (c *Cache[T]) StartEviction(_ context.Context) {}

// Store adds or updates an item in the cache with metadata from the file
func (c *Cache[T]) Store(fileName string, data T, fi os.FileInfo) {
	c.lru.Add(fileName, entry[T]{
		data:         data,
		size:         fi.Size(),
		lastModified: fi.ModTime().UnixNano(),
	})
}

// Invalidate removes an item from the cache
func (c *Cache[T]) Invalidate(fileName string) {
	c.lru.Remove(fileName)
}

// InvalidateAll removes all cached items.
func (c *Cache[T]) InvalidateAll() {
	c.lru.Purge()
}

// LoadLatest gets the latest version of an item, loading it if stale or missing
func (c *Cache[T]) LoadLatest(
	filePath string, loader func() (T, error),
) (T, error) {
	return c.LoadLatestByKey(filePath, filePath, loader)
}

// LoadLatestByKey gets the latest version of an item under a caller-provided cache key.
func (c *Cache[T]) LoadLatestByKey(
	key, filePath string, loader func() (T, error),
) (T, error) {
	stale, fi, err := c.isStaleByKey(key, filePath)
	if err != nil {
		var zero T
		return zero, err
	}
	if !stale {
		if e, ok := c.lru.Get(key); ok {
			return e.data, nil
		}
	}
	data, err := loader()
	if err != nil {
		var zero T
		return zero, err
	}
	c.Store(key, data, fi)
	return data, nil
}

// Load retrieves an item from the cache if it exists
func (c *Cache[T]) Load(fileName string) (T, bool) {
	e, ok := c.lru.Get(fileName)
	if !ok {
		var zero T
		return zero, false
	}
	return e.data, true
}

// IsStale checks if a cached entry is stale compared to the file on disk
// by comparing modification time and size
func (c *Cache[T]) IsStale(fileName string) (bool, os.FileInfo, error) {
	return c.isStale(fileName)
}

func (c *Cache[T]) isStale(fileName string) (bool, os.FileInfo, error) {
	return c.isStaleByKey(fileName, fileName)
}

func (c *Cache[T]) isStaleByKey(key, filePath string) (bool, os.FileInfo, error) {
	fi, err := os.Stat(filePath)
	if err != nil {
		return true, fi, fmt.Errorf("failed to stat file %s: %w", filePath, err)
	}
	e, ok := c.lru.Peek(key)
	if !ok {
		return true, fi, nil
	}
	t := fi.ModTime().UnixNano()
	return e.lastModified < t || e.size != fi.Size(), fi, nil
}
