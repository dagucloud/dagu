// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package profile

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/secret"
)

type Resolver struct {
	profileStore Store
	secretStore  secret.Store
}

type Resolved struct {
	Name      string
	Variables map[string]string
	Secrets   map[string]string
	Entries   []ResolvedEntry
}

type ResolvedEntry struct {
	Key  string
	Kind EntryKind
}

func (r *Resolved) EnvVars(kind EntryKind) []string {
	if r == nil {
		return nil
	}
	envs := make([]string, 0, len(r.Entries))
	for _, entry := range r.Entries {
		if entry.Kind != kind {
			continue
		}
		switch kind {
		case EntryKindVariable:
			envs = append(envs, stringutil.NewKeyValue(entry.Key, r.Variables[entry.Key]).String())
		case EntryKindSecret:
			envs = append(envs, stringutil.NewKeyValue(entry.Key, r.Secrets[entry.Key]).String())
		}
	}
	return envs
}

func NewResolver(profileStore Store, secretStore secret.Store) *Resolver {
	return &Resolver{
		profileStore: profileStore,
		secretStore:  secretStore,
	}
}

func (r *Resolver) Resolve(ctx context.Context, name string) (*Resolved, error) {
	if r == nil || r.profileStore == nil {
		return nil, fmt.Errorf("profile store is not configured")
	}
	p, err := r.profileStore.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if p.Status == StatusDisabled {
		return nil, ErrDisabled
	}

	resolved := &Resolved{
		Name:      p.Name,
		Variables: make(map[string]string),
		Secrets:   make(map[string]string),
		Entries:   make([]ResolvedEntry, 0, len(p.Entries)),
	}
	for _, entry := range p.Entries {
		switch entry.Kind {
		case EntryKindVariable:
			resolved.Variables[entry.Key] = entry.Value
		case EntryKindSecret:
			if r.secretStore == nil {
				return nil, fmt.Errorf("secret store is not configured")
			}
			value, _, err := r.secretStore.ResolveValue(ctx, entry.SecretID)
			if err != nil {
				return nil, fmt.Errorf("resolve profile secret %s: %w", entry.Key, err)
			}
			resolved.Secrets[entry.Key] = value
		default:
			return nil, fmt.Errorf("unsupported profile entry kind %q", entry.Kind)
		}
		resolved.Entries = append(resolved.Entries, ResolvedEntry{
			Key:  entry.Key,
			Kind: entry.Kind,
		})
	}
	return resolved, nil
}
