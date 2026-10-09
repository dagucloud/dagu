// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package replaycache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// Store locates the recorded operations that steps replay on later runs.
// Records are kept per DAG name and step key (the step ID, or the step name
// when the step has no ID).
type Store struct {
	dir string
}

// New returns a store rooted under a step type's data directory.
func New(dataDir string) *Store {
	return &Store{dir: filepath.Join(dataDir, replayCacheDirName)}
}

// Path returns the file that holds the records of a step.
func (c *Store) Path(dagName, stepKey string) string {
	return filepath.Join(c.dagDir(dagName), fileutil.SafeName(stepKey)+replayCacheFileExt)
}

// Steps returns the sorted keys of the DAG's steps that have records. A step
// name is returned in the file-safe form its records are stored under.
func (c *Store) Steps(dagName string) ([]string, error) {
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
func (c *Store) Clear(dagName, stepKey string) ([]string, error) {
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

// Drop removes the records of one step that drop accepts, under the lock a
// run takes to change the file, and returns how many it removed. A step
// without records has nothing to drop.
func (c *Store) Drop(ctx context.Context, dagName, stepKey string, drop func(entry json.RawMessage) bool) (int, error) {
	if dagName == "" || stepKey == "" {
		return 0, errors.New("dag name and step are required")
	}
	path := c.Path(dagName, stepKey)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	removed := 0
	err := Open[json.RawMessage](path).update(ctx, func(entries map[string]json.RawMessage) {
		for key, entry := range entries {
			if drop(entry) {
				delete(entries, key)
				removed++
			}
		}
	})
	return removed, err
}

// dagDir keeps DAGs whose names differ only in characters SafeName replaces,
// such as "etl.daily" and "etl_daily", in separate directories. The 128-bit
// suffix keeps crafted names from sharing one.
func (c *Store) dagDir(dagName string) string {
	name := fileutil.SafeName(dagName)
	if name != dagName {
		sum := sha256.Sum256([]byte(dagName))
		name += "-" + hex.EncodeToString(sum[:16])
	}
	return filepath.Join(c.dir, name)
}
