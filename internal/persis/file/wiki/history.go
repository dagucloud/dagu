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

// renameAttachment renames an attachment file. Tests replace it to fail as a
// rename across mounts does.
var renameAttachment = fileutil.Rename

// MoveHistory moves the attachments, revisions, and page metadata of the
// pages under each top-level directory that inRoot accepts to dst. There
// each is keyed by its page's path under that directory. It moves them
// whether or not the pages are still there.
//
// A page's revisions in dst stay, and the newest moved ones fill the rest of
// the per-page limit; the others are dropped. A creation time dst already has for a page stays. An attachment whose name
// dst already has for the page with other content stays where it is, and a
// warning names it. Calling it again after an interruption finishes the move.
func (s *Store) MoveHistory(ctx context.Context, inRoot func(dir string) bool, dst *Store) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	dst.mutationMu.Lock()
	defer dst.mutationMu.Unlock()

	if err := s.moveRevisions(inRoot, dst); err != nil {
		return err
	}
	if err := s.moveMetadata(inRoot, dst); err != nil {
		return err
	}
	return s.moveAttachments(ctx, inRoot, dst)
}

// underRoot returns the part of a page ID below its top-level directory, and
// false when inRoot does not accept that directory.
func underRoot(id string, inRoot func(dir string) bool) (string, bool) {
	first, rest, ok := strings.Cut(id, "/")
	return rest, ok && rest != "" && inRoot(first)
}

// moveRevisions copies the revision blobs to dst and records them in dst's
// manifest, then removes them here before dropping them from this store's
// manifest. Each revision stays recorded in one of the two manifests, and a
// move that stops partway leaves no blob here that neither records.
func (s *Store) moveRevisions(inRoot func(dir string) bool, dst *Store) error {
	if !s.revisionsEnabled() {
		return nil
	}
	manifest, err := s.loadRevisionsManifest()
	if err != nil {
		return err
	}
	moved := pageRevisionsManifest{}
	for id, entries := range manifest {
		if rel, ok := underRoot(id, inRoot); ok {
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
	for rel, entries := range moved {
		target[rel], err = s.mergeRevisions(target[rel], entries, dst)
		if err != nil {
			return err
		}
	}
	if err := dst.saveRevisionsManifest(target); err != nil {
		return err
	}
	for _, entries := range moved {
		for _, entry := range entries {
			if err := fileutil.Remove(s.revisionBlobPath(entry.File)); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("filewiki: failed to remove moved revision blob: %w", err)
			}
		}
	}
	return s.saveRevisionsManifest(manifest)
}

// mergeRevisions returns a page's revisions in dst, kept, with the newest of
// moved that fit under the per-page limit, newest first, and copies the
// blobs of the moved ones it takes to dst. No revision of dst is dropped, so
// no blob of dst loses its manifest entry.
func (s *Store) mergeRevisions(kept, moved []pageRevisionEntry, dst *Store) ([]pageRevisionEntry, error) {
	merged := slices.Clone(kept)
	inDst := make(map[string]bool, len(kept))
	for _, entry := range kept {
		inDst[entry.File] = true
	}
	moved = slices.DeleteFunc(slices.Clone(moved), func(e pageRevisionEntry) bool { return inDst[e.File] })
	slices.SortStableFunc(moved, newestFirst)
	for _, entry := range moved[:min(len(moved), max(pageRevisionLimit-len(kept), 0))] {
		if err := copyRevisionBlob(s.revisionBlobPath(entry.File), dst.revisionBlobPath(entry.File)); err != nil {
			return nil, err
		}
		merged = append(merged, entry)
	}
	slices.SortStableFunc(merged, newestFirst)
	return merged, nil
}

func newestFirst(a, b pageRevisionEntry) int { return b.SavedAt.Compare(a.SavedAt) }

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

func (s *Store) moveMetadata(inRoot func(dir string) bool, dst *Store) error {
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
		rel, ok := underRoot(id, inRoot)
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

func (s *Store) moveAttachments(ctx context.Context, inRoot func(dir string) bool, dst *Store) error {
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
		if !entry.IsDir() || !inRoot(entry.Name()) {
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
	var dirs []string
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
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
		kept, err := moveAttachmentFile(path, target)
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

// moveAttachmentFile moves src to dst, renaming it, or copying it when the
// rename crosses devices, as between two mounts of one file system. It
// reports true, and leaves src, when dst exists with other content.
func moveAttachmentFile(src, dst string) (bool, error) {
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
	err = renameAttachment(src, dst)
	if err == nil || !errors.Is(err, errCrossDevice) {
		return false, err
	}
	if err := copyFileAtomic(src, dst); err != nil {
		return false, err
	}
	return false, fileutil.Remove(src)
}

// copyFileAtomic durably copies src to dst through a temporary file beside
// dst, so dst never holds part of a copy, and the copy outlasts a crash once
// it returns.
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
	return fileutil.ReplaceFileDurable(tmp.Name(), dst)
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
