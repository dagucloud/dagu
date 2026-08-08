// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package buildenv

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// PresolvedEnvFileKey is the env var key used to reference a secure transport
// file carrying pre-resolved DAG/base-config env values from a parent process
// to a subprocess.
const PresolvedEnvFileKey = "_DAGU_PRESOLVED_BUILD_ENV_FILE"

// Snapshot carries environment values and whether runtime resolution is complete.
type Snapshot struct {
	Env             map[string]string `json:"env,omitempty"`
	RuntimeResolved bool              `json:"runtimeResolved,omitempty"`
}

// NewSnapshot creates a snapshot from KEY=value entries.
func NewSnapshot(env []string, runtimeResolved bool) Snapshot {
	return Snapshot{Env: ToMap(env), RuntimeResolved: runtimeResolved}
}

// Prepare writes a snapshot to a secure temp file and returns the transport env
// vars plus a cleanup function.
func Prepare(snapshot Snapshot) ([]string, func() error, error) {
	if len(snapshot.Env) == 0 && !snapshot.RuntimeResolved {
		return nil, nil, nil
	}

	file, err := os.CreateTemp("", "dagu-buildenv-*")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create presolved build env file: %w", err)
	}
	path := file.Name()

	cleanup := func() error {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove presolved build env file: %w", err)
		}
		return nil
	}

	encoder := json.NewEncoder(file)
	if err := encoder.Encode(snapshot); err != nil {
		_ = file.Close()
		_ = cleanup()
		return nil, nil, fmt.Errorf("failed to encode presolved build env file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = cleanup()
		return nil, nil, fmt.Errorf("failed to close presolved build env file: %w", err)
	}

	return []string{PresolvedEnvFileKey + "=" + path}, cleanup, nil
}

// Load returns the environment snapshot currently present in the process.
func Load() (Snapshot, error) {
	path, ok := os.LookupEnv(PresolvedEnvFileKey)
	if !ok || path == "" {
		return Snapshot{}, nil
	}

	data, err := os.ReadFile(path) //nolint:gosec // Path comes from parent-created internal transport env.
	if err != nil {
		return Snapshot{}, fmt.Errorf("failed to read presolved build env file: %w", err)
	}

	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err == nil && (snapshot.Env != nil || snapshot.RuntimeResolved) {
		return snapshot, nil
	}

	// Older launchers wrote the environment map directly.
	var legacy map[string]string
	if err := json.Unmarshal(data, &legacy); err != nil {
		return Snapshot{}, fmt.Errorf("failed to decode presolved build env file: %w", err)
	}
	return Snapshot{Env: legacy}, nil
}

// ToMap converts env entries into a map. Duplicate keys are collapsed so the
// last value wins.
func ToMap(env []string) map[string]string {
	if len(env) == 0 {
		return nil
	}

	entries := make(map[string]string)
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			continue
		}
		entries[key] = value
	}
	if len(entries) == 0 {
		return nil
	}
	return entries
}

// FromMap converts env entries into a deterministic KEY=value slice.
func FromMap(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}

	keys := make([]string, 0, len(env))
	for key := range env {
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	entries := make([]string, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, key+"="+env[key])
	}
	return entries
}

// AppendMissing appends env entries whose keys are absent from base.
// Duplicate extra keys use the last extra value, matching env slice semantics.
func AppendMissing(base []string, extras ...[]string) []string {
	result := append([]string{}, base...)
	seen := make(map[string]struct{})
	for _, entry := range result {
		key, _, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			seen[key] = struct{}{}
		}
	}

	type indexedEntry struct {
		index int
		key   string
		entry string
	}

	var entries []indexedEntry
	lastIndex := make(map[string]int)
	for _, extra := range extras {
		for _, entry := range extra {
			key, _, ok := strings.Cut(entry, "=")
			if !ok || key == "" {
				continue
			}
			index := len(entries)
			entries = append(entries, indexedEntry{
				index: index,
				key:   key,
				entry: entry,
			})
			lastIndex[key] = index
		}
	}

	for _, item := range entries {
		if _, ok := seen[item.key]; ok {
			continue
		}
		if lastIndex[item.key] != item.index {
			continue
		}
		result = append(result, item.entry)
		seen[item.key] = struct{}{}
	}

	return result
}
