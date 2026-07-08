// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package doc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
)

const docMetadataFileName = ".dagu-docs-meta.json"

type docStoreMetadata struct {
	CreatedAt map[string]string `json:"createdAt,omitempty"`
}

func (s *Store) metadataPath() string {
	return filepath.Join(s.baseDir, docMetadataFileName)
}

func (s *Store) loadMetadata() (docStoreMetadata, error) {
	metadata := docStoreMetadata{CreatedAt: map[string]string{}}
	data, err := os.ReadFile(s.metadataPath()) //nolint:gosec // metadataPath is rooted in the store base dir.
	if os.IsNotExist(err) {
		return metadata, nil
	}
	if err != nil {
		return metadata, fmt.Errorf("filedoc: failed to read metadata: %w", err)
	}
	if len(data) == 0 {
		return metadata, nil
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return metadata, fmt.Errorf("filedoc: failed to parse metadata: %w", err)
	}
	if metadata.CreatedAt == nil {
		metadata.CreatedAt = map[string]string{}
	}
	return metadata, nil
}

func (s *Store) saveMetadata(metadata docStoreMetadata) error {
	path := s.metadataPath()
	if len(metadata.CreatedAt) == 0 {
		if err := fileutil.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("filedoc: failed to remove metadata: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), docDirPermissions); err != nil {
		return fmt.Errorf("filedoc: failed to create metadata directory: %w", err)
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("filedoc: failed to encode metadata: %w", err)
	}
	data = append(data, '\n')
	if err := fileutil.WriteFileAtomic(path, data, filePermissions); err != nil {
		return fmt.Errorf("filedoc: failed to write metadata: %w", err)
	}
	return nil
}

func createdAtFromFileInfo(path string, info os.FileInfo) string {
	return fileCreationTime(path, info).UTC().Format(time.RFC3339)
}

func createdAtNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func (s *Store) docCreatedAt(id, path string, info os.FileInfo) string {
	metadata, err := s.loadMetadata()
	if err == nil {
		if createdAt := metadata.CreatedAt[id]; createdAt != "" {
			return createdAt
		}
	}
	return createdAtFromFileInfo(path, info)
}

func (s *Store) setDocCreatedAt(id, createdAt string) error {
	metadata, err := s.loadMetadata()
	if err != nil {
		return err
	}
	metadata.CreatedAt[id] = createdAt
	return s.saveMetadata(metadata)
}

func (s *Store) deleteDocCreatedAt(id string) error {
	metadata, err := s.loadMetadata()
	if err != nil {
		return err
	}
	delete(metadata.CreatedAt, id)
	return s.saveMetadata(metadata)
}

func (s *Store) deleteDocCreatedAtPrefix(id string) error {
	metadata, err := s.loadMetadata()
	if err != nil {
		return err
	}
	prefix := id + "/"
	for key := range metadata.CreatedAt {
		if key == id || strings.HasPrefix(key, prefix) {
			delete(metadata.CreatedAt, key)
		}
	}
	return s.saveMetadata(metadata)
}

func (s *Store) renameDocCreatedAt(oldID, newID string) error {
	metadata, err := s.loadMetadata()
	if err != nil {
		return err
	}
	if createdAt := metadata.CreatedAt[oldID]; createdAt != "" {
		metadata.CreatedAt[newID] = createdAt
		delete(metadata.CreatedAt, oldID)
	}
	return s.saveMetadata(metadata)
}

func (s *Store) renameDocCreatedAtPrefix(oldID, newID string) error {
	metadata, err := s.loadMetadata()
	if err != nil {
		return err
	}
	prefix := oldID + "/"
	for key, createdAt := range metadata.CreatedAt {
		switch {
		case key == oldID:
			metadata.CreatedAt[newID] = createdAt
			delete(metadata.CreatedAt, key)
		case strings.HasPrefix(key, prefix):
			metadata.CreatedAt[newID+"/"+strings.TrimPrefix(key, prefix)] = createdAt
			delete(metadata.CreatedAt, key)
		}
	}
	return s.saveMetadata(metadata)
}
