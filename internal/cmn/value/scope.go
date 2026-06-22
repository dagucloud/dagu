// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import "maps"

type Values map[string]any

// BuiltinContext contains scalar Dagu-managed context values for strict
// value references such as ${context.run.id} and compatibility aliases such as
// ${run.id}.
type BuiltinContext struct {
	values map[string]string
}

// NewBuiltinContext returns a built-in context with the provided scalar values.
func NewBuiltinContext(values map[string]string) BuiltinContext {
	if len(values) == 0 {
		return BuiltinContext{}
	}
	cloned := make(map[string]string, len(values))
	maps.Copy(cloned, values)
	return BuiltinContext{values: cloned}
}

// Value returns a scalar built-in context value.
func (c BuiltinContext) Value(path string) (string, bool) {
	if len(c.values) == 0 {
		return "", false
	}
	canonical, ok := canonicalBuiltinContextPath(path)
	if !ok {
		canonical = path
	}
	if value, ok := c.values[canonical]; ok {
		return value, true
	}
	if legacy, ok := legacyBuiltinContextPath(canonical); ok {
		value, ok := c.values[legacy]
		return value, ok
	}
	return "", false
}

// StaticScope contains declarations and contracts used by static validation.
type StaticScope struct {
	Consts Values
	Params Values
}

// RuntimeScope contains actual values available during runtime resolution.
type RuntimeScope struct {
	Consts         Values
	Params         Values
	Env            *EnvScope
	Steps          map[string]StepInfo
	Foreach        Values
	BuiltinContext BuiltinContext
}

// ValuesFromStrings converts string variables into binding values.
func ValuesFromStrings(values map[string]string) Values {
	if len(values) == 0 {
		return nil
	}
	out := make(Values, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
