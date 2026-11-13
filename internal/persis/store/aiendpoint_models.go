// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ aiendpoint.ModelsStore = (*AIEndpointModelsStore)(nil)

// AIEndpointModelsStore keeps the models lists of the AI endpoints (Spec 093) in
// a collection. A record ID is the ID of the record of the endpoint:
// "global/<name>" or "workspaces/<workspace>/<name>". The record never holds a key.
type AIEndpointModelsStore struct {
	col persis.Collection
}

type aiEndpointModelsRecord struct {
	Scope  string             `json:"scope"`
	Name   string             `json:"name"`
	Models *aiendpoint.Models `json:"models"`
}

// NewAIEndpointModelsStore creates an AIEndpointModelsStore backed by col.
func NewAIEndpointModelsStore(col persis.Collection) (*AIEndpointModelsStore, error) {
	if col == nil {
		return nil, errors.New("AI endpoint models store: collection cannot be nil")
	}
	return &AIEndpointModelsStore{col: col}, nil
}

func (s *AIEndpointModelsStore) Get(ctx context.Context, scope aiendpoint.Scope, name string) (*aiendpoint.Models, error) {
	id, err := aiEndpointRecordID(scope, name)
	if err != nil {
		return nil, err
	}
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, aiendpoint.ErrNotFound
		}
		return nil, err
	}
	m, err := decodeAIEndpointModels(rec)
	if err != nil {
		// A list that cannot be read is no list: the next read replaces it.
		logger.Warn(ctx, "Skipped an AI models list that cannot be read", tag.Name(rec.ID), tag.Error(err))
		return nil, aiendpoint.ErrNotFound
	}
	return m, nil
}

func (s *AIEndpointModelsStore) List(ctx context.Context) ([]*aiendpoint.Models, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	out := make([]*aiendpoint.Models, 0, len(recs))
	for _, rec := range recs {
		m, err := decodeAIEndpointModels(rec)
		if err != nil {
			logger.Warn(ctx, "Skipped an AI models list that cannot be read", tag.Name(rec.ID), tag.Error(err))
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *AIEndpointModelsStore) Put(ctx context.Context, m *aiendpoint.Models) error {
	if m == nil {
		return errors.New("AI endpoint models store: models cannot be nil")
	}
	id, err := aiEndpointRecordID(m.Scope, m.Name)
	if err != nil {
		return err
	}
	data, err := persis.Encode(&aiEndpointModelsRecord{Scope: m.Scope.String(), Name: m.Name, Models: m.Clone()})
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.col.Put(ctx, &persis.Record{ID: id, Data: data, CreatedAt: now, UpdatedAt: now})
}

func (s *AIEndpointModelsStore) Delete(ctx context.Context, scope aiendpoint.Scope, name string) error {
	id, err := aiEndpointRecordID(scope, name)
	if err != nil {
		return err
	}
	return s.col.Delete(ctx, id)
}

func decodeAIEndpointModels(rec *persis.Record) (*aiendpoint.Models, error) {
	var stored aiEndpointModelsRecord
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("AI endpoint models store: decode %q: %w", rec.ID, err)
	}
	if stored.Models == nil {
		return nil, fmt.Errorf("AI endpoint models store: decode %q: missing models payload", rec.ID)
	}
	scope, err := aiendpoint.ParseScope(stored.Scope)
	if err != nil || stored.Name == "" || !strings.HasSuffix(rec.ID, "/"+stored.Name) {
		return nil, fmt.Errorf("AI endpoint models store: decode %q: the record names another endpoint", rec.ID)
	}
	m := stored.Models.Clone()
	m.Scope, m.Name = scope, stored.Name
	return m, nil
}
