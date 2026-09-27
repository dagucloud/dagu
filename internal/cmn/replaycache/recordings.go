// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package replaycache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	fileMode = 0o600
	dirMode  = 0o700
	// lockTimeout bounds the wait for another run of the DAG to finish
	// changing the file; the change is dropped rather than hold the step.
	lockTimeout = 5 * time.Second
)

// Recordings is one step attempt's view of the recordings in a step's file,
// keyed by operation.
//
// What the attempt records is kept only once it succeeds, and what it
// replayed can be dropped when it fails, so a recording that did the wrong
// thing is not repeated. Every run of the DAG shares the file, so each
// change is merged into the file as it is then, under a lock.
type Recordings[T any] struct {
	path    string
	mu      sync.Mutex
	entries map[string]T
	// pending holds what the attempt recorded, until it succeeds.
	pending map[string]T
	// used holds the entries the attempt replayed, as they were read.
	used map[string]T
}

// Open reads the recordings in the file at path. A missing or corrupt file
// holds none, since a lost recording only costs model calls.
func Open[T any](path string) (*Recordings[T], error) {
	entries, err := read[T](path)
	if err != nil {
		return nil, err
	}
	return &Recordings[T]{path: path, entries: entries, pending: map[string]T{}, used: map[string]T{}}, nil
}

// Lookup returns the recording for key and counts it as replayed.
func (r *Recordings[T]) Lookup(key string) (T, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entries[key]
	if ok {
		r.used[key] = entry
	}
	return entry, ok
}

// Stage records what an operation did, to be kept by Commit.
func (r *Recordings[T]) Stage(key string, entry T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending[key] = entry
}

// Commit keeps what the attempt recorded.
func (r *Recordings[T]) Commit(ctx context.Context) error {
	pending, _ := r.take()
	if len(pending) == 0 {
		return nil
	}
	return r.update(ctx, func(entries map[string]T) { maps.Copy(entries, pending) })
}

// Evict drops the recordings the attempt replayed, unless another run
// replaced them since, and forgets what it recorded.
func (r *Recordings[T]) Evict(ctx context.Context) error {
	_, used := r.take()
	if len(used) == 0 {
		return nil
	}
	return r.update(ctx, func(entries map[string]T) {
		for key, entry := range used {
			if current, ok := entries[key]; ok && reflect.DeepEqual(current, entry) {
				delete(entries, key)
			}
		}
	})
}

// Discard forgets what the attempt recorded and replayed without changing
// the file.
func (r *Recordings[T]) Discard() {
	_, _ = r.take()
}

// Held returns what the attempt recorded and replayed so far, encoded to be
// carried across a pause for human input.
func (r *Recordings[T]) Held() (pending, used map[string]json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return encode(r.pending), encode(r.used)
}

// Hold takes back what Held returned before a pause.
func (r *Recordings[T]) Hold(pending, used map[string]json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	maps.Copy(r.pending, decode[T](pending))
	maps.Copy(r.used, decode[T](used))
}

func (r *Recordings[T]) take() (pending, used map[string]T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending, used = r.pending, r.used
	r.pending, r.used = map[string]T{}, map[string]T{}
	return pending, used
}

// update applies change to the file as it is now. An emptied file is
// removed, so clearing a cache leaves nothing behind.
func (r *Recordings[T]) update(ctx context.Context, change func(map[string]T)) error {
	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create replay cache directory: %w", err)
	}
	// The step may already be over its deadline; the change still gets its
	// own short wait.
	lockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockTimeout)
	defer cancel()
	lock := dirlock.New(dir, nil)
	if err := lock.Lock(lockCtx); err != nil {
		return fmt.Errorf("lock replay cache: %w", err)
	}
	defer func() { _ = lock.Unlock() }()
	entries, err := read[T](r.path)
	if err != nil {
		return err
	}
	change(entries)
	if len(entries) == 0 {
		if err := os.Remove(r.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove replay cache: %w", err)
		}
		return nil
	}
	return fileutil.WriteJSONAtomic(r.path, entries, fileMode)
}

func read[T any](path string) (map[string]T, error) {
	entries := map[string]T{}
	data, err := os.ReadFile(path) //nolint:gosec // The path comes from Store.Path under the data directory.
	if errors.Is(err, os.ErrNotExist) {
		return entries, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read replay cache: %w", err)
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return map[string]T{}, nil
	}
	return entries, nil
}

func encode[T any](entries map[string]T) map[string]json.RawMessage {
	if len(entries) == 0 {
		return nil
	}
	raw := make(map[string]json.RawMessage, len(entries))
	for key, entry := range entries {
		if data, err := json.Marshal(entry); err == nil {
			raw[key] = data
		}
	}
	return raw
}

func decode[T any](raw map[string]json.RawMessage) map[string]T {
	entries := make(map[string]T, len(raw))
	for key, data := range raw {
		var entry T
		if json.Unmarshal(data, &entry) == nil {
			entries[key] = entry
		}
	}
	return entries
}
