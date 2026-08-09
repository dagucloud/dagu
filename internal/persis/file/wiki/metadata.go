// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wiki

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	wikiMetadataFileName       = ".dagu-wiki-meta.json"
	legacyDocsMetadataFileName = ".dagu-docs-meta.json"
)

type wikiStoreMetadata struct {
	CreatedAt map[string]string `json:"createdAt,omitempty"`
}

func (s *Store) metadataPath() string {
	return filepath.Join(s.baseDir, s.metadataFileName)
}

func resolveMetadataFileName(baseDir string, preferLegacy bool) (string, error) {
	wikiPath := filepath.Join(baseDir, wikiMetadataFileName)
	legacyDocsPath := filepath.Join(baseDir, legacyDocsMetadataFileName)
	wikiExists, err := metadataFileExists(wikiPath)
	if err != nil {
		return "", err
	}
	legacyDocsExists, err := metadataFileExists(legacyDocsPath)
	if err != nil {
		return "", err
	}
	if wikiExists && legacyDocsExists {
		return "", fmt.Errorf("filewiki: both %s and %s exist; reconcile them before starting Dagu", wikiPath, legacyDocsPath)
	}
	if legacyDocsExists || (!wikiExists && preferLegacy) {
		return legacyDocsMetadataFileName, nil
	}
	return wikiMetadataFileName, nil
}

func metadataFileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("filewiki: inspect metadata %s: %w", path, err)
}

func (s *Store) loadMetadata() (wikiStoreMetadata, error) {
	metadata := wikiStoreMetadata{CreatedAt: map[string]string{}}
	data, err := os.ReadFile(s.metadataPath()) //nolint:gosec // metadataPath is rooted in the store base dir.
	if os.IsNotExist(err) {
		return metadata, nil
	}
	if err != nil {
		return metadata, fmt.Errorf("filewiki: failed to read metadata: %w", err)
	}
	if len(data) == 0 {
		return metadata, nil
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return metadata, fmt.Errorf("filewiki: failed to parse metadata: %w", err)
	}
	if metadata.CreatedAt == nil {
		metadata.CreatedAt = map[string]string{}
	}
	return metadata, nil
}

func (s *Store) saveMetadata(metadata wikiStoreMetadata) error {
	path := s.metadataPath()
	if len(metadata.CreatedAt) == 0 {
		if err := fileutil.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("filewiki: failed to remove metadata: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), wikiDirPermissions); err != nil {
		return fmt.Errorf("filewiki: failed to create metadata directory: %w", err)
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("filewiki: failed to encode metadata: %w", err)
	}
	data = append(data, '\n')
	if err := fileutil.WriteFileAtomic(path, data, filePermissions); err != nil {
		return fmt.Errorf("filewiki: failed to write metadata: %w", err)
	}
	return nil
}

func createdAtFromFileInfo(path string, info os.FileInfo) string {
	return fileCreationTime(path, info).UTC().Format(time.RFC3339)
}

func createdAtNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func (s *Store) pageCreatedAt(id, path string, info os.FileInfo) string {
	metadata, err := s.loadMetadata()
	if err == nil {
		if createdAt := metadata.CreatedAt[id]; createdAt != "" {
			return createdAt
		}
	}
	return createdAtFromFileInfo(path, info)
}

func (s *Store) setPageCreatedAt(id, createdAt string) error {
	metadata, err := s.loadMetadata()
	if err != nil {
		return err
	}
	metadata.CreatedAt[id] = createdAt
	return s.saveMetadata(metadata)
}

func (s *Store) deletePageCreatedAt(id string) error {
	metadata, err := s.loadMetadata()
	if err != nil {
		return err
	}
	delete(metadata.CreatedAt, id)
	return s.saveMetadata(metadata)
}

func (s *Store) deletePageCreatedAtPrefix(id string) error {
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

func (s *Store) renamePageCreatedAt(oldID, newID string) error {
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

func (s *Store) renamePageCreatedAtPrefix(oldID, newID string) error {
	metadata, err := s.loadMetadata()
	if err != nil {
		return err
	}
	prefix := oldID + "/"
	renamed := make(map[string]string)
	for key, createdAt := range metadata.CreatedAt {
		switch {
		case key == oldID:
			renamed[newID] = createdAt
			delete(metadata.CreatedAt, key)
		case strings.HasPrefix(key, prefix):
			renamed[newID+"/"+strings.TrimPrefix(key, prefix)] = createdAt
			delete(metadata.CreatedAt, key)
		}
	}
	maps.Copy(metadata.CreatedAt, renamed)
	return s.saveMetadata(metadata)
}
