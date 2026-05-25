// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package fileremotenode provides a file-based implementation of the remote node Store interface.
package fileremotenode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/remotenode"
)

const (
	fileExtension   = ".json"
	dirPermissions  = 0750
	filePermissions = 0600
)

// Store implements remotenode.Store using the local filesystem.
// Remote nodes are stored as individual JSON files with encrypted credentials.
// Thread-safe through internal locking.
type Store struct {
	baseDir   string
	encryptor *crypto.Encryptor

	// mu protects the index maps
	mu sync.RWMutex
	// byID maps node ID to file path
	byID map[string]string
	// byName maps node name to node ID
	byName map[string]string
}

var _ remotenode.Store = (*Store)(nil)

// New creates a file-backed Store that persists remote nodes as individual JSON files in baseDir.
func New(baseDir string, enc *crypto.Encryptor) (*Store, error) {
	if baseDir == "" {
		return nil, errors.New("fileremotenode: baseDir cannot be empty")
	}
	if enc == nil {
		return nil, errors.New("fileremotenode: encryptor cannot be nil")
	}

	store := &Store{
		baseDir:   baseDir,
		encryptor: enc,
		byID:      make(map[string]string),
		byName:    make(map[string]string),
	}

	if err := os.MkdirAll(baseDir, dirPermissions); err != nil {
		return nil, fmt.Errorf("fileremotenode: failed to create directory %s: %w", baseDir, err)
	}

	if err := store.rebuildIndex(); err != nil {
		return nil, fmt.Errorf("fileremotenode: failed to build index: %w", err)
	}

	return store, nil
}

// rebuildIndex scans the directory and rebuilds the in-memory index.
func (s *Store) rebuildIndex() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.byID = make(map[string]string)
	s.byName = make(map[string]string)

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return fmt.Errorf("failed to read directory %s: %w", s.baseDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != fileExtension {
			continue
		}

		filePath := filepath.Join(s.baseDir, entry.Name())
		stored, err := s.loadStoredFromFile(filePath)
		if err != nil {
			slog.Warn("Failed to load remote node file during index rebuild",
				slog.String("file", filePath),
				slog.String("error", err.Error()))
			continue
		}

		s.byID[stored.ID] = filePath
		s.byName[stored.Name] = stored.ID
	}

	return nil
}

// loadStoredFromFile reads and parses a RemoteNodeForStorage from a JSON file.
func (s *Store) loadStoredFromFile(filePath string) (*remotenode.RemoteNodeForStorage, error) {
	data, err := fileutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	var stored remotenode.RemoteNodeForStorage
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("failed to parse remote node file %s: %w", filePath, err)
	}

	return &stored, nil
}

// loadNodeFromFile reads a file and returns a decrypted RemoteNode.
func (s *Store) loadNodeFromFile(filePath string) (*remotenode.RemoteNode, error) {
	stored, err := s.loadStoredFromFile(filePath)
	if err != nil {
		return nil, err
	}

	node := stored.ToRemoteNode()

	// Decrypt credentials — return errors rather than silently serving
	// nodes with missing credentials.
	if stored.BasicAuthPasswordEnc != "" {
		pwd, err := s.encryptor.Decrypt(stored.BasicAuthPasswordEnc)
		if err != nil {
			return nil, fmt.Errorf("fileremotenode: failed to decrypt basic auth password for node %s: %w", stored.ID, err)
		}
		node.BasicAuthPassword = pwd
	}

	if stored.AuthTokenEnc != "" {
		token, err := s.encryptor.Decrypt(stored.AuthTokenEnc)
		if err != nil {
			return nil, fmt.Errorf("fileremotenode: failed to decrypt auth token for node %s: %w", stored.ID, err)
		}
		node.AuthToken = token
	}

	return node, nil
}

// nodeFilePath returns the file path for a node ID.
func (s *Store) nodeFilePath(id string) string {
	return filepath.Join(s.baseDir, id+fileExtension)
}

// writeNodeToFile writes a remote node to a JSON file atomically with encrypted credentials.
func (s *Store) writeNodeToFile(filePath string, node *remotenode.RemoteNode) error {
	stored := node.ToStorage()

	// Encrypt credentials
	if node.BasicAuthPassword != "" {
		enc, err := s.encryptor.Encrypt(node.BasicAuthPassword)
		if err != nil {
			return fmt.Errorf("fileremotenode: failed to encrypt password: %w", err)
		}
		stored.BasicAuthPasswordEnc = enc
	}

	if node.AuthToken != "" {
		enc, err := s.encryptor.Encrypt(node.AuthToken)
		if err != nil {
			return fmt.Errorf("fileremotenode: failed to encrypt token: %w", err)
		}
		stored.AuthTokenEnc = enc
	}

	if err := fileutil.WriteJSONAtomic(filePath, stored, filePermissions); err != nil {
		return fmt.Errorf("fileremotenode: %w", err)
	}
	return nil
}

// Create stores a new remote node.
func (s *Store) Create(_ context.Context, node *remotenode.RemoteNode) error {
	if node == nil {
		return errors.New("fileremotenode: node cannot be nil")
	}
	if node.ID == "" {
		return remotenode.ErrInvalidRemoteNodeID
	}
	if node.Name == "" {
		return remotenode.ErrInvalidRemoteNodeName
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byName[node.Name]; exists {
		return remotenode.ErrRemoteNodeAlreadyExists
	}
	if _, exists := s.byID[node.ID]; exists {
		return remotenode.ErrRemoteNodeAlreadyExists
	}

	filePath := s.nodeFilePath(node.ID)
	if err := s.writeNodeToFile(filePath, node); err != nil {
		return err
	}

	s.byID[node.ID] = filePath
	s.byName[node.Name] = node.ID

	return nil
}

// GetByID retrieves a remote node by its unique ID.
func (s *Store) GetByID(_ context.Context, id string) (*remotenode.RemoteNode, error) {
	if id == "" {
		return nil, remotenode.ErrInvalidRemoteNodeID
	}

	s.mu.RLock()
	filePath, exists := s.byID[id]
	s.mu.RUnlock()

	if !exists {
		return nil, remotenode.ErrRemoteNodeNotFound
	}

	node, err := s.loadNodeFromFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, remotenode.ErrRemoteNodeNotFound
		}
		return nil, fmt.Errorf("fileremotenode: failed to load node %s: %w", id, err)
	}

	return node, nil
}

// GetByName retrieves a remote node by its name.
func (s *Store) GetByName(ctx context.Context, name string) (*remotenode.RemoteNode, error) {
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

// List returns all remote nodes in the store.
func (s *Store) List(ctx context.Context) ([]*remotenode.RemoteNode, error) {
	s.mu.RLock()
	ids := make([]string, 0, len(s.byID))
	for id := range s.byID {
		ids = append(ids, id)
	}
	s.mu.RUnlock()

	nodes := make([]*remotenode.RemoteNode, 0, len(ids))
	for _, id := range ids {
		node, err := s.GetByID(ctx, id)
		if err != nil {
			if errors.Is(err, remotenode.ErrRemoteNodeNotFound) {
				continue
			}
			return nil, err
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// Update modifies an existing remote node.
func (s *Store) Update(_ context.Context, node *remotenode.RemoteNode) error {
	if node == nil {
		return errors.New("fileremotenode: node cannot be nil")
	}
	if node.ID == "" {
		return remotenode.ErrInvalidRemoteNodeID
	}
	if node.Name == "" {
		return remotenode.ErrInvalidRemoteNodeName
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	filePath, exists := s.byID[node.ID]
	if !exists {
		return remotenode.ErrRemoteNodeNotFound
	}

	// Load existing node to check for name change
	existingNode, err := s.loadNodeFromFile(filePath)
	if err != nil {
		return fmt.Errorf("fileremotenode: failed to load existing node: %w", err)
	}

	// If name changed, check for conflicts and update index
	if existingNode.Name != node.Name {
		if existingID, taken := s.byName[node.Name]; taken && existingID != node.ID {
			return remotenode.ErrRemoteNodeAlreadyExists
		}
		delete(s.byName, existingNode.Name)
		s.byName[node.Name] = node.ID
	}

	if err := s.writeNodeToFile(filePath, node); err != nil {
		// Rollback name index change on failure
		if existingNode.Name != node.Name {
			delete(s.byName, node.Name)
			s.byName[existingNode.Name] = node.ID
		}
		return err
	}

	return nil
}

// Delete removes a remote node by its ID.
func (s *Store) Delete(_ context.Context, id string) error {
	if id == "" {
		return remotenode.ErrInvalidRemoteNodeID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	filePath, exists := s.byID[id]
	if !exists {
		return remotenode.ErrRemoteNodeNotFound
	}

	// Load node to get name for index cleanup.
	// Tolerate corrupt/missing files so deletion can still proceed.
	stored, err := s.loadStoredFromFile(filePath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("Failed to load node metadata for deletion, proceeding anyway",
				slog.String("id", id),
				slog.String("error", err.Error()))
		}
		stored = nil
	}

	if err := fileutil.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("fileremotenode: failed to delete node file: %w", err)
	}

	delete(s.byID, id)
	if stored != nil {
		delete(s.byName, stored.Name)
	} else {
		for name, nodeID := range s.byName {
			if nodeID == id {
				delete(s.byName, name)
				break
			}
		}
	}

	return nil
}
