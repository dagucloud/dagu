// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package doc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/core/docs"
)

// docAttachmentsDirName is the reserved directory under the docs base dir
// holding attachments as {baseDir}/.attachments/{docID}/{name}. The leading
// dot keeps the whole subtree invisible to the doc index (ValidateDocID
// rejects leading-dot segments), and the deterministic layout is what allows
// external tools such as git sync to carry attachments alongside documents.
const docAttachmentsDirName = ".attachments"

// attachmentDocDir returns the attachment directory for a doc ID with
// path-traversal validation.
func (s *Store) attachmentDocDir(docID string) (string, error) {
	return s.safePath(filepath.Join(s.baseDir, docAttachmentsDirName, filepath.FromSlash(docID)), docID)
}

// attachmentFilePath returns the path of one attachment with path-traversal
// validation. Name must be pre-validated as a single segment.
func (s *Store) attachmentFilePath(docID, name string) (string, error) {
	return s.safePath(filepath.Join(s.baseDir, docAttachmentsDirName, filepath.FromSlash(docID), name), docID)
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

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	docFile, err := s.docFilePath(id)
	if err != nil {
		return nil, err
	}
	if _, err := statRegularDocFile(docFile); err != nil {
		if os.IsNotExist(err) || errors.Is(err, docs.ErrDocNotFound) {
			return nil, docs.ErrDocNotFound
		}
		return nil, fmt.Errorf("filedoc: failed to stat file %s: %w", docFile, err)
	}

	filePath, err := s.attachmentFilePath(id, name)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(content)
	if err != nil {
		return nil, fmt.Errorf("filedoc: failed to read attachment content: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(filePath), docDirPermissions); err != nil {
		return nil, fmt.Errorf("filedoc: failed to create attachments directory: %w", err)
	}
	if err := fileutil.WriteFileAtomic(filePath, data, filePermissions); err != nil {
		return nil, fmt.Errorf("filedoc: failed to write attachment: %w", err)
	}

	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, fmt.Errorf("filedoc: failed to stat attachment: %w", err)
	}
	return &docs.DocAttachment{
		Name:    name,
		Size:    info.Size(),
		SavedAt: info.ModTime().UTC(),
	}, nil
}

// OpenAttachment opens an attachment for reading.
func (s *Store) OpenAttachment(_ context.Context, id, name string) (io.ReadCloser, *docs.DocAttachment, error) {
	if err := docs.ValidateDocID(id); err != nil {
		return nil, nil, err
	}
	if err := docs.ValidateAttachmentName(name); err != nil {
		return nil, nil, err
	}
	filePath, err := s.attachmentFilePath(id, name)
	if err != nil {
		return nil, nil, err
	}
	info, err := statRegularDocFile(filePath)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, docs.ErrDocNotFound) {
			return nil, nil, docs.ErrDocAttachmentNotFound
		}
		return nil, nil, fmt.Errorf("filedoc: failed to stat attachment %s: %w", filePath, err)
	}
	file, err := os.Open(filePath) //nolint:gosec // filePath is validated by attachmentFilePath.
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, docs.ErrDocAttachmentNotFound
		}
		return nil, nil, fmt.Errorf("filedoc: failed to open attachment: %w", err)
	}
	return file, &docs.DocAttachment{
		Name:    name,
		Size:    info.Size(),
		SavedAt: info.ModTime().UTC(),
	}, nil
}

// deleteAttachmentsSubtree removes the attachment directory for a doc ID or
// a directory prefix.
func (s *Store) deleteAttachmentsSubtree(id string) error {
	dir, err := s.attachmentDocDir(id)
	if err != nil {
		return err
	}
	exists, err := pathExistsNoFollow(dir)
	if err != nil || !exists {
		return err
	}
	if err := s.safeDeleteDir(dir); err != nil {
		return fmt.Errorf("filedoc: failed to delete attachments: %w", err)
	}
	s.cleanEmptyParents(filepath.Dir(dir))
	return nil
}

// deleteAttachments removes all attachments for a document ID.
func (s *Store) deleteAttachments(id string) error {
	return s.deleteAttachmentsSubtree(id)
}

// deleteAttachmentsPrefix removes attachments for a directory subtree.
func (s *Store) deleteAttachmentsPrefix(id string) error {
	return s.deleteAttachmentsSubtree(id)
}

// renameAttachmentsSubtree moves the attachment directory from one ID to
// another, merging into the target when it already exists.
func (s *Store) renameAttachmentsSubtree(oldID, newID string) error {
	oldDir, err := s.attachmentDocDir(oldID)
	if err != nil {
		return err
	}
	exists, err := pathExistsNoFollow(oldDir)
	if err != nil || !exists {
		return err
	}
	newDir, err := s.attachmentDocDir(newID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(newDir), docDirPermissions); err != nil {
		return fmt.Errorf("filedoc: failed to create attachments directory: %w", err)
	}
	if err := renameNoReplace(oldDir, newDir); err == nil {
		s.cleanEmptyParents(filepath.Dir(oldDir))
		return nil
	} else if !errors.Is(err, docs.ErrDocAlreadyExists) {
		return fmt.Errorf("filedoc: failed to move attachments: %w", err)
	}

	// Target exists (stale leftovers): move entries individually, letting the
	// renamed doc's files win.
	entries, err := os.ReadDir(oldDir)
	if err != nil {
		return fmt.Errorf("filedoc: failed to read attachments directory: %w", err)
	}
	for _, entry := range entries {
		oldPath := filepath.Join(oldDir, entry.Name())
		newPath := filepath.Join(newDir, entry.Name())
		if err := fileutil.Remove(newPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("filedoc: failed to replace attachment: %w", err)
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			return fmt.Errorf("filedoc: failed to move attachment: %w", err)
		}
	}
	if err := fileutil.Remove(oldDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("filedoc: failed to remove attachments directory: %w", err)
	}
	s.cleanEmptyParents(filepath.Dir(oldDir))
	return nil
}

// renameAttachments carries attachments from one document ID to another.
func (s *Store) renameAttachments(oldID, newID string) error {
	return s.renameAttachmentsSubtree(oldID, newID)
}

// renameAttachmentsPrefix carries attachments for a directory subtree.
func (s *Store) renameAttachmentsPrefix(oldID, newID string) error {
	return s.renameAttachmentsSubtree(oldID, newID)
}
