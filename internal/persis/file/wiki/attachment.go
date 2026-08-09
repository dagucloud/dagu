// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wiki

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	wikimodel "github.com/dagucloud/dagu/v2/internal/wiki"
)

// wikiPageAttachmentsDirName is the reserved directory under the pages base dir
// holding attachments as {baseDir}/.attachments/{pageID}/{name}. The leading
// dot keeps the whole subtree invisible to the page index (ValidatePageID
// rejects leading-dot segments), and the deterministic layout is what allows
// external tools such as git sync to carry attachments alongside pages.
const wikiPageAttachmentsDirName = ".attachments"

// attachmentPageDir returns the attachment directory for a page ID with
// path-traversal validation.
func (s *Store) attachmentPageDir(pageID string) (string, error) {
	return s.safePath(filepath.Join(s.baseDir, wikiPageAttachmentsDirName, filepath.FromSlash(pageID)), pageID)
}

// attachmentFilePath returns the path of one attachment with path-traversal
// validation. Name must be pre-validated as a single segment.
func (s *Store) attachmentFilePath(pageID, name string) (string, error) {
	return s.safePath(filepath.Join(s.baseDir, wikiPageAttachmentsDirName, filepath.FromSlash(pageID), name), pageID)
}

// PutAttachment stores an attachment for an existing page, replacing any
// attachment with the same name.
func (s *Store) PutAttachment(_ context.Context, id, name string, content io.Reader) (*wikimodel.PageAttachment, error) {
	if err := wikimodel.ValidatePageID(id); err != nil {
		return nil, err
	}
	if err := wikimodel.ValidateAttachmentName(name); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(content)
	if err != nil {
		return nil, fmt.Errorf("filewiki: failed to read attachment content: %w", err)
	}

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	pageFile, err := s.wikiPageFilePath(id)
	if err != nil {
		return nil, err
	}
	if _, err := statRegularPageFile(pageFile); err != nil {
		if os.IsNotExist(err) || errors.Is(err, wikimodel.ErrPageNotFound) {
			return nil, wikimodel.ErrPageNotFound
		}
		return nil, fmt.Errorf("filewiki: failed to stat file %s: %w", pageFile, err)
	}

	filePath, err := s.attachmentFilePath(id, name)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(filePath), wikiDirPermissions); err != nil {
		return nil, fmt.Errorf("filewiki: failed to create attachments directory: %w", err)
	}
	if err := fileutil.WriteFileAtomic(filePath, data, filePermissions); err != nil {
		return nil, fmt.Errorf("filewiki: failed to write attachment: %w", err)
	}

	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, fmt.Errorf("filewiki: failed to stat attachment: %w", err)
	}
	return &wikimodel.PageAttachment{
		Name:    name,
		Size:    info.Size(),
		SavedAt: info.ModTime().UTC(),
	}, nil
}

// OpenAttachment opens an attachment for reading.
func (s *Store) OpenAttachment(_ context.Context, id, name string) (io.ReadCloser, *wikimodel.PageAttachment, error) {
	if err := wikimodel.ValidatePageID(id); err != nil {
		return nil, nil, err
	}
	if err := wikimodel.ValidateAttachmentName(name); err != nil {
		return nil, nil, err
	}
	filePath, err := s.attachmentFilePath(id, name)
	if err != nil {
		return nil, nil, err
	}
	info, err := statRegularPageFile(filePath)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, wikimodel.ErrPageNotFound) {
			return nil, nil, wikimodel.ErrPageAttachmentNotFound
		}
		return nil, nil, fmt.Errorf("filewiki: failed to stat attachment %s: %w", filePath, err)
	}
	file, err := os.Open(filePath) //nolint:gosec // filePath is validated by attachmentFilePath.
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, wikimodel.ErrPageAttachmentNotFound
		}
		return nil, nil, fmt.Errorf("filewiki: failed to open attachment: %w", err)
	}
	return file, &wikimodel.PageAttachment{
		Name:    name,
		Size:    info.Size(),
		SavedAt: info.ModTime().UTC(),
	}, nil
}

// deleteAttachmentsSubtree removes the attachment directory for a page ID or
// a directory prefix.
func (s *Store) deleteAttachmentsSubtree(id string) error {
	dir, err := s.attachmentPageDir(id)
	if err != nil {
		return err
	}
	exists, err := pathExistsNoFollow(dir)
	if err != nil || !exists {
		return err
	}
	if err := s.safeDeleteDir(dir); err != nil {
		return fmt.Errorf("filewiki: failed to delete attachments: %w", err)
	}
	s.cleanEmptyParents(filepath.Dir(dir))
	return nil
}

// deleteAttachments removes all attachments for a page ID.
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
	oldDir, err := s.attachmentPageDir(oldID)
	if err != nil {
		return err
	}
	exists, err := pathExistsNoFollow(oldDir)
	if err != nil || !exists {
		return err
	}
	newDir, err := s.attachmentPageDir(newID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(newDir), wikiDirPermissions); err != nil {
		return fmt.Errorf("filewiki: failed to create attachments directory: %w", err)
	}
	if err := renameNoReplace(oldDir, newDir); err == nil {
		s.cleanEmptyParents(filepath.Dir(oldDir))
		return nil
	} else if !errors.Is(err, wikimodel.ErrPageAlreadyExists) {
		return fmt.Errorf("filewiki: failed to move attachments: %w", err)
	}

	// Target exists (stale leftovers): merge recursively, letting the
	// renamed page's files win over stale ones.
	if err := mergeMoveAttachmentDir(oldDir, newDir); err != nil {
		return err
	}
	s.cleanEmptyParents(filepath.Dir(oldDir))
	return nil
}

// mergeMoveAttachmentDir moves every entry of oldDir into newDir, descending
// into subdirectories that already exist at the target, then removes oldDir.
func mergeMoveAttachmentDir(oldDir, newDir string) error {
	if err := os.MkdirAll(newDir, wikiDirPermissions); err != nil {
		return fmt.Errorf("filewiki: failed to create attachments directory: %w", err)
	}
	entries, err := os.ReadDir(oldDir)
	if err != nil {
		return fmt.Errorf("filewiki: failed to read attachments directory: %w", err)
	}
	for _, entry := range entries {
		oldPath := filepath.Join(oldDir, entry.Name())
		newPath := filepath.Join(newDir, entry.Name())
		if entry.IsDir() {
			if err := mergeMoveAttachmentDir(oldPath, newPath); err != nil {
				return err
			}
			continue
		}
		if err := fileutil.Remove(newPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("filewiki: failed to replace attachment: %w", err)
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			return fmt.Errorf("filewiki: failed to move attachment: %w", err)
		}
	}
	if err := fileutil.Remove(oldDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("filewiki: failed to remove attachments directory: %w", err)
	}
	return nil
}

// renameAttachments carries attachments from one page ID to another.
func (s *Store) renameAttachments(oldID, newID string) error {
	return s.renameAttachmentsSubtree(oldID, newID)
}

// renameAttachmentsPrefix carries attachments for a directory subtree.
func (s *Store) renameAttachmentsPrefix(oldID, newID string) error {
	return s.renameAttachmentsSubtree(oldID, newID)
}
