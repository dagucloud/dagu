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
	// stepsDirName holds a DAG's step files, apart from the files released
	// versions kept beside it under names other steps could share.
	stepsDirName = "steps"
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
	return filepath.Join(c.stepsDir(dagName), fileutil.SafeKey(stepKey)+replayCacheFileExt)
}

// Claim readies the file of a step before a run opens it. Released versions
// named the file by the SafeName of the step key, which other keys could
// share; Claim moves it to the step's own file when no other key in
// stepKeys, the keys of all steps of the DAG, had that SafeName. Records
// already in the step's own file stay, and a file several keys had is never
// read. Claim also keeps the key so that Steps returns it in full.
func (c *Store) Claim(ctx context.Context, dagName, stepKey string, stepKeys []string) error {
	path := c.Path(dagName, stepKey)
	if err := fileutil.KeepSafeKeyName(filepath.Dir(path), stepKey); err != nil {
		return err
	}
	dir := c.dagDir(dagName)
	legacy, owned := fileutil.LegacySafeName(stepKey, stepKeys)
	if _, ok := fileutil.LegacyEntry(dir, legacy+replayCacheFileExt); !ok || !owned {
		return nil
	}
	return locked(ctx, filepath.Dir(path), func() error {
		return fileutil.ClaimLegacySafeName(dir, stepKey, replayCacheFileExt, stepKeys, path)
	})
}

// Steps returns the sorted keys of the DAG's steps that have records. Records
// that released versions stored, until a run claims them, are not listed.
func (c *Store) Steps(dagName string) ([]string, error) {
	dir := c.stepsDir(dagName)
	entries, err := os.ReadDir(dir)
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
		steps = append(steps, fileutil.SafeKeyName(dir, step))
	}
	slices.Sort(steps)
	return steps, nil
}

// Clear removes the records of one step, or of every step of the DAG when
// stepKey is empty, and returns the keys of the steps it removed: stepKey
// itself, or the keys Steps reports. Missing records are not an error.
// Clearing a step also removes the file that released versions stored under
// its SafeName, so no claim brings it back.
func (c *Store) Clear(dagName, stepKey string) ([]string, error) {
	if dagName == "" {
		// An empty name maps to the cache root, which holds every DAG.
		return nil, errors.New("dag name is required")
	}
	if stepKey != "" {
		paths := []string{c.Path(dagName, stepKey)}
		dir := c.dagDir(dagName)
		legacy, _ := fileutil.LegacySafeName(stepKey, nil)
		if rel, ok := fileutil.LegacyEntry(dir, legacy+replayCacheFileExt); ok {
			paths = append(paths, filepath.Join(dir, rel))
		}
		removed := false
		for _, path := range paths {
			err := fileutil.Remove(path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			removed = removed || err == nil
		}
		if !removed {
			return nil, nil
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

// Entries returns the records of one step as stored, by key. A step without
// records, or whose file cannot be decoded, has none.
func (c *Store) Entries(dagName, stepKey string) (map[string]json.RawMessage, error) {
	if dagName == "" || stepKey == "" {
		return nil, errors.New("dag name and step are required")
	}
	return read[json.RawMessage](c.Path(dagName, stepKey))
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

func (c *Store) stepsDir(dagName string) string {
	return filepath.Join(c.dagDir(dagName), stepsDirName)
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
	if fileutil.SafeNameEscapesDevice(dagName) {
		name = fileutil.ExistingName(c.dir, name, dagName)
	}
	return filepath.Join(c.dir, name)
}
