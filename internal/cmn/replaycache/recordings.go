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
	"slices"
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
// thing is not repeated. Every run of the DAG shares the file, and runs of a
// step can start together and act one after another, so each lookup reads
// the file as it is then, and each change is merged into it under a lock.
type Recordings[T any] struct {
	path string
	mu   sync.Mutex
	// pending holds what the attempt recorded, until it succeeds. A nil
	// entry removes the recording for its key.
	pending map[string]*T
	// used holds the entries the attempt replayed, as they were read.
	used map[string]T
}

// Open returns an attempt's view of the recordings in the file at path.
func Open[T any](path string) *Recordings[T] {
	return &Recordings[T]{path: path, pending: map[string]*T{}, used: map[string]T{}}
}

// Lookup returns the recording for key as the file holds it now and counts
// it as replayed. A missing, corrupt, or unreadable file holds none, since a
// lost recording only costs model calls.
func (r *Recordings[T]) Lookup(key string) (T, bool) {
	entries, _ := read[T](r.path)
	entry, ok := entries[key]
	if ok {
		r.mu.Lock()
		r.used[key] = entry
		r.mu.Unlock()
	}
	return entry, ok
}

// Find returns the first recording, in key order, that match accepts, as
// the file holds it now, and counts it as replayed. It serves a lookup
// whose key is not known in advance, such as a recording made for a sheet
// of another shape that still answers this one. A missing, corrupt, or
// unreadable file holds none.
func (r *Recordings[T]) Find(match func(key string, entry T) bool) (string, T, bool) {
	entries, _ := read[T](r.path)
	for _, key := range slices.Sorted(maps.Keys(entries)) {
		entry := entries[key]
		if !match(key, entry) {
			continue
		}
		r.mu.Lock()
		r.used[key] = entry
		r.mu.Unlock()
		return key, entry, true
	}
	var none T
	return "", none, false
}

// Stage records what an operation did, to be kept by Commit.
func (r *Recordings[T]) Stage(key string, entry T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending[key] = &entry
}

// Drop marks the recording the attempt looked up for key as one that no
// longer replays, to be removed by Commit unless another run replaced it
// since. A later Stage for key takes its place.
func (r *Recordings[T]) Drop(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending[key] = nil
}

// Commit keeps what the attempt recorded and removes what it dropped.
func (r *Recordings[T]) Commit(ctx context.Context) error {
	pending, used := r.take()
	if len(pending) == 0 {
		return nil
	}
	return r.update(ctx, func(entries map[string]T) {
		for key, entry := range pending {
			if entry != nil {
				entries[key] = *entry
			} else if replayed, ok := used[key]; ok {
				removeUnchanged(entries, key, replayed)
			}
		}
	})
}

// Evict drops the recordings the attempt replayed, unless another run
// replaced them since, and forgets what it recorded.
func (r *Recordings[T]) Evict(ctx context.Context) error {
	_, used := r.take()
	if len(used) == 0 {
		return nil
	}
	return r.update(ctx, func(entries map[string]T) {
		for key, replayed := range used {
			removeUnchanged(entries, key, replayed)
		}
	})
}

// Discard forgets what the attempt recorded and replayed without changing
// the file.
func (r *Recordings[T]) Discard() {
	_, _ = r.take()
}

// Held returns what the attempt recorded, dropped, and replayed so far,
// encoded to be carried across a pause for human input.
func (r *Recordings[T]) Held() (pending, used map[string]json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return encode(r.pending), encode(r.used)
}

// Hold takes back what Held returned before a pause.
func (r *Recordings[T]) Hold(pending, used map[string]json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	maps.Copy(r.pending, decode[*T](pending))
	maps.Copy(r.used, decode[T](used))
}

func (r *Recordings[T]) take() (pending map[string]*T, used map[string]T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending, used = r.pending, r.used
	r.pending, r.used = map[string]*T{}, map[string]T{}
	return pending, used
}

// removeUnchanged removes the entry for key unless another run replaced it
// after the attempt replayed it as replayed.
func removeUnchanged[T any](entries map[string]T, key string, replayed T) {
	if current, ok := entries[key]; ok && reflect.DeepEqual(current, replayed) {
		delete(entries, key)
	}
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
	data, err := fileutil.ReadFile(path)
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

func encode[V any](entries map[string]V) map[string]json.RawMessage {
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

func decode[V any](raw map[string]json.RawMessage) map[string]V {
	entries := make(map[string]V, len(raw))
	for key, data := range raw {
		var entry V
		if json.Unmarshal(data, &entry) == nil {
			entries[key] = entry
		}
	}
	return entries
}
