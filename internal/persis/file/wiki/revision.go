// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wiki

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	wikimodel "github.com/dagucloud/dagu/v2/internal/wiki"
)

const (
	pageRevisionsFileName = "revisions.json"
	pageRevisionsDirName  = "revisions"
	// pageRevisionLimit caps stored revisions per page; the oldest is
	// pruned when a new snapshot exceeds it.
	pageRevisionLimit = 20
)

// validPageRevisionName constrains revision identifiers to a filesystem-safe
// single path segment.
var validPageRevisionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.\-]{0,63}$`)

// pageRevisionEntry is one manifest row; File names the blob under the
// revisions directory.
type pageRevisionEntry struct {
	File    string    `json:"file"`
	SavedAt time.Time `json:"savedAt"`
	Size    int64     `json:"size"`
}

// pageRevisionsManifest maps page IDs to their revisions, newest first.
type pageRevisionsManifest map[string][]pageRevisionEntry

func (s *Store) revisionsEnabled() bool {
	return s.dataDir != ""
}

func (s *Store) revisionsManifestPath() string {
	return filepath.Join(s.dataDir, pageRevisionsFileName)
}

func (s *Store) revisionBlobPath(file string) string {
	return filepath.Join(s.dataDir, pageRevisionsDirName, file)
}

func (s *Store) loadRevisionsManifest() (pageRevisionsManifest, error) {
	manifest := pageRevisionsManifest{}
	data, err := os.ReadFile(s.revisionsManifestPath()) //nolint:gosec // path is rooted in the store data dir.
	if os.IsNotExist(err) {
		return manifest, nil
	}
	if err != nil {
		return manifest, fmt.Errorf("filewiki: failed to read revisions manifest: %w", err)
	}
	if len(data) == 0 {
		return manifest, nil
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, fmt.Errorf("filewiki: failed to parse revisions manifest: %w", err)
	}
	return manifest, nil
}

func (s *Store) saveRevisionsManifest(manifest pageRevisionsManifest) error {
	path := s.revisionsManifestPath()
	if len(manifest) == 0 {
		if err := fileutil.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("filewiki: failed to remove revisions manifest: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), wikiDirPermissions); err != nil {
		return fmt.Errorf("filewiki: failed to create revisions directory: %w", err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("filewiki: failed to encode revisions manifest: %w", err)
	}
	data = append(data, '\n')
	if err := fileutil.WriteFileAtomic(path, data, filePermissions); err != nil {
		return fmt.Errorf("filewiki: failed to write revisions manifest: %w", err)
	}
	return nil
}

func newPageRevisionName(now time.Time) string {
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	return now.UTC().Format("20060102T150405") + "-" + hex.EncodeToString(suffix) + ".md"
}

// snapshotRevision stores prior page content as a revision before an
// overwrite. Failures must not fail the save; the caller logs the error.
// Callers hold mutationMu, which serializes manifest read-modify-write.
func (s *Store) snapshotRevision(id string, prior []byte, next []byte) error {
	if !s.revisionsEnabled() || string(prior) == string(next) {
		return nil
	}
	manifest, err := s.loadRevisionsManifest()
	if err != nil {
		return err
	}

	file := newPageRevisionName(time.Now())
	blobPath := s.revisionBlobPath(file)
	if err := os.MkdirAll(filepath.Dir(blobPath), wikiDirPermissions); err != nil {
		return fmt.Errorf("filewiki: failed to create revisions directory: %w", err)
	}
	if err := fileutil.WriteFileAtomic(blobPath, prior, filePermissions); err != nil {
		return fmt.Errorf("filewiki: failed to write revision blob: %w", err)
	}

	entries := append([]pageRevisionEntry{{
		File:    file,
		SavedAt: time.Now().UTC(),
		Size:    int64(len(prior)),
	}}, manifest[id]...)
	for len(entries) > pageRevisionLimit {
		last := entries[len(entries)-1]
		entries = entries[:len(entries)-1]
		if err := fileutil.Remove(s.revisionBlobPath(last.File)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("filewiki: failed to prune revision blob: %w", err)
		}
	}
	manifest[id] = entries
	return s.saveRevisionsManifest(manifest)
}

// deleteRevisions removes all revisions for a page ID.
func (s *Store) deleteRevisions(id string) error {
	if !s.revisionsEnabled() {
		return nil
	}
	manifest, err := s.loadRevisionsManifest()
	if err != nil {
		return err
	}
	entries, ok := manifest[id]
	if !ok {
		return nil
	}
	for _, entry := range entries {
		if err := fileutil.Remove(s.revisionBlobPath(entry.File)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("filewiki: failed to remove revision blob: %w", err)
		}
	}
	delete(manifest, id)
	return s.saveRevisionsManifest(manifest)
}

// deleteRevisionsPrefix removes revisions for a directory subtree.
func (s *Store) deleteRevisionsPrefix(id string) error {
	if !s.revisionsEnabled() {
		return nil
	}
	manifest, err := s.loadRevisionsManifest()
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
			if err := fileutil.Remove(s.revisionBlobPath(entry.File)); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("filewiki: failed to remove revision blob: %w", err)
			}
		}
		delete(manifest, key)
		changed = true
	}
	if !changed {
		return nil
	}
	return s.saveRevisionsManifest(manifest)
}

// renameRevisions carries revisions from one page ID to another.
func (s *Store) renameRevisions(oldID, newID string) error {
	if !s.revisionsEnabled() {
		return nil
	}
	manifest, err := s.loadRevisionsManifest()
	if err != nil {
		return err
	}
	entries, ok := manifest[oldID]
	if !ok {
		return nil
	}
	manifest[newID] = entries
	delete(manifest, oldID)
	return s.saveRevisionsManifest(manifest)
}

// renameRevisionsPrefix carries revisions for a directory subtree.
func (s *Store) renameRevisionsPrefix(oldID, newID string) error {
	if !s.revisionsEnabled() {
		return nil
	}
	manifest, err := s.loadRevisionsManifest()
	if err != nil {
		return err
	}
	prefix := oldID + "/"
	renamed := pageRevisionsManifest{}
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
	return s.saveRevisionsManifest(manifest)
}

// ListRevisions returns stored revisions for a page, newest first,
// without content.
func (s *Store) ListRevisions(_ context.Context, id string) ([]wikimodel.PageRevision, error) {
	if err := wikimodel.ValidatePageID(id); err != nil {
		return nil, err
	}
	if !s.revisionsEnabled() {
		return nil, nil
	}
	manifest, err := s.loadRevisionsManifest()
	if err != nil {
		return nil, err
	}
	entries := manifest[id]
	revisions := make([]wikimodel.PageRevision, 0, len(entries))
	for _, entry := range entries {
		revisions = append(revisions, wikimodel.PageRevision{
			Rev:     strings.TrimSuffix(entry.File, ".md"),
			SavedAt: entry.SavedAt,
			Size:    entry.Size,
		})
	}
	return revisions, nil
}

// GetRevision returns one stored revision including its content.
func (s *Store) GetRevision(_ context.Context, id, rev string) (*wikimodel.PageRevision, error) {
	if err := wikimodel.ValidatePageID(id); err != nil {
		return nil, err
	}
	if !s.revisionsEnabled() || !validPageRevisionName.MatchString(rev) {
		return nil, wikimodel.ErrPageRevisionNotFound
	}
	manifest, err := s.loadRevisionsManifest()
	if err != nil {
		return nil, err
	}
	for _, entry := range manifest[id] {
		if strings.TrimSuffix(entry.File, ".md") != rev {
			continue
		}
		data, err := os.ReadFile(s.revisionBlobPath(entry.File)) //nolint:gosec // rev is validated against a safe pattern.
		if err != nil {
			if os.IsNotExist(err) {
				return nil, wikimodel.ErrPageRevisionNotFound
			}
			return nil, fmt.Errorf("filewiki: failed to read revision blob: %w", err)
		}
		return &wikimodel.PageRevision{
			Rev:     rev,
			SavedAt: entry.SavedAt,
			Size:    entry.Size,
			Content: string(data),
		}, nil
	}
	return nil, wikimodel.ErrPageRevisionNotFound
}
