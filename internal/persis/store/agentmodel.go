// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/persis"
)

var _ agent.ModelStore = (*AgentModelStore)(nil)

// AgentModelStore implements [agent.ModelStore] over [persis.Collection],
// keeping an in-memory name→id index rebuilt on startup.
type AgentModelStore struct {
	col persis.Collection

	mu     sync.RWMutex
	byName map[string]string
}

// NewAgentModelStore creates an AgentModelStore backed by col.
func NewAgentModelStore(col persis.Collection) (*AgentModelStore, error) {
	s := &AgentModelStore{
		col:    col,
		byName: make(map[string]string),
	}
	if err := s.rebuildIndex(context.Background()); err != nil {
		return nil, fmt.Errorf("agent-model store: build index: %w", err)
	}
	return s, nil
}

func (s *AgentModelStore) rebuildIndex(ctx context.Context) error {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range recs {
		var model agent.ModelConfig
		if err := persis.Decode(rec, &model); err != nil {
			continue
		}
		s.byName[model.Name] = model.ID
	}
	return nil
}

// Create stores a new model configuration.
func (s *AgentModelStore) Create(ctx context.Context, model *agent.ModelConfig) error {
	if model == nil {
		return errors.New("agent-model store: model cannot be nil")
	}
	if err := agent.ValidateModelID(model.ID); err != nil {
		return err
	}
	if model.Name == "" {
		return errors.New("agent-model store: model name is required")
	}

	data, err := persis.Encode(model)
	if err != nil {
		return fmt.Errorf("agent-model store: encode: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byName[model.Name]; exists {
		return agent.ErrModelNameAlreadyExists
	}
	if _, err := s.col.Get(ctx, model.ID); err == nil {
		return agent.ErrModelAlreadyExists
	}
	if err := s.col.Put(ctx, &persis.Record{ID: model.ID, Data: data}); err != nil {
		return fmt.Errorf("agent-model store: create: %w", err)
	}
	s.byName[model.Name] = model.ID
	return nil
}

// GetByID retrieves a model configuration by its ID.
func (s *AgentModelStore) GetByID(ctx context.Context, id string) (*agent.ModelConfig, error) {
	if err := agent.ValidateModelID(id); err != nil {
		return nil, err
	}
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, agent.ErrModelNotFound
		}
		return nil, fmt.Errorf("agent-model store: get: %w", err)
	}
	var model agent.ModelConfig
	if err := persis.Decode(rec, &model); err != nil {
		return nil, fmt.Errorf("agent-model store: decode: %w", err)
	}
	return &model, nil
}

// List returns all model configurations sorted by name.
func (s *AgentModelStore) List(ctx context.Context) ([]*agent.ModelConfig, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	out := make([]*agent.ModelConfig, 0, len(recs))
	for _, rec := range recs {
		var m agent.ModelConfig
		if err := persis.Decode(rec, &m); err != nil {
			continue
		}
		out = append(out, &m)
	}
	slices.SortFunc(out, func(a, b *agent.ModelConfig) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

// Update modifies an existing model configuration.
func (s *AgentModelStore) Update(ctx context.Context, model *agent.ModelConfig) error {
	if model == nil {
		return errors.New("agent-model store: model cannot be nil")
	}
	if err := agent.ValidateModelID(model.ID); err != nil {
		return err
	}
	if model.Name == "" {
		return errors.New("agent-model store: model name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existingRec, err := s.col.Get(ctx, model.ID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return agent.ErrModelNotFound
		}
		return fmt.Errorf("agent-model store: get for update: %w", err)
	}
	var existing agent.ModelConfig
	if err := persis.Decode(existingRec, &existing); err != nil {
		return fmt.Errorf("agent-model store: decode existing: %w", err)
	}

	if existing.Name != model.Name {
		if id, taken := s.byName[model.Name]; taken && id != model.ID {
			return agent.ErrModelNameAlreadyExists
		}
	}
	data, err := persis.Encode(model)
	if err != nil {
		return fmt.Errorf("agent-model store: encode: %w", err)
	}
	if err := s.col.Put(ctx, &persis.Record{ID: model.ID, Data: data}); err != nil {
		return fmt.Errorf("agent-model store: update: %w", err)
	}
	if existing.Name != model.Name {
		delete(s.byName, existing.Name)
		s.byName[model.Name] = model.ID
	}
	return nil
}

// Delete removes a model configuration by ID.
func (s *AgentModelStore) Delete(ctx context.Context, id string) error {
	if err := agent.ValidateModelID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return agent.ErrModelNotFound
		}
		return fmt.Errorf("agent-model store: get for delete: %w", err)
	}
	var model agent.ModelConfig
	if err := persis.Decode(rec, &model); err != nil {
		return fmt.Errorf("agent-model store: decode for delete: %w", err)
	}
	if err := s.col.Delete(ctx, id); err != nil {
		return fmt.Errorf("agent-model store: delete: %w", err)
	}
	delete(s.byName, model.Name)
	return nil
}
