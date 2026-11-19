// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runctx

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
)

// SecretsForMasking returns the NAME=value pairs of every secret of the run,
// also of the secrets that the current step does not get.
func (e Context) SecretsForMasking() []string {
	pairs := e.RunSecrets.Pairs()
	if e.EnvScope != nil {
		secrets := e.EnvScope.AllSecrets()
		for _, key := range slices.Sorted(maps.Keys(secrets)) {
			pairs = append(pairs, key+"="+secrets[key])
		}
	}
	return pairs
}

// StepContext returns the context of the run for one step. Its scope keeps
// only the secrets that the step gets (Spec 092): an untrusted step gets no
// secret, and a step of a DAG with step scope gets the secrets of its list. A
// step with no name stands for the DAG itself and keeps the scope.
//
// With fetch, the step makes a request for each deferred registry secret of its
// list. Without fetch, only a value that the step fetched before joins the
// scope. On an error, the returned context has the filtered scope without the
// value.
func (e Context) StepContext(ctx context.Context, step ir.Step, fetch bool) (Context, error) {
	if step.Name == "" || e.EnvScope == nil {
		return e, nil
	}
	policy := e.DAG.SecretScopeOf(step)
	if !policy.Scoped {
		return e, nil
	}
	e.EnvScope = e.EnvScope.Without(func(entry cmnvalue.EnvEntry) bool {
		return entry.Source == cmnvalue.EnvSourceSecret && !policy.Allows(entry.Key)
	})
	fetched := make(map[string]string)
	for _, name := range policy.Names {
		if !e.RunSecrets.IsDeferred(name) {
			continue
		}
		if value, ok := e.RunSecrets.Fetched(step.Name, name); ok {
			fetched[name] = value
			continue
		}
		if !fetch {
			continue
		}
		value, err := e.RunSecrets.Fetch(ctx, step.Name, name)
		if err != nil {
			return e, err
		}
		fetched[name] = value
	}
	e.EnvScope = e.EnvScope.WithEntries(fetched, cmnvalue.EnvSourceSecret)
	return e, nil
}

// StepSecretResolver fetches the value of a registry secret for one step
// (Spec 092). The coordinator gives the value only to a step that lists it.
type StepSecretResolver interface {
	ResolveStepReference(ctx context.Context, step string, ref secretref.Ref) (string, error)
}

// RunSecrets holds the values of the secrets of one run. Each log and each
// record of the run masks every value that it holds, also the values that a
// step does not get. It also holds the registry secrets that a step fetches
// when it starts. A RunSecrets is safe for concurrent use; the copies of a
// Context share one.
type RunSecrets struct {
	mu       sync.RWMutex
	pairs    []string
	seen     map[string]struct{}
	version  int
	deferred map[string]secretref.Ref
	resolver StepSecretResolver
	fetched  map[stepSecretKey]string
}

type stepSecretKey struct {
	step string
	name string
}

// NewRunSecrets returns a holder with the NAME=value pairs. resolver fetches
// the deferred registry secrets; it can be nil when deferred is empty.
func NewRunSecrets(pairs []string, deferred []secretref.Ref, resolver StepSecretResolver) *RunSecrets {
	s := &RunSecrets{
		seen:     make(map[string]struct{}),
		deferred: make(map[string]secretref.Ref, len(deferred)),
		resolver: resolver,
		fetched:  make(map[stepSecretKey]string),
	}
	for _, ref := range deferred {
		s.deferred[ref.Name] = ref
	}
	s.AddPairs(pairs...)
	return s
}

// AddPairs adds NAME=value pairs to mask.
func (s *RunSecrets) AddPairs(pairs ...string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, pair := range pairs {
		if _, value, ok := strings.Cut(pair, "="); !ok || value == "" {
			continue
		}
		if _, dup := s.seen[pair]; dup {
			continue
		}
		s.seen[pair] = struct{}{}
		s.pairs = append(s.pairs, pair)
		s.version++
	}
}

// AddMap adds the entries of values to mask.
func (s *RunSecrets) AddMap(values map[string]string) {
	if s == nil || len(values) == 0 {
		return
	}
	keys := slices.Sorted(maps.Keys(values))
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+values[key])
	}
	s.AddPairs(pairs...)
}

// Pairs returns the NAME=value pairs to mask.
func (s *RunSecrets) Pairs() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.pairs)
}

// Version changes each time that a pair is added.
func (s *RunSecrets) Version() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// IsDeferred reports whether the value of the secret comes when a step that
// lists it starts.
func (s *RunSecrets) IsDeferred(name string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.deferred[name]
	return ok
}

// Fetched returns the value that the step fetched before, with no request.
func (s *RunSecrets) Fetched(step, name string) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.fetched[stepSecretKey{step: step, name: name}]
	return value, ok
}

// ErrStepSecretUnavailable is the cause of a failed fetch of a deferred
// registry secret.
var ErrStepSecretUnavailable = errors.New("the secret is not available to the step")

// Fetch returns the value of a deferred registry secret for the step. A retry
// or a repetition of the step uses the value of the first fetch. The value
// joins the pairs to mask before Fetch returns it. The error names the step and
// the secret and has no value.
func (s *RunSecrets) Fetch(ctx context.Context, step, name string) (string, error) {
	if value, ok := s.Fetched(step, name); ok {
		return value, nil
	}
	if s == nil {
		return "", fmt.Errorf("step %q: secret %q: %w", step, name, ErrStepSecretUnavailable)
	}
	s.mu.RLock()
	ref, deferred := s.deferred[name]
	resolver := s.resolver
	s.mu.RUnlock()
	if !deferred || resolver == nil {
		return "", fmt.Errorf("step %q: secret %q: %w", step, name, ErrStepSecretUnavailable)
	}
	value, err := resolver.ResolveStepReference(ctx, step, ref)
	if err != nil {
		return "", fmt.Errorf("step %q: secret %q: %w", step, name, err)
	}
	s.AddPairs(name + "=" + value)
	s.mu.Lock()
	s.fetched[stepSecretKey{step: step, name: name}] = value
	s.mu.Unlock()
	return value, nil
}
