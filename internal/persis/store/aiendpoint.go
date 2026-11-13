// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ aiendpoint.Store = (*AIEndpointStore)(nil)

// AIEndpointStore keeps the metadata of the AI endpoints (Spec 093) in a
// collection. A record ID is "global/<name>" or "workspaces/<workspace>/<name>".
// The record never holds a key.
type AIEndpointStore struct {
	col persis.Collection
}

type aiEndpointStoredRecord struct {
	Endpoint *aiendpoint.Endpoint `json:"endpoint"`
}

// NewAIEndpointStore creates an AIEndpointStore backed by col.
func NewAIEndpointStore(col persis.Collection) (*AIEndpointStore, error) {
	if col == nil {
		return nil, errors.New("AI endpoint store: collection cannot be nil")
	}
	return &AIEndpointStore{col: col}, nil
}

func aiEndpointRecordID(scope aiendpoint.Scope, name string) (string, error) {
	if err := aiendpoint.ValidateName(name); err != nil {
		return "", err
	}
	if scope.IsGlobal() {
		return "global/" + name, nil
	}
	return "workspaces/" + scope.Workspace + "/" + name, nil
}

func (s *AIEndpointStore) Create(ctx context.Context, ep *aiendpoint.Endpoint) error {
	if ep == nil {
		return errors.New("AI endpoint store: endpoint cannot be nil")
	}
	id, err := aiEndpointRecordID(ep.Scope, ep.Name)
	if err != nil {
		return err
	}
	data, err := persis.Encode(&aiEndpointStoredRecord{Endpoint: ep.Clone()})
	if err != nil {
		return err
	}
	err = s.col.Create(ctx, &persis.Record{ID: id, Data: data, CreatedAt: ep.CreatedAt, UpdatedAt: ep.UpdatedAt})
	if errors.Is(err, persis.ErrConflict) {
		return aiendpoint.ErrAlreadyExists
	}
	return err
}

func (s *AIEndpointStore) Get(ctx context.Context, scope aiendpoint.Scope, name string) (*aiendpoint.Endpoint, error) {
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
	return decodeAIEndpoint(rec)
}

func (s *AIEndpointStore) List(ctx context.Context) ([]*aiendpoint.Endpoint, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	out := make([]*aiendpoint.Endpoint, 0, len(recs))
	for _, rec := range recs {
		ep, err := decodeAIEndpoint(rec)
		if err != nil {
			// One bad record must not hide the others from the API and the steps.
			logger.Warn(ctx, "Skipped an AI endpoint that cannot be read", tag.Name(rec.ID), tag.Error(err))
			continue
		}
		out = append(out, ep)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope.Workspace < out[j].Scope.Workspace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (s *AIEndpointStore) Update(ctx context.Context, scope aiendpoint.Scope, name string, change func(*aiendpoint.Endpoint) error) (*aiendpoint.Endpoint, error) {
	id, err := aiEndpointRecordID(scope, name)
	if err != nil {
		return nil, err
	}
	return swapRecord(ctx, s.col, id, decodeAIEndpoint, func(ep *aiendpoint.Endpoint) ([]byte, error) {
		return persis.Encode(&aiEndpointStoredRecord{Endpoint: ep.Clone()})
	}, change)
}

// maxRecordUpdates bounds the reads of swapRecord when other processes write
// the same record.
const maxRecordUpdates = 8

// swapRecord reads the record id, applies change, and saves the result with a
// compare-and-swap on the bytes that it read. A write of another process
// between the read and the save makes it read again, so no change of another
// process is lost. It returns aiendpoint.ErrNotFound when the record does not
// exist, and the error of change as it is.
func swapRecord[T any](ctx context.Context, col persis.Collection, id string,
	decode func(*persis.Record) (T, error), encode func(T) ([]byte, error), change func(T) error) (T, error) {
	var zero T
	for range maxRecordUpdates {
		rec, err := col.Get(ctx, id)
		if errors.Is(err, persis.ErrNotFound) {
			return zero, aiendpoint.ErrNotFound
		}
		if err != nil {
			return zero, err
		}
		value, err := decode(rec)
		if err != nil {
			return zero, err
		}
		if err := change(value); err != nil {
			return zero, err
		}
		data, err := encode(value)
		if err != nil {
			return zero, err
		}
		err = col.CompareAndSwap(ctx, id, rec.Data, data)
		switch {
		case err == nil:
			return value, nil
		case errors.Is(err, persis.ErrNotFound):
			return zero, aiendpoint.ErrNotFound
		case !errors.Is(err, persis.ErrConflict):
			return zero, err
		}
	}
	return zero, fmt.Errorf("record %q: other processes changed it during %d updates", id, maxRecordUpdates)
}

func (s *AIEndpointStore) Delete(ctx context.Context, scope aiendpoint.Scope, name string) error {
	id, err := aiEndpointRecordID(scope, name)
	if err != nil {
		return err
	}
	// Collection.Delete gives no error for a missing record, so the check comes first.
	if _, err := s.col.Get(ctx, id); err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return aiendpoint.ErrNotFound
		}
		return err
	}
	return s.col.Delete(ctx, id)
}

func decodeAIEndpoint(rec *persis.Record) (*aiendpoint.Endpoint, error) {
	var stored aiEndpointStoredRecord
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("AI endpoint store: decode %q: %w", rec.ID, err)
	}
	if stored.Endpoint == nil {
		return nil, fmt.Errorf("AI endpoint store: decode %q: missing endpoint payload", rec.ID)
	}
	return stored.Endpoint.Clone(), nil
}
