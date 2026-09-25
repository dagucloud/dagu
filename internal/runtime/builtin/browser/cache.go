// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	cacheFileMode = 0o600
	cacheDirMode  = 0o700
)

// replayCache stores the actions each act operation performed, so later runs
// repeat them without a model call. Entries are keyed by operation position,
// instruction, and page, so an edited instruction or a different page misses.
type replayCache struct {
	path    string
	mu      sync.Mutex
	entries map[string][]recordedAction
}

func openReplayCache(browserDir, dagName, stepKey string) (*replayCache, error) {
	cache := &replayCache{
		path:    browserhost.NewReplayCache(browserDir).Path(dagName, stepKey),
		entries: map[string][]recordedAction{},
	}
	data, err := os.ReadFile(cache.path)
	if errors.Is(err, os.ErrNotExist) {
		return cache, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read replay cache: %w", err)
	}
	if err := json.Unmarshal(data, &cache.entries); err != nil {
		// A corrupt cache only costs model calls; start over.
		cache.entries = map[string][]recordedAction{}
	}
	return cache, nil
}

func (c *replayCache) lookup(key string) ([]recordedAction, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	actions, ok := c.entries[key]
	return actions, ok && len(actions) > 0
}

func (c *replayCache) store(key string, actions []recordedAction) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = actions
	if err := os.MkdirAll(filepath.Dir(c.path), cacheDirMode); err != nil {
		return fmt.Errorf("create replay cache directory: %w", err)
	}
	return fileutil.WriteJSONAtomic(c.path, c.entries, cacheFileMode)
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
