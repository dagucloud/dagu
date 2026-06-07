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

func MergeResolved(name string, layers ...*Resolved) *Resolved {
	merged := &Resolved{
		Name:      name,
		Variables: make(map[string]string),
		Secrets:   make(map[string]string),
		Entries:   make([]ResolvedEntry, 0),
	}
	entryIndex := make(map[string]int)
	for _, layer := range layers {
		if layer == nil {
			continue
		}
		for _, entry := range layer.Entries {
			if idx, ok := entryIndex[entry.Key]; ok {
				merged.Entries[idx] = entry
			} else {
				entryIndex[entry.Key] = len(merged.Entries)
				merged.Entries = append(merged.Entries, entry)
			}
			switch entry.Kind {
			case EntryKindVariable:
				merged.Variables[entry.Key] = layer.Variables[entry.Key]
				delete(merged.Secrets, entry.Key)
			case EntryKindSecret:
				merged.Secrets[entry.Key] = layer.Secrets[entry.Key]
				delete(merged.Variables, entry.Key)
			}
		}
	}
	return merged
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
	return r.resolve(ctx, p)
}

func (r *Resolver) ResolveInherited(ctx context.Context, ref InheritedRef) (*Resolved, error) {
	if r == nil || r.profileStore == nil {
		return nil, fmt.Errorf("profile store is not configured")
	}
	p, err := r.profileStore.GetInherited(ctx, ref)
	if err != nil {
		return nil, err
	}
	return r.resolve(ctx, p)
}

func (r *Resolver) resolve(ctx context.Context, p *Profile) (*Resolved, error) {
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
