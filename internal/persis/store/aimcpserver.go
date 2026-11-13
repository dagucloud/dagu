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

var _ aiendpoint.MCPStore = (*AIMCPServerStore)(nil)

// AIMCPServerStore keeps the metadata of the MCP server entries (Spec 095) in a
// collection. A record ID is "global/<name>" or "workspaces/<workspace>/<name>".
// The record never holds a credential value.
type AIMCPServerStore struct {
	col persis.Collection
}

type aiMCPServerStoredRecord struct {
	Server *aiendpoint.MCPServer `json:"server"`
}

// NewAIMCPServerStore creates an AIMCPServerStore backed by col.
func NewAIMCPServerStore(col persis.Collection) (*AIMCPServerStore, error) {
	if col == nil {
		return nil, errors.New("MCP server entry store: collection cannot be nil")
	}
	return &AIMCPServerStore{col: col}, nil
}

func aiMCPServerRecordID(scope aiendpoint.Scope, name string) (string, error) {
	if err := aiendpoint.ValidateMCPName(name); err != nil {
		return "", err
	}
	if scope.IsGlobal() {
		return "global/" + name, nil
	}
	return "workspaces/" + scope.Workspace + "/" + name, nil
}

func (s *AIMCPServerStore) Create(ctx context.Context, server *aiendpoint.MCPServer) error {
	if server == nil {
		return errors.New("MCP server entry store: server cannot be nil")
	}
	id, err := aiMCPServerRecordID(server.Scope, server.Name)
	if err != nil {
		return err
	}
	data, err := persis.Encode(&aiMCPServerStoredRecord{Server: server.Clone()})
	if err != nil {
		return err
	}
	err = s.col.Create(ctx, &persis.Record{ID: id, Data: data, CreatedAt: server.CreatedAt, UpdatedAt: server.UpdatedAt})
	if errors.Is(err, persis.ErrConflict) {
		return aiendpoint.ErrAlreadyExists
	}
	return err
}

func (s *AIMCPServerStore) Get(ctx context.Context, scope aiendpoint.Scope, name string) (*aiendpoint.MCPServer, error) {
	id, err := aiMCPServerRecordID(scope, name)
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
	return decodeAIMCPServer(rec)
}

func (s *AIMCPServerStore) List(ctx context.Context) ([]*aiendpoint.MCPServer, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	out := make([]*aiendpoint.MCPServer, 0, len(recs))
	for _, rec := range recs {
		server, err := decodeAIMCPServer(rec)
		if err != nil {
			// One bad record must not hide the others from the API and the steps.
			logger.Warn(ctx, "Skipped an MCP server entry that cannot be read", tag.Name(rec.ID), tag.Error(err))
			continue
		}
		out = append(out, server)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope.Workspace < out[j].Scope.Workspace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (s *AIMCPServerStore) Update(ctx context.Context, scope aiendpoint.Scope, name string, change func(*aiendpoint.MCPServer) error) (*aiendpoint.MCPServer, error) {
	id, err := aiMCPServerRecordID(scope, name)
	if err != nil {
		return nil, err
	}
	return swapRecord(ctx, s.col, id, decodeAIMCPServer, func(server *aiendpoint.MCPServer) ([]byte, error) {
		return persis.Encode(&aiMCPServerStoredRecord{Server: server.Clone()})
	}, change)
}

func (s *AIMCPServerStore) Delete(ctx context.Context, scope aiendpoint.Scope, name string) error {
	id, err := aiMCPServerRecordID(scope, name)
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

func decodeAIMCPServer(rec *persis.Record) (*aiendpoint.MCPServer, error) {
	var stored aiMCPServerStoredRecord
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("MCP server entry store: decode %q: %w", rec.ID, err)
	}
	if stored.Server == nil {
		return nil, fmt.Errorf("MCP server entry store: decode %q: missing server payload", rec.ID)
	}
	return stored.Server.Clone(), nil
}
