// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wiki

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

// MoveHistory moves the attachments, revisions, and page metadata of the
// pages under root, a top-level directory compared without regard to case,
// to dst. There each is keyed by its page's path under root. It moves them
// whether or not the pages are still under root.
//
// An entry dst already has for a page stays. An attachment whose name dst
// already has for the page with other content stays where it is, and a
// warning names it. Calling it again after an interruption finishes the move.
func (s *Store) MoveHistory(ctx context.Context, root string, dst *Store) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	dst.mutationMu.Lock()
	defer dst.mutationMu.Unlock()

	if err := s.moveRevisions(root, dst); err != nil {
		return err
	}
	if err := s.moveMetadata(root, dst); err != nil {
		return err
	}
	return s.moveAttachments(ctx, root, dst)
}

// underRoot returns the part of a page ID below root, and false when the ID
// is not under root.
func underRoot(id, root string) (string, bool) {
	first, rest, ok := strings.Cut(id, "/")
	return rest, ok && rest != "" && strings.EqualFold(first, root)
}

// moveRevisions copies the revision blobs to dst, then records them in dst's
// manifest before dropping them from this store's, so each revision stays
// recorded in one of the two until both manifests are saved.
func (s *Store) moveRevisions(root string, dst *Store) error {
	if !s.revisionsEnabled() {
		return nil
	}
	manifest, err := s.loadRevisionsManifest()
	if err != nil {
		return err
	}
	moved := pageRevisionsManifest{}
	for id, entries := range manifest {
		if rel, ok := underRoot(id, root); ok {
			moved[rel] = append(moved[rel], entries...)
			delete(manifest, id)
		}
	}
	if len(moved) == 0 {
		return nil
	}
	if !dst.revisionsEnabled() {
		return errors.New("filewiki: the destination keeps no revisions")
	}
	target, err := dst.loadRevisionsManifest()
	if err != nil {
		return err
	}
	var pruned []string
	for rel, entries := range moved {
		merged := target[rel]
		for _, entry := range entries {
			if slices.ContainsFunc(merged, func(e pageRevisionEntry) bool { return e.File == entry.File }) {
				continue
			}
			if err := copyRevisionBlob(s.revisionBlobPath(entry.File), dst.revisionBlobPath(entry.File)); err != nil {
				return err
			}
			merged = append(merged, entry)
		}
		slices.SortStableFunc(merged, func(a, b pageRevisionEntry) int { return b.SavedAt.Compare(a.SavedAt) })
		if len(merged) > pageRevisionLimit {
			for _, entry := range merged[pageRevisionLimit:] {
				pruned = append(pruned, entry.File)
			}
			merged = merged[:pageRevisionLimit]
		}
		target[rel] = merged
	}
	if err := dst.saveRevisionsManifest(target); err != nil {
		return err
	}
	if err := s.saveRevisionsManifest(manifest); err != nil {
		return err
	}
	for _, entries := range moved {
		for _, entry := range entries {
			if err := fileutil.Remove(s.revisionBlobPath(entry.File)); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("filewiki: failed to remove moved revision blob: %w", err)
			}
		}
	}
	for _, file := range pruned {
		if err := fileutil.Remove(dst.revisionBlobPath(file)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("filewiki: failed to prune revision blob: %w", err)
		}
	}
	return nil
}

// copyRevisionBlob copies a revision blob. A blob gone from src is skipped,
// since its revision then reads as missing in either store.
func copyRevisionBlob(src, dst string) error {
	data, err := fileutil.ReadFile(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("filewiki: failed to read revision blob: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), wikiDirPermissions); err != nil {
		return fmt.Errorf("filewiki: failed to create revisions directory: %w", err)
	}
	if err := fileutil.WriteFileAtomic(dst, data, filePermissions); err != nil {
		return fmt.Errorf("filewiki: failed to write revision blob: %w", err)
	}
	return nil
}

func (s *Store) moveMetadata(root string, dst *Store) error {
	metadata, err := s.loadMetadata()
	if err != nil {
		return err
	}
	target, err := dst.loadMetadata()
	if err != nil {
		return err
	}
	moved := false
	for id, createdAt := range metadata.CreatedAt {
		rel, ok := underRoot(id, root)
		if !ok {
			continue
		}
		if _, exists := target.CreatedAt[rel]; !exists {
			target.CreatedAt[rel] = createdAt
		}
		delete(metadata.CreatedAt, id)
		moved = true
	}
	if !moved {
		return nil
	}
	if err := dst.saveMetadata(target); err != nil {
		return err
	}
	return s.saveMetadata(metadata)
}

func (s *Store) moveAttachments(ctx context.Context, root string, dst *Store) error {
	base := filepath.Join(s.baseDir, wikiPageAttachmentsDirName)
	entries, err := fileutil.ReadDir(base)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("filewiki: failed to read attachments directory: %w", err)
	}
	dstBase := filepath.Join(dst.baseDir, wikiPageAttachmentsDirName)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.EqualFold(entry.Name(), root) {
			continue
		}
		if err := s.moveAttachmentTree(ctx, filepath.Join(base, entry.Name()), dstBase, dst); err != nil {
			return err
		}
	}
	s.cleanEmptyParents(base)
	return nil
}

// moveAttachmentTree moves each attachment file under src to the same path
// under dstBase, then removes the directories it emptied.
func (s *Store) moveAttachmentTree(ctx context.Context, src, dstBase string, dst *Store) error {
	sameFS, err := fileutil.SameFileSystem(src, dstBase)
	if err != nil {
		return fmt.Errorf("filewiki: failed to inspect attachments file systems: %w", err)
	}
	var dirs []string
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target, err := dst.safePath(filepath.Join(dstBase, rel), filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		kept, err := moveAttachmentFile(path, target, sameFS)
		if err != nil {
			return err
		}
		if kept {
			logger.Warn(ctx, "Kept a wiki attachment in place, since the project's wiki has another file of that name",
				tag.File(path), tag.Path(target))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("filewiki: failed to move attachments: %w", err)
	}
	// A directory that still holds a kept attachment is not empty and stays.
	for _, dir := range slices.Backward(dirs) {
		_ = fileutil.Remove(dir)
	}
	return nil
}

// moveAttachmentFile moves src to dst, renaming it on one file system and
// copying it to another. It reports true, and leaves src, when dst exists
// with other content.
func moveAttachmentFile(src, dst string, sameFS bool) (bool, error) {
	exists, err := pathExistsNoFollow(dst)
	if err != nil {
		return false, err
	}
	if exists {
		same, err := sameFileContent(src, dst)
		if err != nil || !same {
			return !same, err
		}
		return false, fileutil.Remove(src)
	}
	if err := os.MkdirAll(filepath.Dir(dst), wikiDirPermissions); err != nil {
		return false, err
	}
	if sameFS {
		return false, fileutil.Rename(src, dst)
	}
	if err := copyFileAtomic(src, dst); err != nil {
		return false, err
	}
	return false, fileutil.Remove(src)
}

// copyFileAtomic copies src to dst through a temporary file beside dst, so
// dst never holds part of a copy.
func copyFileAtomic(src, dst string) (err error) {
	in, err := fileutil.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = fileutil.Remove(tmp.Name())
		}
	}()
	if _, err = io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	// CreateTemp makes the file with the store's file permissions.
	if err = tmp.Close(); err != nil {
		return err
	}
	return fileutil.Rename(tmp.Name(), dst)
}

// sameFileContent reports whether files a and b hold the same bytes.
func sameFileContent(a, b string) (bool, error) {
	infoA, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	infoB, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	if infoA.Size() != infoB.Size() {
		return false, nil
	}
	fa, err := fileutil.Open(a)
	if err != nil {
		return false, err
	}
	defer func() { _ = fa.Close() }()
	fb, err := fileutil.Open(b)
	if err != nil {
		return false, err
	}
	defer func() { _ = fb.Close() }()
	const chunk = 32 * 1024
	bufA, bufB := make([]byte, chunk), make([]byte, chunk)
	for {
		nA, errA := io.ReadFull(fa, bufA)
		nB, errB := io.ReadFull(fb, bufB)
		if !bytes.Equal(bufA[:nA], bufB[:nB]) {
			return false, nil
		}
		if errA == io.EOF || errA == io.ErrUnexpectedEOF {
			return errB == io.EOF || errB == io.ErrUnexpectedEOF, nil
		}
		if errA != nil {
			return false, errA
		}
		if errB != nil {
			return false, errB
		}
	}
}
