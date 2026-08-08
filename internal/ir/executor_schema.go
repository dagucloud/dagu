// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"fmt"
	"sync"
	"sync/atomic"

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
		return fmt.Errorf("invalid %s config: %w", executorType, err)
	}

	return nil
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
