// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package doc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/core/docs"
)

const (
	docAttachmentsFileName = "attachments.json"
	docAttachmentsDirName  = "attachments"
)

// docAttachmentEntry is one manifest row; File names the blob under the
// attachments directory.
type docAttachmentEntry struct {
	File    string    `json:"file"`
	SavedAt time.Time `json:"savedAt"`
	Size    int64     `json:"size"`
}

// docAttachmentsManifest maps doc IDs to attachments by name.
type docAttachmentsManifest map[string]map[string]docAttachmentEntry

func (s *Store) attachmentsManifestPath() string {
	return filepath.Join(s.dataDir, docAttachmentsFileName)
}

func (s *Store) attachmentBlobPath(file string) string {
	return filepath.Join(s.dataDir, docAttachmentsDirName, file)
}

func (s *Store) loadAttachmentsManifest() (docAttachmentsManifest, error) {
	manifest := docAttachmentsManifest{}
	data, err := os.ReadFile(s.attachmentsManifestPath()) //nolint:gosec // path is rooted in the store data dir.
	if os.IsNotExist(err) {
		return manifest, nil
	}
	if err != nil {
		return manifest, fmt.Errorf("filedoc: failed to read attachments manifest: %w", err)
	}
	if len(data) == 0 {
		return manifest, nil
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, fmt.Errorf("filedoc: failed to parse attachments manifest: %w", err)
	}
	return manifest, nil
}

func (s *Store) saveAttachmentsManifest(manifest docAttachmentsManifest) error {
	path := s.attachmentsManifestPath()
	if len(manifest) == 0 {
		if err := fileutil.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("filedoc: failed to remove attachments manifest: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), docDirPermissions); err != nil {
		return fmt.Errorf("filedoc: failed to create attachments directory: %w", err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("filedoc: failed to encode attachments manifest: %w", err)
	}
	data = append(data, '\n')
	if err := fileutil.WriteFileAtomic(path, data, filePermissions); err != nil {
		return fmt.Errorf("filedoc: failed to write attachments manifest: %w", err)
	}
	return nil
}

func newAttachmentBlobName(name string) string {
	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix)
	return hex.EncodeToString(suffix) + strings.ToLower(filepath.Ext(name))
}

// PutAttachment stores an attachment for an existing document, replacing any
// attachment with the same name.
func (s *Store) PutAttachment(_ context.Context, id, name string, content io.Reader) (*docs.DocAttachment, error) {
	if err := docs.ValidateDocID(id); err != nil {
		return nil, err
	}
	if err := docs.ValidateAttachmentName(name); err != nil {
		return nil, err
	}
	if s.dataDir == "" {
		return nil, docs.ErrDocAttachmentNotFound
	}

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	filePath, err := s.docFilePath(id)
	if err != nil {
		return nil, err
	}
	if _, err := statRegularDocFile(filePath); err != nil {
		if os.IsNotExist(err) || errors.Is(err, docs.ErrDocNotFound) {
			return nil, docs.ErrDocNotFound
		}
		return nil, fmt.Errorf("filedoc: failed to stat file %s: %w", filePath, err)
	}

	blobFile := newAttachmentBlobName(name)
	blobPath := s.attachmentBlobPath(blobFile)
	if err := os.MkdirAll(filepath.Dir(blobPath), docDirPermissions); err != nil {
		return nil, fmt.Errorf("filedoc: failed to create attachments directory: %w", err)
	}
	out, err := os.OpenFile(blobPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePermissions) //nolint:gosec // blob name is generated internally.
	if err != nil {
		return nil, fmt.Errorf("filedoc: failed to create attachment blob: %w", err)
	}
	size, err := io.Copy(out, content)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = fileutil.Remove(blobPath)
		return nil, fmt.Errorf("filedoc: failed to write attachment blob: %w", err)
	}

	manifest, err := s.loadAttachmentsManifest()
	if err != nil {
		_ = fileutil.Remove(blobPath)
		return nil, err
	}
	if manifest[id] == nil {
		manifest[id] = map[string]docAttachmentEntry{}
	}
	if prior, ok := manifest[id][name]; ok {
		if err := fileutil.Remove(s.attachmentBlobPath(prior.File)); err != nil && !os.IsNotExist(err) {
			_ = fileutil.Remove(blobPath)
			return nil, fmt.Errorf("filedoc: failed to replace attachment blob: %w", err)
		}
	}
	attachment := docs.DocAttachment{
		Name:    name,
		Size:    size,
		SavedAt: time.Now().UTC(),
	}
	manifest[id][name] = docAttachmentEntry{
		File:    blobFile,
		SavedAt: attachment.SavedAt,
		Size:    size,
	}
	if err := s.saveAttachmentsManifest(manifest); err != nil {
		_ = fileutil.Remove(blobPath)
		return nil, err
	}
	return &attachment, nil
}

// OpenAttachment opens an attachment for reading.
func (s *Store) OpenAttachment(_ context.Context, id, name string) (io.ReadCloser, *docs.DocAttachment, error) {
	if err := docs.ValidateDocID(id); err != nil {
		return nil, nil, err
	}
	if err := docs.ValidateAttachmentName(name); err != nil {
		return nil, nil, err
	}
	if s.dataDir == "" {
		return nil, nil, docs.ErrDocAttachmentNotFound
	}
	manifest, err := s.loadAttachmentsManifest()
	if err != nil {
		return nil, nil, err
	}
	entry, ok := manifest[id][name]
	if !ok {
		return nil, nil, docs.ErrDocAttachmentNotFound
	}
	file, err := os.Open(s.attachmentBlobPath(entry.File)) //nolint:gosec // blob name comes from the manifest.
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, docs.ErrDocAttachmentNotFound
		}
		return nil, nil, fmt.Errorf("filedoc: failed to open attachment blob: %w", err)
	}
	return file, &docs.DocAttachment{
		Name:    name,
		Size:    entry.Size,
		SavedAt: entry.SavedAt,
	}, nil
}

// deleteAttachments removes all attachments for a document ID.
func (s *Store) deleteAttachments(id string) error {
	if s.dataDir == "" {
		return nil
	}
	manifest, err := s.loadAttachmentsManifest()
	if err != nil {
		return err
	}
	entries, ok := manifest[id]
	if !ok {
		return nil
	}
	for _, entry := range entries {
		if err := fileutil.Remove(s.attachmentBlobPath(entry.File)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("filedoc: failed to remove attachment blob: %w", err)
		}
	}
	delete(manifest, id)
	return s.saveAttachmentsManifest(manifest)
}

// deleteAttachmentsPrefix removes attachments for a directory subtree.
func (s *Store) deleteAttachmentsPrefix(id string) error {
	if s.dataDir == "" {
		return nil
	}
	manifest, err := s.loadAttachmentsManifest()
	if err != nil {
		return err
	}
	prefix := id + "/"
	changed := false
	for key, entries := range manifest {
		if key != id && !strings.HasPrefix(key, prefix) {
			continue
		}
		for _, entry := range entries {
			if err := fileutil.Remove(s.attachmentBlobPath(entry.File)); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("filedoc: failed to remove attachment blob: %w", err)
			}
		}
		delete(manifest, key)
		changed = true
	}
	if !changed {
		return nil
	}
	return s.saveAttachmentsManifest(manifest)
}

// renameAttachments carries attachments from one document ID to another.
func (s *Store) renameAttachments(oldID, newID string) error {
	if s.dataDir == "" {
		return nil
	}
	manifest, err := s.loadAttachmentsManifest()
	if err != nil {
		return err
	}
	entries, ok := manifest[oldID]
	if !ok {
		return nil
	}
	manifest[newID] = entries
	delete(manifest, oldID)
	return s.saveAttachmentsManifest(manifest)
}

// renameAttachmentsPrefix carries attachments for a directory subtree.
func (s *Store) renameAttachmentsPrefix(oldID, newID string) error {
	if s.dataDir == "" {
		return nil
	}
	manifest, err := s.loadAttachmentsManifest()
	if err != nil {
		return err
	}
	prefix := oldID + "/"
	renamed := docAttachmentsManifest{}
	for key, entries := range manifest {
		switch {
		case key == oldID:
			renamed[newID] = entries
			delete(manifest, key)
		case strings.HasPrefix(key, prefix):
			renamed[newID+"/"+strings.TrimPrefix(key, prefix)] = entries
			delete(manifest, key)
		}
	}
	if len(renamed) == 0 {
		return nil
	}
	maps.Copy(manifest, renamed)
	return s.saveAttachmentsManifest(manifest)
}
