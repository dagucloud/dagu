// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	replayCacheDirName = "cache"
	replayCacheFileExt = ".json"
)

// ReplayCache locates the recorded act operations that browser steps replay
// on later runs. Records are kept per DAG name and step key (the step ID, or
// the step name when the step has no ID).
type ReplayCache struct {
	dir string
}

// NewReplayCache returns a replay cache rooted under the browser data
// directory.
func NewReplayCache(browserDataDir string) *ReplayCache {
	return &ReplayCache{dir: filepath.Join(browserDataDir, replayCacheDirName)}
}

// Path returns the file that holds the records of a step.
func (c *ReplayCache) Path(dagName, stepKey string) string {
	return filepath.Join(c.dagDir(dagName), fileutil.SafeName(stepKey)+replayCacheFileExt)
}

// Steps returns the sorted keys of the DAG's steps that have records. A step
// name is returned in the file-safe form its records are stored under.
func (c *ReplayCache) Steps(dagName string) ([]string, error) {
	entries, err := os.ReadDir(c.dagDir(dagName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var steps []string
	for _, entry := range entries {
		step, ok := strings.CutSuffix(entry.Name(), replayCacheFileExt)
		if entry.IsDir() || !ok {
			continue
		}
		steps = append(steps, step)
	}
	slices.Sort(steps)
	return steps, nil
}

// Clear removes the records of one step, or of every step of the DAG when
// stepKey is empty, and returns the keys of the steps it removed: stepKey
// itself, or the keys Steps reports. Missing records are not an error.
func (c *ReplayCache) Clear(dagName, stepKey string) ([]string, error) {
	if dagName == "" {
		// An empty name maps to the cache root, which holds every DAG.
		return nil, errors.New("dag name is required")
	}
	if stepKey != "" {
		err := fileutil.Remove(c.Path(dagName, stepKey))
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return []string{stepKey}, nil
	}
	steps, err := c.Steps(dagName)
	if err != nil {
		return nil, err
	}
	if err := fileutil.RemoveAll(c.dagDir(dagName)); err != nil {
		return nil, err
	}
	return steps, nil
}

// dagDir keeps DAGs whose names differ only in characters SafeName replaces,
// such as "etl.daily" and "etl_daily", in separate directories. The 128-bit
// suffix keeps crafted names from sharing one.
func (c *ReplayCache) dagDir(dagName string) string {
	name := fileutil.SafeName(dagName)
	if name != dagName {
		sum := sha256.Sum256([]byte(dagName))
		name += "-" + hex.EncodeToString(sum[:16])
	}
	return filepath.Join(c.dir, name)
}
