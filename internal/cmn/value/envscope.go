// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
)

// internalSecretPrefix must match secrets.PresolvedEnvPrefix.
// Duplicated here to avoid a circular import (eval <- secrets).
const internalSecretPrefix = "_DAGU_PRESOLVED_SECRET_" //nolint:gosec // Not a credential; this is an env var prefix for transport.

// internalBuildEnvFileKey must match buildenv.PresolvedEnvFileKey.
// Duplicated here to avoid a circular import (eval <- buildenv).
const internalBuildEnvFileKey = "_DAGU_PRESOLVED_BUILD_ENV_FILE"

// EnvSource tracks where an environment variable came from (for debugging)
type EnvSource string

const (
	EnvSourceOS        EnvSource = "os"              // From os.Environ()
	EnvSourceDAGEnv    EnvSource = "dag_env"         // From DAG env: field
	EnvSourceDotEnv    EnvSource = "dotenv"          // From .env file
	EnvSourceParam     EnvSource = "param"           // From params
	EnvSourceOutput    EnvSource = "output"          // From step output
	EnvSourcePresolved EnvSource = "presolved_build" // From presolved build env transport
	EnvSourceSecret    EnvSource = "secret"          // From secrets
	EnvSourceStepEnv   EnvSource = "step_env"        // From step.env field
)

// EnvSourceStep is deprecated: use EnvSourceOutput instead
const EnvSourceStep = EnvSourceOutput

// EnvEntry represents a single environment variable with metadata
type EnvEntry struct {
	Key    string
	Value  string
	Source EnvSource
	Origin string // stepID for outputs, filepath for dotenv (optional metadata)
}

// EnvScope is an isolated, immutable environment scope for DAG loading/execution.
// It does NOT modify the global os.Env.
type EnvScope struct {
	mu      sync.RWMutex
	entries map[string]EnvEntry
	parent  *EnvScope
}

// NewEnvScope creates a new EnvScope, optionally inheriting from parent.
// If includeOS is true, it includes os.Environ() as the base layer.
func NewEnvScope(parent *EnvScope, includeOS bool) *EnvScope {
	e := &EnvScope{
		entries: make(map[string]EnvEntry),
		parent:  parent,
	}
	if includeOS {
		for _, env := range os.Environ() {
			if k, v, ok := strings.Cut(env, "="); ok {
				// Skip internal transport vars for pre-resolved secrets.
				// These are only consumed by the env secret resolver via
				// direct os.LookupEnv; they must not appear in step environments.
				// Must match secrets.PresolvedEnvPrefix (duplicated to avoid
				// circular import: eval <- secrets).
				if strings.HasPrefix(k, internalSecretPrefix) || k == internalBuildEnvFileKey {
					continue
				}
				e.entries[k] = EnvEntry{Key: k, Value: v, Source: EnvSourceOS}
			}
		}
	}
	return e
}

// WithEntry returns a new EnvScope with the given entry added.
// The original scope is not modified (immutable).
func (e *EnvScope) WithEntry(key, value string, source EnvSource) *EnvScope {
	return e.WithEntryOrigin(key, value, source, "")
}

// WithEntryOrigin returns a new EnvScope with the given entry and origin metadata.
// The original scope is not modified (immutable).
func (e *EnvScope) WithEntryOrigin(key, value string, source EnvSource, origin string) *EnvScope {
	newScope := &EnvScope{
		entries: make(map[string]EnvEntry, 1),
		parent:  e,
	}
	newScope.entries[key] = EnvEntry{Key: key, Value: value, Source: source, Origin: origin}
	return newScope
}

// WithEntries returns a new EnvScope with the given entries added.
// The original scope is not modified (immutable).
func (e *EnvScope) WithEntries(entries map[string]string, source EnvSource) *EnvScope {
	if len(entries) == 0 {
		return e
	}
	newScope := &EnvScope{
		entries: make(map[string]EnvEntry, len(entries)),
		parent:  e,
	}
	for k, v := range entries {
		newScope.entries[k] = EnvEntry{Key: k, Value: v, Source: source}
	}
	return newScope
}

// WithStepOutputs returns a new EnvScope with step output variables added.
// The stepID is recorded as the origin for debugging.
func (e *EnvScope) WithStepOutputs(outputs map[string]string, stepID string) *EnvScope {
	if len(outputs) == 0 {
		return e
	}
	newScope := &EnvScope{
		entries: make(map[string]EnvEntry, len(outputs)),
		parent:  e,
	}
	for k, v := range outputs {
		newScope.entries[k] = EnvEntry{Key: k, Value: v, Source: EnvSourceOutput, Origin: stepID}
	}
	return newScope
}

// Get retrieves a variable value, checking this scope then parent scopes.
func (e *EnvScope) Get(key string) (string, bool) {
	entry, ok := e.GetEntry(key)
	if !ok {
		return "", false
	}
	return entry.Value, true
}

// GetEntry retrieves the full entry with metadata, checking this scope then parent scopes.
func (e *EnvScope) GetEntry(key string) (EnvEntry, bool) {
	if e == nil {
		return EnvEntry{}, false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if entry, ok := e.entries[key]; ok {
		return entry, true
	}
	if e.parent != nil {
		return e.parent.GetEntry(key)
	}
	return EnvEntry{}, false
}

// ToSlice returns all variables as KEY=value strings (for cmd.Env)
func (e *EnvScope) ToSlice() []string {
	if e == nil {
		return nil
	}
	all := e.ToMap()
	result := make([]string, 0, len(all))
	for k, v := range all {
		result = append(result, k+"="+v)
	}
	return result
}

// ToMap returns all variables as a map, with child entries overriding parent entries.
func (e *EnvScope) ToMap() map[string]string {
	return e.collectAll(func(_ EnvEntry) bool { return true })
}

// expandWithLookup expands $VAR and ${VAR} using the provided lookup function.
// Single-quoted variables ('$VAR' or '${VAR}') and unknown variables are preserved.
func expandWithLookup(s string, lookup func(key string) (string, bool), recognizeEscapedDollar bool) string {
	matches := reVarSubstitution.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return s
	}

	var b strings.Builder
	last := 0
	for _, loc := range matches {
		b.WriteString(s[last:loc[0]])
		last = loc[1]

		match := s[loc[0]:loc[1]]
		if isSingleQuotedVar(s, loc[0], loc[1]) ||
			(recognizeEscapedDollar && isEscapedDollar(s, loc[0])) {
			b.WriteString(match)
			continue
		}

		var key string
		var unbracedPositional bool
		if loc[2] >= 0 { // Group 1: ${...}
			key = s[loc[2]:loc[3]]
		} else if loc[4] >= 0 { // Group 2: $VAR
			key = s[loc[4]:loc[5]]
		} else if loc[6] >= 0 { // Group 3: positional $1
			key = s[loc[6]:loc[7]]
			unbracedPositional = true
		} else {
			// Neither group captured — preserve original text.
			b.WriteString(match)
			continue
		}

		if !validVariableTokenName(key) ||
			(unbracedPositional && numericVarContinues(s, key, loc[1])) {
			b.WriteString(match)
			continue
		}
		if val, found := lookup(key); found {
			b.WriteString(val)
			continue
		}
		b.WriteString(match)
	}
	b.WriteString(s[last:])
	return b.String()
}

// Expand expands ${VAR} and $VAR in s using this scope.
// Variables not found in the scope are preserved in their original form.
func (e *EnvScope) Expand(s string) string {
	if e == nil {
		return s
	}
	return expandWithLookup(s, e.Get, true)
}

// Debug returns a string representation for debugging
func (e *EnvScope) Debug() string {
	if e == nil {
		return "EnvScope{nil}"
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	var b strings.Builder
	b.WriteString("EnvScope{\n")
	for k, entry := range e.entries {
		fmt.Fprintf(&b, "  %s=%q (source: %s)\n", k, entry.Value, entry.Source)
	}
	if e.parent != nil {
		b.WriteString("  parent: <yes>\n")
	}
	b.WriteString("}")
	return b.String()
}

type envScopeKey struct{}

// WithEnvScope returns a context with the given EnvScope
func WithEnvScope(ctx context.Context, scope *EnvScope) context.Context {
	return context.WithValue(ctx, envScopeKey{}, scope)
}

// GetEnvScope retrieves the EnvScope from context.
// Returns nil if context is nil or no EnvScope is set.
func GetEnvScope(ctx context.Context) *EnvScope {
	if ctx == nil {
		return nil
	}
	if scope, ok := ctx.Value(envScopeKey{}).(*EnvScope); ok {
		return scope
	}
	return nil
}

// AllBySource returns all entries with the given source.
// This is useful for getting all secrets for masking, all params, etc.
func (e *EnvScope) AllBySource(source EnvSource) map[string]string {
	return e.collectAll(func(entry EnvEntry) bool { return entry.Source == source })
}

// AllSecrets returns all entries with EnvSourceSecret.
// This is a convenience method for output masking.
func (e *EnvScope) AllSecrets() map[string]string {
	return e.AllBySource(EnvSourceSecret)
}

// AllUserEnvs returns all entries excluding OS environment.
func (e *EnvScope) AllUserEnvs() map[string]string {
	return e.collectAll(func(entry EnvEntry) bool { return entry.Source != EnvSourceOS })
}

// collectAll gathers entries matching include across the full scope chain.
// Parent entries are collected first so child entries override them.
func (e *EnvScope) collectAll(include func(EnvEntry) bool) map[string]string {
	if e == nil {
		return make(map[string]string)
	}
	result := make(map[string]string)
	e.collect(result, include)
	return result
}

func (e *EnvScope) collect(result map[string]string, include func(EnvEntry) bool) {
	if e == nil {
		return
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.parent != nil {
		e.parent.collect(result, include)
	}
	for k, entry := range e.entries {
		if include(entry) {
			result[k] = entry.Value
		}
	}
}

// Provenance returns a human-readable description of where a variable came from.
// Returns empty string if the variable is not found.
func (e *EnvScope) Provenance(key string) string {
	entry, ok := e.GetEntry(key)
	if !ok {
		return ""
	}
	if entry.Origin != "" {
		return fmt.Sprintf("%s (from %s)", entry.Source, entry.Origin)
	}
	return string(entry.Source)
}
