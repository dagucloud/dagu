// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/remotenode"
)

var _ remotenode.Store = (*RemoteNodeStore)(nil)

// RemoteNodeStore implements [remotenode.Store] over [persis.Collection].
// Credential fields are encrypted before Put and decrypted after Get.
type RemoteNodeStore struct {
	col       persis.Collection
	encryptor *crypto.Encryptor

	mu     sync.RWMutex
	byName map[string]string
}

// NewRemoteNodeStore creates a RemoteNodeStore backed by col.
func NewRemoteNodeStore(col persis.Collection, enc *crypto.Encryptor) (*RemoteNodeStore, error) {
	if enc == nil {
		return nil, errors.New("remote-node store: encryptor cannot be nil")
	}
	s := &RemoteNodeStore{
		col:       col,
		encryptor: enc,
		byName:    make(map[string]string),
	}
	if err := s.rebuildIndex(context.Background()); err != nil {
		return nil, fmt.Errorf("remote-node store: build index: %w", err)
	}
	return s, nil
}

func (s *RemoteNodeStore) rebuildIndex(ctx context.Context) error {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range recs {
		var stored remotenode.RemoteNodeForStorage
		if err := persis.Decode(rec, &stored); err != nil {
			continue
		}
		s.byName[stored.Name] = stored.ID
	}
	return nil
}

// Create stores a new remote node, encrypting credentials.
func (s *RemoteNodeStore) Create(ctx context.Context, node *remotenode.RemoteNode) error {
	if node == nil {
		return errors.New("remote-node store: node cannot be nil")
	}
	if node.ID == "" {
		return remotenode.ErrInvalidRemoteNodeID
	}
	if node.Name == "" {
		return remotenode.ErrInvalidRemoteNodeName
	}

	stored, err := s.encryptForStorage(node)
	if err != nil {
		return err
	}
	data, err := persis.Encode(stored)
	if err != nil {
		return fmt.Errorf("remote-node store: encode: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byName[node.Name]; exists {
		return remotenode.ErrRemoteNodeAlreadyExists
	}
	if _, err := s.col.Get(ctx, node.ID); err == nil {
		return remotenode.ErrRemoteNodeAlreadyExists
	}
	if err := s.col.Put(ctx, &persis.Record{ID: node.ID, Data: data}); err != nil {
		return fmt.Errorf("remote-node store: create: %w", err)
	}
	s.byName[node.Name] = node.ID
	return nil
}

// GetByID retrieves a remote node by its ID and decrypts credentials.
func (s *RemoteNodeStore) GetByID(ctx context.Context, id string) (*remotenode.RemoteNode, error) {
	if id == "" {
		return nil, remotenode.ErrInvalidRemoteNodeID
	}
	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, remotenode.ErrRemoteNodeNotFound
		}
		return nil, fmt.Errorf("remote-node store: get: %w", err)
	}
	return s.decryptFromRecord(rec)
}

// GetByName retrieves a remote node by its name.
func (s *RemoteNodeStore) GetByName(ctx context.Context, name string) (*remotenode.RemoteNode, error) {
	if name == "" {
		return nil, remotenode.ErrInvalidRemoteNodeName
	}
	s.mu.RLock()
	nodeID, exists := s.byName[name]
	s.mu.RUnlock()
	if !exists {
		return nil, remotenode.ErrRemoteNodeNotFound
	}
	return s.GetByID(ctx, nodeID)
}

// List returns all remote nodes (with decrypted credentials).
func (s *RemoteNodeStore) List(ctx context.Context) ([]*remotenode.RemoteNode, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	out := make([]*remotenode.RemoteNode, 0, len(recs))
	for _, rec := range recs {
		node, err := s.decryptFromRecord(rec)
		if err != nil {
			return nil, err
		}
		out = append(out, node)
	}
	return out, nil
}

// Update modifies an existing remote node.
func (s *RemoteNodeStore) Update(ctx context.Context, node *remotenode.RemoteNode) error {
	if node == nil {
		return errors.New("remote-node store: node cannot be nil")
	}
	if node.ID == "" {
		return remotenode.ErrInvalidRemoteNodeID
	}
	if node.Name == "" {
		return remotenode.ErrInvalidRemoteNodeName
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existingRec, err := s.col.Get(ctx, node.ID)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return remotenode.ErrRemoteNodeNotFound
		}
		return fmt.Errorf("remote-node store: get for update: %w", err)
	}
	var existing remotenode.RemoteNodeForStorage
	if err := persis.Decode(existingRec, &existing); err != nil {
		return fmt.Errorf("remote-node store: decode existing: %w", err)
	}
	if existing.Name != node.Name {
		if id, taken := s.byName[node.Name]; taken && id != node.ID {
			return remotenode.ErrRemoteNodeAlreadyExists
		}
	}

	stored, err := s.encryptForStorage(node)
	if err != nil {
		return err
	}
	data, err := persis.Encode(stored)
	if err != nil {
		return fmt.Errorf("remote-node store: encode: %w", err)
	}
	if err := s.col.Put(ctx, &persis.Record{ID: node.ID, Data: data}); err != nil {
		return fmt.Errorf("remote-node store: update: %w", err)
	}
	if existing.Name != node.Name {
		delete(s.byName, existing.Name)
		s.byName[node.Name] = node.ID
	}
	return nil
}

// Delete removes a remote node by ID.
func (s *RemoteNodeStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return remotenode.ErrInvalidRemoteNodeID
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.col.Get(ctx, id)
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return remotenode.ErrRemoteNodeNotFound
		}
		return fmt.Errorf("remote-node store: get for delete: %w", err)
	}
	var stored remotenode.RemoteNodeForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return fmt.Errorf("remote-node store: decode for delete: %w", err)
	}
	if err := s.col.Delete(ctx, id); err != nil {
		return fmt.Errorf("remote-node store: delete: %w", err)
	}
	delete(s.byName, stored.Name)
	return nil
}

func (s *RemoteNodeStore) encryptForStorage(node *remotenode.RemoteNode) (*remotenode.RemoteNodeForStorage, error) {
	stored := node.ToStorage()
	if node.BasicAuthPassword != "" {
		enc, err := s.encryptor.Encrypt(node.BasicAuthPassword)
		if err != nil {
			return nil, fmt.Errorf("remote-node store: encrypt password: %w", err)
		}
		stored.BasicAuthPasswordEnc = enc
	}
	if node.AuthToken != "" {
		enc, err := s.encryptor.Encrypt(node.AuthToken)
		if err != nil {
			return nil, fmt.Errorf("remote-node store: encrypt token: %w", err)
		}
		stored.AuthTokenEnc = enc
	}
	return stored, nil
}

func (s *RemoteNodeStore) decryptFromRecord(rec *persis.Record) (*remotenode.RemoteNode, error) {
	var stored remotenode.RemoteNodeForStorage
	if err := persis.Decode(rec, &stored); err != nil {
		return nil, fmt.Errorf("remote-node store: decode record %q: %w", rec.ID, err)
	}
	node := stored.ToRemoteNode()
	if stored.BasicAuthPasswordEnc != "" {
		pwd, err := s.encryptor.Decrypt(stored.BasicAuthPasswordEnc)
		if err != nil {
			return nil, fmt.Errorf("remote-node store: decrypt password for %s: %w", stored.ID, err)
		}
		node.BasicAuthPassword = pwd
	}
	if stored.AuthTokenEnc != "" {
		token, err := s.encryptor.Decrypt(stored.AuthTokenEnc)
		if err != nil {
			return nil, fmt.Errorf("remote-node store: decrypt token for %s: %w", stored.ID, err)
		}
		node.AuthToken = token
	}
	return node, nil
}
