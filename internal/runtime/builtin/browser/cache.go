// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	cacheFileMode = 0o600
	cacheDirMode  = 0o700
	// cacheLockTimeout bounds the wait for another run of the DAG to finish
	// changing the cache; the change is dropped rather than hold the step.
	cacheLockTimeout = 5 * time.Second
)

// replayCache stores the actions each act operation performed, so later runs
// repeat them without a model call. Entries are keyed by operation position,
// instruction, and page, so an edited instruction or a different page misses.
//
// A step's new recordings are kept only once the step succeeds, and the
// recordings it replayed are dropped when it fails, so an action that did the
// wrong thing is not repeated. Every run of the DAG shares the file, so each
// change is merged into it as it is now, under a lock.
type replayCache struct {
	path    string
	mu      sync.Mutex
	entries map[string][]recordedAction
	// pending holds what this step recorded, until it succeeds.
	pending map[string][]recordedAction
	// used holds the entries this step replayed, as they were read.
	used map[string][]recordedAction
}

func openReplayCache(browserDir, dagName, stepKey string) (*replayCache, error) {
	cache := &replayCache{
		path:    browserhost.NewReplayCache(browserDir).Path(dagName, stepKey),
		pending: map[string][]recordedAction{},
		used:    map[string][]recordedAction{},
	}
	entries, err := readReplayEntries(cache.path)
	if err != nil {
		return nil, err
	}
	cache.entries = entries
	return cache, nil
}

func readReplayEntries(path string) (map[string][]recordedAction, error) {
	entries := map[string][]recordedAction{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return entries, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read replay cache: %w", err)
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		// A corrupt cache only costs model calls; start over.
		return map[string][]recordedAction{}, nil
	}
	return entries, nil
}

// lookup returns the recorded actions for key and marks them used by this
// step, since every hit is replayed.
func (c *replayCache) lookup(key string) ([]recordedAction, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	actions, ok := c.entries[key]
	if !ok || len(actions) == 0 {
		return nil, false
	}
	c.used[key] = actions
	return actions, true
}

// stage records what an act did, to be kept if the step succeeds.
func (c *replayCache) stage(key string, actions []recordedAction) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending[key] = actions
}

// commit keeps what the step recorded.
func (c *replayCache) commit(ctx context.Context) error {
	c.mu.Lock()
	pending := c.pending
	c.pending, c.used = map[string][]recordedAction{}, map[string][]recordedAction{}
	c.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	return c.update(ctx, func(entries map[string][]recordedAction) { maps.Copy(entries, pending) })
}

// evict drops the recordings the step replayed, unless another run replaced
// them since, and forgets what it recorded.
func (c *replayCache) evict(ctx context.Context) error {
	c.mu.Lock()
	used := c.used
	c.pending, c.used = map[string][]recordedAction{}, map[string][]recordedAction{}
	c.mu.Unlock()
	if len(used) == 0 {
		return nil
	}
	return c.update(ctx, func(entries map[string][]recordedAction) {
		for key, actions := range used {
			if reflect.DeepEqual(entries[key], actions) {
				delete(entries, key)
			}
		}
	})
}

// discard forgets the step's recordings and replays without changing the
// file, for a failure that says nothing about them.
func (c *replayCache) discard() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending, c.used = map[string][]recordedAction{}, map[string][]recordedAction{}
}

// held returns what the step recorded and replayed so far, to be carried
// across an ask while the step waits.
func (c *replayCache) held() (pending, used map[string][]recordedAction) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.pending), maps.Clone(c.used)
}

// hold takes back what the step recorded and replayed before an ask.
func (c *replayCache) hold(pending, used map[string][]recordedAction) {
	c.mu.Lock()
	defer c.mu.Unlock()
	maps.Copy(c.pending, pending)
	maps.Copy(c.used, used)
}

// update applies change to the file as it is now. An emptied cache is
// removed, so clearing one leaves nothing behind.
func (c *replayCache) update(ctx context.Context, change func(map[string][]recordedAction)) error {
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, cacheDirMode); err != nil {
		return fmt.Errorf("create replay cache directory: %w", err)
	}
	// The step may already be over its deadline; the change still gets its
	// own short wait.
	lockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheLockTimeout)
	defer cancel()
	lock := dirlock.New(dir, nil)
	if err := lock.Lock(lockCtx); err != nil {
		return fmt.Errorf("lock replay cache: %w", err)
	}
	defer func() { _ = lock.Unlock() }()
	entries, err := readReplayEntries(c.path)
	if err != nil {
		return err
	}
	change(entries)
	if len(entries) == 0 {
		if err := os.Remove(c.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove replay cache: %w", err)
		}
		return nil
	}
	return fileutil.WriteJSONAtomic(c.path, entries, cacheFileMode)
}

// encodeRecordings and decodeRecordings carry recordings through a session
// record, which cannot name their type.
func encodeRecordings(entries map[string][]recordedAction) map[string]json.RawMessage {
	if len(entries) == 0 {
		return nil
	}
	raw := make(map[string]json.RawMessage, len(entries))
	for key, actions := range entries {
		if data, err := json.Marshal(actions); err == nil {
			raw[key] = data
		}
	}
	return raw
}

func decodeRecordings(raw map[string]json.RawMessage) map[string][]recordedAction {
	entries := make(map[string][]recordedAction, len(raw))
	for key, data := range raw {
		var actions []recordedAction
		if json.Unmarshal(data, &actions) == nil {
			entries[key] = actions
		}
	}
	return entries
}

// replayKey identifies an act operation on a page. Query strings and
// fragments are ignored so pagination or tracking parameters still hit.
func replayKey(index int, instruction, pageURL string) string {
	page := pageURL
	if parsed, err := url.Parse(pageURL); err == nil {
		parsed.RawQuery = ""
		parsed.Fragment = ""
		page = parsed.String()
	}
	sum := sha256.Sum256([]byte(strconv.Itoa(index) + "\x00" + instruction + "\x00" + page))
	return hex.EncodeToString(sum[:])
}
