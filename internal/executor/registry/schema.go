// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package registry

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
)

// executorSchemaRegistry holds registered JSON schemas for executor configs.
type executorSchemaRegistry struct {
	mu      sync.RWMutex
	entries map[string]*schemaEntry
}

type schemaEntry struct {
	schema      *jsonschema.Schema
	resolved    atomic.Pointer[jsonschema.Resolved]
	resolveOnce sync.Once
	resolveErr  error
}

var executorSchemas = &executorSchemaRegistry{
	entries: make(map[string]*schemaEntry),
}

// RegisterExecutorConfigSchema registers a JSON schema for an executor config.
// The schema is used to validate executor config at DAG parse time.
func RegisterExecutorConfigSchema(executorType string, schema *jsonschema.Schema) {
	executorSchemas.mu.Lock()
	defer executorSchemas.mu.Unlock()
	executorSchemas.entries[executorType] = &schemaEntry{schema: schema}
}

// ValidateExecutorConfig validates config against the registered schema.
// Returns nil if no schema is registered (backward compatible).
func ValidateExecutorConfig(executorType string, config map[string]any) error {
	executorSchemas.mu.RLock()
	entry, ok := executorSchemas.entries[executorType]
	executorSchemas.mu.RUnlock()

	if !ok {
		return nil // No schema - skip validation
	}

	resolved, err := entry.getResolved()
	if err != nil {
		return fmt.Errorf("schema error for %s: %w", executorType, err)
	}

	if err := resolved.Validate(config); err != nil {
		err = fmt.Errorf("invalid %s config: %w", executorType, err)
		if hint := unknownKeyHint(entry.schema, config); hint != "" {
			err = fmt.Errorf("%w; %s", err, hint)
		}
		return err
	}

	return nil
}

// unknownKeyHint suggests a defined key for each top-level key that a schema
// closed to additional properties does not define, when one is close enough
// to be a likely misspelling. It returns "" when there is nothing to suggest.
func unknownKeyHint(schema *jsonschema.Schema, config map[string]any) string {
	if !forbidsAdditionalProperties(schema) {
		return ""
	}
	var hints []string
	for _, key := range slices.Sorted(maps.Keys(config)) {
		if _, ok := schema.Properties[key]; ok {
			continue
		}
		if near := closestKey(key, schema.Properties); near != "" {
			hints = append(hints, fmt.Sprintf("%q instead of %q", near, key))
		}
	}
	if len(hints) == 0 {
		return ""
	}
	return "did you mean " + strings.Join(hints, ", ") + "?"
}

// forbidsAdditionalProperties reports whether a schema rejects every key it
// does not define, as additionalProperties: false does.
func forbidsAdditionalProperties(schema *jsonschema.Schema) bool {
	extra := schema.AdditionalProperties
	return extra != nil && extra.Not != nil && reflect.ValueOf(*extra.Not).IsZero()
}

// closestKey returns the defined key nearest to key, ignoring the case of
// key, or "" when none is within one edit per three characters of key.
func closestKey(key string, properties map[string]*jsonschema.Schema) string {
	want := strings.ToLower(key)
	best, bestDistance := "", utf8.RuneCountInString(want)/3+1
	for _, name := range slices.Sorted(maps.Keys(properties)) {
		if distance := editDistance(want, name); distance < bestDistance {
			best, bestDistance = name, distance
		}
	}
	return best
}

// editDistance is the Levenshtein distance between two strings.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	previous := make([]int, len(rb)+1)
	current := make([]int, len(rb)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		current[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(rb)]
}

func (e *schemaEntry) getResolved() (*jsonschema.Resolved, error) {
	e.resolveOnce.Do(func() {
		resolved, err := e.schema.Resolve(&jsonschema.ResolveOptions{
			ValidateDefaults: true,
		})
		if err != nil {
			e.resolveErr = err
			return
		}
		e.resolved.Store(resolved)
	})

	if e.resolveErr != nil {
		return nil, e.resolveErr
	}
	return e.resolved.Load(), nil
}
