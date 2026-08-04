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
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/core/docs"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/persis/file/dag/grep"
	"github.com/goccy/go-yaml"
)

// Verify Store implements docs.DocStore at compile time.
var _ docs.DocStore = (*Store)(nil)

const (
	docDirPermissions      = 0750
	filePermissions        = 0600
	docSearchCursorVersion = 1
	docIndexCheckInterval  = 5 * time.Second
)

// docFrontmatter holds the YAML fields in the doc file frontmatter.
type docFrontmatter struct {
	Title       string `yaml:"title,omitempty"`
	Description string `yaml:"description,omitempty"`
}

// Store implements a file-based doc store.
// Docs are stored as files: {baseDir}/{id}.md
// Each file contains optional YAML frontmatter (title, description) and a Markdown body.
type Store struct {
	baseDir string

	mutationMu         sync.Mutex
	mu                 sync.RWMutex
	indexBuilt         bool
	indexCheckedAt     time.Time
	indexCheckInterval time.Duration
	docs               map[string]docIndexEntry
	dirs               map[string]docDirIndexEntry
}

type docIndexEntry struct {
	ID          string
	RelPath     string
	AbsPath     string
	Title       string
	Description string
	ModTime     time.Time
	Size        int64
	Mode        os.FileMode
	Readable    bool
}

type docDirIndexEntry struct {
	ID      string
	AbsPath string
	ModTime time.Time
	Size    int64
	Mode    os.FileMode
}

// New creates a new file-based doc store.
func New(baseDir string) *Store {
	_ = os.MkdirAll(baseDir, docDirPermissions) // best effort
	return &Store{
		baseDir:            baseDir,
		indexCheckInterval: docIndexCheckInterval,
		docs:               make(map[string]docIndexEntry),
		dirs:               make(map[string]docDirIndexEntry),
	}
}

// safePath validates that the given path stays within baseDir (preventing
// path traversal, including via symlinks) and returns the cleaned absolute path.
func (s *Store) safePath(p string, id string) (string, error) {
	cleaned := filepath.Clean(p)

	resolvedBase, err := filepath.EvalSymlinks(s.baseDir)
	if err != nil {
		return "", fmt.Errorf("filedoc: cannot resolve base dir: %w", err)
	}
	resolvedDir, err := filepath.EvalSymlinks(filepath.Dir(cleaned))
	if err != nil {
		// Parent dir may not exist yet (e.g. during Create); fall back to lexical check.
		if !strings.HasPrefix(cleaned, filepath.Clean(s.baseDir)+string(filepath.Separator)) {
			return "", fmt.Errorf("filedoc: path traversal detected for id %q", id)
		}
		return cleaned, nil
	}
	fullResolved := filepath.Join(resolvedDir, filepath.Base(cleaned))
	if !strings.HasPrefix(fullResolved, resolvedBase+string(filepath.Separator)) {
		return "", fmt.Errorf("filedoc: path traversal detected for id %q", id)
	}
	return cleaned, nil
}

// docFilePath returns the .md file path for a doc ID with path-traversal validation.
func (s *Store) docFilePath(id string) (string, error) {
	return s.safePath(filepath.Join(s.baseDir, id+".md"), id)
}

func cleanDocPathPrefix(prefix string) (string, error) {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return "", nil
	}
	if err := docs.ValidateDocID(prefix); err != nil {
		return "", err
	}
	return prefix, nil
}

func scopedDocID(prefix, id string) (string, error) {
	prefix, err := cleanDocPathPrefix(prefix)
	if err != nil {
		return "", err
	}
	if prefix == "" {
		return id, nil
	}
	if err := docs.ValidateDocID(id); err != nil {
		return "", err
	}
	return prefix + "/" + id, nil
}

func joinDocID(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "/" + child
}

func parentDocID(id string) string {
	idx := strings.LastIndex(id, "/")
	if idx < 0 {
		return ""
	}
	return id[:idx]
}

func relativeDocID(id, prefix string) (string, bool) {
	if prefix == "" {
		return id, true
	}
	prefixWithSlash := prefix + "/"
	if !strings.HasPrefix(id, prefixWithSlash) {
		return "", false
	}
	rel := strings.TrimPrefix(id, prefixWithSlash)
	return rel, rel != ""
}

func fingerprintsEqual(modTime time.Time, size int64, mode os.FileMode, info os.FileInfo) bool {
	return modTime.Equal(info.ModTime()) && size == info.Size() && mode == info.Mode()
}

func statRegularDocFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, docs.ErrDocNotFound
	}
	return info, nil
}

func statDocDir(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, docs.ErrDocNotFound
	}
	return info, nil
}

func pathExistsNoFollow(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func readRegularDocFile(path string) ([]byte, os.FileInfo, error) {
	initialInfo, err := statRegularDocFile(path)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Open(path) //nolint:gosec // path is validated before and after open.
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		_ = file.Close()
	}()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if openedInfo.Mode()&os.ModeSymlink != 0 || !openedInfo.Mode().IsRegular() {
		return nil, nil, docs.ErrDocNotFound
	}
	currentInfo, err := statRegularDocFile(path)
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(initialInfo, currentInfo) || !os.SameFile(openedInfo, currentInfo) {
		return nil, nil, fmt.Errorf("filedoc: doc changed while opening: %s", path)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, err
	}
	return data, currentInfo, nil
}

func (s *Store) ensureFreshIndex(ctx context.Context) error {
	s.mu.RLock()
	fresh := s.indexBuilt && s.indexCheckInterval > 0 && time.Since(s.indexCheckedAt) < s.indexCheckInterval
	s.mu.RUnlock()
	if fresh {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.indexBuilt {
		if err := s.rebuildIndexLocked(ctx); err != nil {
			return err
		}
		s.markIndexCheckedLocked()
		return nil
	}
	if s.indexCheckInterval > 0 && time.Since(s.indexCheckedAt) < s.indexCheckInterval {
		return nil
	}
	if err := s.refreshIndexLocked(ctx); err != nil {
		return err
	}
	s.markIndexCheckedLocked()
	return nil
}

func (s *Store) markIndexCheckedLocked() {
	s.indexCheckedAt = time.Now()
}

func (s *Store) rebuildIndexLocked(ctx context.Context) error {
	s.docs = make(map[string]docIndexEntry)
	s.dirs = make(map[string]docDirIndexEntry)

	info, err := os.Stat(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			s.indexBuilt = true
			return nil
		}
		return fmt.Errorf("filedoc: failed to access docs directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("filedoc: docs path %s is not a directory", s.baseDir)
	}

	s.recordDirLocked("", s.baseDir, info)
	if err := s.scanDirLocked(ctx, "", s.baseDir, true); err != nil {
		return err
	}
	s.indexBuilt = true
	return nil
}

func (s *Store) refreshIndexLocked(ctx context.Context) error {
	info, err := os.Stat(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			s.docs = make(map[string]docIndexEntry)
			s.dirs = make(map[string]docDirIndexEntry)
			return nil
		}
		return fmt.Errorf("filedoc: failed to access docs directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("filedoc: docs path %s is not a directory", s.baseDir)
	}

	s.recordDirLocked("", s.baseDir, info)
	if err := s.scanDirLocked(ctx, "", s.baseDir, false); err != nil {
		return err
	}

	dirIDs := make([]string, 0, len(s.dirs))
	for id := range s.dirs {
		if id != "" {
			dirIDs = append(dirIDs, id)
		}
	}
	sort.Strings(dirIDs)
	for _, id := range dirIDs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		entry, ok := s.dirs[id]
		if !ok {
			continue
		}
		info, err := statDocDir(entry.AbsPath)
		if err != nil {
			if os.IsNotExist(err) {
				s.removeDirSubtreeLocked(id)
				continue
			}
			if errors.Is(err, docs.ErrDocNotFound) {
				s.removeDirSubtreeLocked(id)
				continue
			}
			logger.Warn(ctx, "Skipping unreadable doc directory", tag.File(entry.AbsPath), tag.Error(err))
			continue
		}
		s.recordDirLocked(id, entry.AbsPath, info)
		if err := s.scanDirLocked(ctx, id, entry.AbsPath, false); err != nil {
			return err
		}
	}

	docIDs := make([]string, 0, len(s.docs))
	for id := range s.docs {
		docIDs = append(docIDs, id)
	}
	sort.Strings(docIDs)
	for _, id := range docIDs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		entry, ok := s.docs[id]
		if !ok {
			continue
		}
		info, err := statRegularDocFile(entry.AbsPath)
		if err != nil {
			if os.IsNotExist(err) {
				delete(s.docs, id)
				continue
			}
			if errors.Is(err, docs.ErrDocNotFound) {
				delete(s.docs, id)
				continue
			}
			logger.Warn(ctx, "Skipping unreadable doc file", tag.File(entry.RelPath), tag.Error(err))
			continue
		}
		if fingerprintsEqual(entry.ModTime, entry.Size, entry.Mode, info) {
			continue
		}
		if err := s.upsertDocLocked(ctx, id, entry.AbsPath, info); err != nil {
			logger.Warn(ctx, "Skipping doc with changed metadata", tag.File(entry.RelPath), tag.Error(err))
		}
	}

	return nil
}

func (s *Store) scanDirLocked(ctx context.Context, dirID, absPath string, recurseExisting bool) error {
	entries, err := os.ReadDir(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			s.removeDirSubtreeLocked(dirID)
			return nil
		}
		if dirID != "" {
			logger.Warn(ctx, "Skipping unreadable doc directory", tag.File(absPath), tag.Error(err))
			return nil
		}
		return fmt.Errorf("filedoc: failed to read docs directory %s: %w", absPath, err)
	}

	seenDocs := make(map[string]struct{})
	seenDirs := make(map[string]struct{})
	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		name := entry.Name()
		childAbsPath := filepath.Join(absPath, name)
		info, err := entry.Info()
		if err != nil {
			logger.Warn(ctx, "Skipping unreadable doc path", tag.File(childAbsPath), tag.Error(err))
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.IsDir() {
			childID := joinDocID(dirID, name)
			if err := docs.ValidateDocID(childID); err != nil {
				continue
			}
			seenDirs[childID] = struct{}{}
			_, existed := s.dirs[childID]
			s.recordDirLocked(childID, childAbsPath, info)
			if !existed || recurseExisting {
				if err := s.scanDirLocked(ctx, childID, childAbsPath, recurseExisting); err != nil {
					return err
				}
			}
			continue
		}

		if filepath.Ext(name) != ".md" {
			continue
		}

		childID := joinDocID(dirID, strings.TrimSuffix(name, ".md"))
		relPath := filepath.ToSlash(joinDocID(dirID, name))
		if err := docs.ValidateDocID(childID); err != nil {
			logger.Debug(ctx, "Skipping non-conforming doc file", tag.File(relPath), tag.Reason(err.Error()))
			continue
		}

		if !info.Mode().IsRegular() {
			continue
		}
		seenDocs[childID] = struct{}{}
		current, exists := s.docs[childID]
		if exists && fingerprintsEqual(current.ModTime, current.Size, current.Mode, info) {
			continue
		}
		if err := s.upsertDocLocked(ctx, childID, childAbsPath, info); err != nil {
			logger.Warn(ctx, "Skipping doc file", tag.File(relPath), tag.Error(err))
		}
	}

	for id := range s.docs {
		if parentDocID(id) != dirID {
			continue
		}
		if _, ok := seenDocs[id]; !ok {
			delete(s.docs, id)
		}
	}
	for id := range s.dirs {
		if id == "" || parentDocID(id) != dirID {
			continue
		}
		if _, ok := seenDirs[id]; !ok {
			s.removeDirSubtreeLocked(id)
		}
	}

	return nil
}

func (s *Store) recordDirLocked(id, absPath string, info os.FileInfo) {
	s.dirs[id] = docDirIndexEntry{
		ID:      id,
		AbsPath: absPath,
		ModTime: info.ModTime(),
		Size:    info.Size(),
		Mode:    info.Mode(),
	}
}

func (s *Store) upsertDocLocked(ctx context.Context, id, absPath string, info os.FileInfo) error {
	title := titleFromID(id)
	var description string
	readable := false
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return docs.ErrDocNotFound
	}
	data, readInfo, err := readRegularDocFile(absPath)
	if err == nil {
		info = readInfo
		doc, parseErr := parseDocFile(data, id)
		if parseErr != nil {
			return parseErr
		}
		title = doc.Title
		description = doc.Description
		readable = true
	} else if errors.Is(err, docs.ErrDocNotFound) {
		return err
	}
	s.docs[id] = docIndexEntry{
		ID:          id,
		RelPath:     filepath.ToSlash(id + ".md"),
		AbsPath:     absPath,
		Title:       title,
		Description: description,
		ModTime:     info.ModTime(),
		Size:        info.Size(),
		Mode:        info.Mode(),
		Readable:    readable,
	}
	return ctx.Err()
}

func (s *Store) upsertDocIndexAfterMutation(ctx context.Context, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.indexBuilt {
		return
	}
	filePath, err := s.docFilePath(id)
	if err != nil {
		logger.Warn(ctx, "Failed to update doc index", tag.File(id), tag.Error(err))
		return
	}
	info, err := statRegularDocFile(filePath)
	if err != nil {
		logger.Warn(ctx, "Failed to stat doc for index update", tag.File(id), tag.Error(err))
		return
	}
	if err := s.upsertDocLocked(ctx, id, filePath, info); err != nil {
		logger.Warn(ctx, "Failed to update doc index", tag.File(id), tag.Error(err))
		return
	}
	s.recordParentDirsLocked(ctx, id)
	s.markIndexCheckedLocked()
}

func (s *Store) removeDocIndexAfterDelete(ctx context.Context, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.indexBuilt {
		return
	}
	delete(s.docs, id)
	s.pruneMissingParentsLocked(ctx, parentDocID(id))
	s.markIndexCheckedLocked()
}

func (s *Store) removeDirIndexAfterDelete(ctx context.Context, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.indexBuilt {
		return
	}
	s.removeDirSubtreeLocked(id)
	s.pruneMissingParentsLocked(ctx, parentDocID(id))
	s.markIndexCheckedLocked()
}

func (s *Store) rebuildIndexAfterMutation(ctx context.Context) {
	rebuildCtx := context.Background()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.indexBuilt {
		return
	}
	if err := s.rebuildIndexLocked(rebuildCtx); err != nil {
		logger.Warn(ctx, "Failed to rebuild doc index", tag.Error(err))
		return
	}
	s.markIndexCheckedLocked()
}

func (s *Store) removeDirSubtreeLocked(id string) {
	delete(s.dirs, id)
	prefix := id + "/"
	for docID := range s.docs {
		if strings.HasPrefix(docID, prefix) {
			delete(s.docs, docID)
		}
	}
	for dirID := range s.dirs {
		if strings.HasPrefix(dirID, prefix) {
			delete(s.dirs, dirID)
		}
	}
}

func (s *Store) recordParentDirsLocked(ctx context.Context, id string) {
	parent := parentDocID(id)
	for {
		if ctx.Err() != nil {
			return
		}
		absPath := s.baseDir
		if parent != "" {
			absPath = filepath.Join(s.baseDir, filepath.FromSlash(parent))
		}
		info, err := os.Stat(absPath)
		if err == nil && info.IsDir() {
			s.recordDirLocked(parent, absPath, info)
		}
		if parent == "" {
			return
		}
		parent = parentDocID(parent)
	}
}

func (s *Store) pruneMissingParentsLocked(ctx context.Context, id string) {
	for id != "" {
		if ctx.Err() != nil {
			return
		}
		absPath := filepath.Join(s.baseDir, filepath.FromSlash(id))
		info, err := os.Stat(absPath)
		if err == nil && info.IsDir() {
			s.recordDirLocked(id, absPath, info)
			return
		}
		delete(s.dirs, id)
		id = parentDocID(id)
	}
	if info, err := os.Stat(s.baseDir); err == nil && info.IsDir() {
		s.recordDirLocked("", s.baseDir, info)
	}
}

// parseDocFile parses a doc .md file into an docs.Doc.
// The file format is optional YAML frontmatter between --- delimiters, followed by markdown body.
// Content always contains the full file (including frontmatter); frontmatter is parsed to extract title and description.
func parseDocFile(data []byte, id string) (*docs.Doc, error) {
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	content = strings.TrimRight(content, "\n")

	var title string
	var description string

	if strings.HasPrefix(content, "---\n") {
		rest := content[4:]

		closingIdx := strings.Index(rest, "\n---\n")
		if closingIdx == -1 {
			if strings.HasSuffix(rest, "\n---") {
				closingIdx = len(rest) - 4
			}
		}

		if closingIdx >= 0 {
			frontmatterStr := rest[:closingIdx]

			var fm docFrontmatter
			if err := yaml.Unmarshal([]byte(frontmatterStr), &fm); err == nil {
				title = fm.Title
				description = fm.Description
			}
		}
	}

	if title == "" {
		title = titleFromID(id)
	}

	return &docs.Doc{
		ID:          id,
		Title:       title,
		Description: description,
		Content:     content,
	}, nil
}

// titleFromID derives a display title from a doc ID.
// E.g., "docs/deploy-guide" → "deploy-guide"
func titleFromID(id string) string {
	parts := strings.Split(id, "/")
	return parts[len(parts)-1]
}

// List returns a paginated tree of doc nodes.
func (s *Store) List(ctx context.Context, opts docs.ListDocsOptions) (*exec.PaginatedResult[*docs.DocTreeNode], error) {
	sortField, sortOrder := normalizeSortParams(opts.Sort, opts.Order)
	pathPrefix, err := cleanDocPathPrefix(opts.PathPrefix)
	if err != nil {
		return nil, err
	}
	if err := s.ensureFreshIndex(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	tree := s.buildTreeFromIndexLocked(pathPrefix, sortField, sortOrder, opts.ExcludePathRoots)
	s.mu.RUnlock()

	pg := exec.NewPaginator(opts.Page, opts.PerPage)
	total := len(tree)
	offset := min(pg.Offset(), total)
	end := min(offset+pg.Limit(), total)
	pageItems := tree[offset:end]

	result := exec.NewPaginatedResult(pageItems, total, pg)
	return &result, nil
}

// flatDocItem is an intermediate struct for flat listing with sort support.
type flatDocItem struct {
	meta docs.DocMetadata
}

// ListFlat returns a paginated flat list of doc metadata.
func (s *Store) ListFlat(ctx context.Context, opts docs.ListDocsOptions) (*exec.PaginatedResult[docs.DocMetadata], error) {
	sortField, sortOrder := normalizeSortParams(opts.Sort, opts.Order)
	pathPrefix, err := cleanDocPathPrefix(opts.PathPrefix)
	if err != nil {
		return nil, err
	}
	if err := s.ensureFreshIndex(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	items := make([]flatDocItem, 0, len(s.docs))
	for _, doc := range s.docs {
		if !doc.Readable {
			continue
		}
		id, ok := relativeDocID(doc.ID, pathPrefix)
		if !ok || docPathRootExcluded(id, opts.ExcludePathRoots) {
			continue
		}
		items = append(items, flatDocItem{
			meta: docs.DocMetadata{
				ID:          id,
				Title:       doc.Title,
				Description: doc.Description,
				ModTime:     doc.ModTime,
			},
		})
	}
	s.mu.RUnlock()

	sort.Slice(items, func(i, j int) bool {
		var less bool
		switch sortField {
		case "mtime":
			if items[i].meta.ModTime.Equal(items[j].meta.ModTime) {
				less = items[i].meta.ID < items[j].meta.ID
			} else {
				less = items[i].meta.ModTime.Before(items[j].meta.ModTime)
			}
		case "type":
			less = items[i].meta.ID < items[j].meta.ID
		default: // "name"
			less = strings.ToLower(items[i].meta.ID) < strings.ToLower(items[j].meta.ID)
		}
		if sortOrder == "desc" {
			return !less
		}
		return less
	})

	metadata := make([]docs.DocMetadata, len(items))
	for i, item := range items {
		metadata[i] = item.meta
	}

	pg := exec.NewPaginator(opts.Page, opts.PerPage)
	total := len(metadata)
	offset := min(pg.Offset(), total)
	end := min(offset+pg.Limit(), total)
	pageItems := metadata[offset:end]

	result := exec.NewPaginatedResult(pageItems, total, pg)
	return &result, nil
}

func docPathRootExcluded(id string, excludedRoots []string) bool {
	if len(excludedRoots) == 0 {
		return false
	}
	root, _, _ := strings.Cut(id, "/")
	return slices.Contains(excludedRoots, root)
}

// Get retrieves a doc by its ID.
func (s *Store) Get(_ context.Context, id string) (*docs.Doc, error) {
	if err := docs.ValidateDocID(id); err != nil {
		return nil, err
	}

	filePath, err := s.docFilePath(id)
	if err != nil {
		return nil, err
	}

	data, info, err := readRegularDocFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, docs.ErrDocNotFound
		}
		if errors.Is(err, docs.ErrDocNotFound) {
			return nil, docs.ErrDocNotFound
		}
		return nil, fmt.Errorf("filedoc: failed to read file %s: %w", filePath, err)
	}

	doc, err := parseDocFile(data, id)
	if err != nil {
		return nil, fmt.Errorf("filedoc: failed to parse doc %s: %w", id, err)
	}

	doc.FilePath = filePath
	doc.CreatedAt = s.docCreatedAt(id, filePath, info)
	doc.UpdatedAt = info.ModTime().UTC().Format(time.RFC3339)

	return doc, nil
}

// Create creates a new doc file.
func (s *Store) Create(ctx context.Context, id, content string) error {
	if err := docs.ValidateDocID(id); err != nil {
		return err
	}

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	filePath, err := s.docFilePath(id)
	if err != nil {
		return err
	}

	// Ensure parent directories exist.
	if err := os.MkdirAll(filepath.Dir(filePath), docDirPermissions); err != nil {
		return fmt.Errorf("filedoc: failed to create parent directories: %w", err)
	}

	data := []byte(content)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}

	// Use O_EXCL for atomic create — prevents race between concurrent creates.
	f, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePermissions) //nolint:gosec // filePath is validated by docFilePath
	if err != nil {
		if os.IsExist(err) {
			return docs.ErrDocAlreadyExists
		}
		return fmt.Errorf("filedoc: failed to create file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("filedoc: failed to write file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("filedoc: failed to close file: %w", err)
	}
	if err := s.setDocCreatedAt(id, createdAtNow()); err != nil {
		return err
	}

	s.upsertDocIndexAfterMutation(ctx, id)
	return nil
}

// Update modifies an existing doc file.
func (s *Store) Update(ctx context.Context, id, content string) error {
	if err := docs.ValidateDocID(id); err != nil {
		return err
	}

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	filePath, err := s.docFilePath(id)
	if err != nil {
		return err
	}

	info, err := statRegularDocFile(filePath)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, docs.ErrDocNotFound) {
			return docs.ErrDocNotFound
		}
		return fmt.Errorf("filedoc: failed to stat file %s: %w", filePath, err)
	}
	createdAt := s.docCreatedAt(id, filePath, info)

	data := []byte(content)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	if err := fileutil.WriteFileAtomic(filePath, data, filePermissions); err != nil {
		return fmt.Errorf("filedoc: failed to write file: %w", err)
	}
	if err := s.setDocCreatedAt(id, createdAt); err != nil {
		return err
	}

	s.upsertDocIndexAfterMutation(ctx, id)
	return nil
}

// Delete removes a doc file or directory and cleans up empty parent directories.
// File takes precedence: if both foo.md and foo/ exist, Delete("foo") deletes the file.
func (s *Store) Delete(ctx context.Context, id string) error {
	if err := docs.ValidateDocID(id); err != nil {
		return err
	}

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	// Try a file before falling back to a directory.
	filePath, err := s.docFilePath(id)
	if err != nil {
		return err
	}
	if _, err := statRegularDocFile(filePath); err == nil {
		if err := fileutil.Remove(filePath); err != nil {
			return fmt.Errorf("filedoc: failed to delete file: %w", err)
		}
		if err := s.deleteDocCreatedAt(id); err != nil {
			logger.Warn(ctx, "Failed to remove doc metadata", tag.File(filePath), tag.Error(err))
		}
		s.cleanEmptyParents(filepath.Dir(filePath))
		s.removeDocIndexAfterDelete(ctx, id)
		return nil
	} else if err != nil && !os.IsNotExist(err) && !errors.Is(err, docs.ErrDocNotFound) {
		return fmt.Errorf("filedoc: failed to stat file %s: %w", filePath, err)
	}

	// Try as directory.
	dirPath, err := s.dirPath(id)
	if err != nil {
		return err
	}
	_, err = statDocDir(dirPath)
	if err != nil {
		return docs.ErrDocNotFound
	}
	if err := s.safeDeleteDir(dirPath); err != nil {
		return fmt.Errorf("filedoc: failed to delete directory: %w", err)
	}
	if err := s.deleteDocCreatedAtPrefix(id); err != nil {
		logger.Warn(ctx, "Failed to remove doc metadata", tag.File(dirPath), tag.Error(err))
	}
	s.cleanEmptyParents(filepath.Dir(dirPath))
	s.removeDirIndexAfterDelete(ctx, id)
	return nil
}

// safeDeleteDir removes a directory tree safely without using os.RemoveAll.
// It walks depth-first and uses fileutil.Remove for each entry, which never follows
// symlinks and only removes empty directories.
func (s *Store) safeDeleteDir(dirPath string) error {
	var paths []string
	err := filepath.WalkDir(dirPath, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return err
	}

	// Reverse to delete deepest entries first (children before parents).
	slices.Reverse(paths)

	var lastErr error
	for _, p := range paths {
		// fileutil.Remove deletes file/symlink/empty-dir. Never follows symlinks.
		if err := fileutil.Remove(p); err != nil && !os.IsNotExist(err) {
			lastErr = err
		}
	}
	return lastErr
}

// DeleteBatch deletes multiple docs/directories in one operation.
// Not-found items are treated as success (idempotency for safe retries).
func (s *Store) DeleteBatch(ctx context.Context, ids []string) ([]string, []docs.DeleteError, error) {
	var deleted []string
	var failed []docs.DeleteError

	// Validate all IDs upfront, separate valid from invalid.
	validIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if err := docs.ValidateDocID(id); err != nil {
			failed = append(failed, docs.DeleteError{ID: id, Error: err.Error()})
		} else {
			validIDs = append(validIDs, id)
		}
	}

	// Sort shortest-first for parent-before-child deduplication.
	sort.Slice(validIDs, func(i, j int) bool { return len(validIDs[i]) < len(validIDs[j]) })

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	// Track deleted directory prefixes to skip subsumed children.
	deletedPrefixes := map[string]bool{}

	for _, id := range validIDs {
		// Skip if already covered by a deleted parent directory.
		if isSubsumedByPrefix(id, deletedPrefixes) {
			deleted = append(deleted, id)
			continue
		}

		// Try as file first.
		filePath, err := s.docFilePath(id)
		if err != nil {
			failed = append(failed, docs.DeleteError{ID: id, Error: err.Error()})
			continue
		}
		if _, err := statRegularDocFile(filePath); err == nil {
			if err := fileutil.Remove(filePath); err != nil {
				failed = append(failed, docs.DeleteError{ID: id, Error: err.Error()})
				continue
			}
			if err := s.deleteDocCreatedAt(id); err != nil {
				logger.Warn(ctx, "Failed to remove doc metadata", tag.File(filePath), tag.Error(err))
			}
			s.cleanEmptyParents(filepath.Dir(filePath))
			s.removeDocIndexAfterDelete(ctx, id)
			deleted = append(deleted, id)
			continue
		} else if !os.IsNotExist(err) && !errors.Is(err, docs.ErrDocNotFound) {
			failed = append(failed, docs.DeleteError{ID: id, Error: err.Error()})
			continue
		}

		// Try as directory.
		dirPath, err := s.dirPath(id)
		if err != nil {
			failed = append(failed, docs.DeleteError{ID: id, Error: err.Error()})
			continue
		}
		_, err = statDocDir(dirPath)
		if os.IsNotExist(err) || errors.Is(err, docs.ErrDocNotFound) {
			// Not found → treat as success (idempotency).
			s.removeDocIndexAfterDelete(ctx, id)
			s.removeDirIndexAfterDelete(ctx, id)
			deleted = append(deleted, id)
			continue
		}
		if err != nil {
			failed = append(failed, docs.DeleteError{ID: id, Error: err.Error()})
			continue
		}
		if err := s.safeDeleteDir(dirPath); err != nil {
			failed = append(failed, docs.DeleteError{ID: id, Error: err.Error()})
			continue
		}
		if err := s.deleteDocCreatedAtPrefix(id); err != nil {
			logger.Warn(ctx, "Failed to remove doc metadata", tag.File(dirPath), tag.Error(err))
		}
		s.cleanEmptyParents(filepath.Dir(dirPath))
		s.removeDirIndexAfterDelete(ctx, id)
		deletedPrefixes[id+"/"] = true
		deleted = append(deleted, id)
	}

	return deleted, failed, nil
}

// isSubsumedByPrefix checks if id is a child of any deleted directory prefix.
func isSubsumedByPrefix(id string, prefixes map[string]bool) bool {
	for prefix := range prefixes {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

// dirPath returns the directory path for a doc ID with path-traversal validation.
func (s *Store) dirPath(id string) (string, error) {
	return s.safePath(filepath.Join(s.baseDir, id), id)
}

// Rename moves a doc (file or directory) from oldID to newID.
func (s *Store) Rename(ctx context.Context, oldID, newID string) error {
	if err := docs.ValidateDocID(oldID); err != nil {
		return err
	}
	if err := docs.ValidateDocID(newID); err != nil {
		return err
	}

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	// Try a file before falling back to a directory.
	oldFilePath, err := s.docFilePath(oldID)
	if err != nil {
		return err
	}
	newFilePath, err := s.docFilePath(newID)
	if err != nil {
		return err
	}

	if _, err := statRegularDocFile(oldFilePath); err == nil {
		// Old file exists — rename as file.
		exists, err := pathExistsNoFollow(newFilePath)
		if err != nil {
			return err
		}
		if exists {
			return docs.ErrDocAlreadyExists
		}
		if err := os.MkdirAll(filepath.Dir(newFilePath), docDirPermissions); err != nil {
			return fmt.Errorf("filedoc: failed to create target directories: %w", err)
		}
		if err := renameNoReplace(oldFilePath, newFilePath); err != nil {
			if errors.Is(err, docs.ErrDocAlreadyExists) {
				return docs.ErrDocAlreadyExists
			}
			return fmt.Errorf("filedoc: failed to rename file: %w", err)
		}
		if err := s.renameDocCreatedAt(oldID, newID); err != nil {
			return err
		}
		s.cleanEmptyParents(filepath.Dir(oldFilePath))
		s.removeDocIndexAfterDelete(ctx, oldID)
		s.upsertDocIndexAfterMutation(ctx, newID)
		return nil
	} else if err != nil && !os.IsNotExist(err) && !errors.Is(err, docs.ErrDocNotFound) {
		return err
	}

	// Try as directory.
	oldDirPath, err := s.dirPath(oldID)
	if err != nil {
		return err
	}
	if _, err := statDocDir(oldDirPath); err != nil {
		return docs.ErrDocNotFound
	}

	newDirPath, err := s.dirPath(newID)
	if err != nil {
		return err
	}
	// Check that neither a directory nor a file with the target name exists.
	exists, err := pathExistsNoFollow(newDirPath)
	if err != nil {
		return err
	}
	if exists {
		return docs.ErrDocAlreadyExists
	}
	exists, err = pathExistsNoFollow(newFilePath)
	if err != nil {
		return err
	}
	if exists {
		return docs.ErrDocAlreadyExists
	}

	if err := os.MkdirAll(filepath.Dir(newDirPath), docDirPermissions); err != nil {
		return fmt.Errorf("filedoc: failed to create target directories: %w", err)
	}
	if err := renameNoReplace(oldDirPath, newDirPath); err != nil {
		if errors.Is(err, docs.ErrDocAlreadyExists) {
			return docs.ErrDocAlreadyExists
		}
		return fmt.Errorf("filedoc: failed to rename directory: %w", err)
	}
	if err := s.renameDocCreatedAtPrefix(oldID, newID); err != nil {
		return err
	}
	s.cleanEmptyParents(filepath.Dir(oldDirPath))
	s.rebuildIndexAfterMutation(ctx)
	return nil
}

// Search searches all docs for the given query pattern.
func (s *Store) Search(ctx context.Context, query string) ([]*docs.DocSearchResult, error) {
	var results []*docs.DocSearchResult

	candidates, err := s.listSearchCandidates(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		data, _, err := readRegularDocFile(candidate.AbsPath)
		if err != nil {
			logger.Warn(ctx, "Failed to read doc while searching", tag.File(candidate.RelPath), tag.Error(err))
			continue
		}

		matches, err := grep.Grep(data, query, grep.DefaultGrepOptions)
		if err != nil {
			continue // no match or error — skip
		}

		doc, parseErr := parseDocFile(data, candidate.ID)
		title := candidate.ID
		var description string
		if parseErr == nil {
			title = doc.Title
			description = doc.Description
		}

		results = append(results, &docs.DocSearchResult{
			ID:          candidate.ID,
			Title:       title,
			Description: description,
			Matches:     matches,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].ID < results[j].ID
	})

	return results, nil
}

func docSearchPattern(query string) string {
	return fmt.Sprintf("(?i)%s", regexp.QuoteMeta(query))
}

type docSearchCursor struct {
	Version       int      `json:"v"`
	Query         string   `json:"q"`
	PathPrefix    string   `json:"prefix,omitempty"`
	ExcludedRoots []string `json:"exclude,omitempty"`
	ID            string   `json:"id,omitempty"`
}

type docMatchCursor struct {
	Version    int    `json:"v"`
	Query      string `json:"q"`
	PathPrefix string `json:"prefix,omitempty"`
	ID         string `json:"id"`
	Offset     int    `json:"offset"`
}

type docSearchCandidate struct {
	ID      string
	RelPath string
	AbsPath string
}

func (s *Store) listSearchCandidates(ctx context.Context, pathPrefix string) ([]docSearchCandidate, error) {
	if err := s.ensureFreshIndex(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	candidates := make([]docSearchCandidate, 0, len(s.docs))
	for _, doc := range s.docs {
		if !doc.Readable {
			continue
		}
		id, ok := relativeDocID(doc.ID, pathPrefix)
		if !ok {
			continue
		}
		candidates = append(candidates, docSearchCandidate{
			ID:      id,
			RelPath: filepath.ToSlash(id + ".md"),
			AbsPath: doc.AbsPath,
		})
	}
	s.mu.RUnlock()

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].ID < candidates[j].ID
	})
	return candidates, nil
}

func normalizeExcludedPathRoots(roots []string) []string {
	if len(roots) == 0 {
		return nil
	}
	normalized := slices.Clone(roots)
	sort.Strings(normalized)
	return slices.Compact(normalized)
}

func decodeDocSearchCursor(raw, query, pathPrefix string, excludedRoots []string) (docSearchCursor, error) {
	if raw == "" {
		return docSearchCursor{}, nil
	}
	var cursor docSearchCursor
	if err := exec.DecodeSearchCursor(raw, &cursor); err != nil {
		return docSearchCursor{}, err
	}
	if cursor.Version != docSearchCursorVersion ||
		cursor.Query != query ||
		cursor.PathPrefix != pathPrefix ||
		!slices.Equal(cursor.ExcludedRoots, excludedRoots) {
		return docSearchCursor{}, exec.ErrInvalidCursor
	}
	return cursor, nil
}

func decodeDocMatchCursor(raw, query, pathPrefix, id string) (docMatchCursor, error) {
	if raw == "" {
		return docMatchCursor{ID: id}, nil
	}
	var cursor docMatchCursor
	if err := exec.DecodeSearchCursor(raw, &cursor); err != nil {
		return docMatchCursor{}, err
	}
	if cursor.Version != docSearchCursorVersion || cursor.Query != query || cursor.PathPrefix != pathPrefix || cursor.ID != id || cursor.Offset < 0 {
		return docMatchCursor{}, exec.ErrInvalidCursor
	}
	return cursor, nil
}

// SearchCursor returns lightweight, cursor-based document search hits.
func (s *Store) SearchCursor(ctx context.Context, opts docs.SearchDocsOptions) (*exec.CursorResult[docs.DocSearchResult], error) {
	if opts.Query == "" {
		return &exec.CursorResult[docs.DocSearchResult]{Items: []docs.DocSearchResult{}}, nil
	}
	pathPrefix, err := cleanDocPathPrefix(opts.PathPrefix)
	if err != nil {
		return nil, err
	}
	excludedRoots := normalizeExcludedPathRoots(opts.ExcludePathRoots)

	cursor, err := decodeDocSearchCursor(opts.Cursor, opts.Query, pathPrefix, excludedRoots)
	if err != nil {
		return nil, err
	}

	limit := max(opts.Limit, 1)
	matchLimit := max(opts.MatchLimit, 1)
	results := make([]docs.DocSearchResult, 0, limit)
	pattern := docSearchPattern(opts.Query)
	var hasMore bool
	var nextCursor string

	candidates, err := s.listSearchCandidates(ctx, pathPrefix)
	if err != nil {
		return nil, err
	}

	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if cursor.ID != "" && candidate.ID <= cursor.ID {
			continue
		}
		if docPathRootExcluded(candidate.ID, excludedRoots) {
			continue
		}

		data, _, err := readRegularDocFile(candidate.AbsPath)
		if err != nil {
			logger.Warn(ctx, "Failed to read doc while searching", tag.File(candidate.RelPath), tag.Error(err))
			continue
		}

		window, err := grep.GrepWindow(data, pattern, grep.GrepOptions{
			IsRegexp: true,
			Before:   grep.DefaultGrepOptions.Before,
			After:    grep.DefaultGrepOptions.After,
			Limit:    matchLimit,
		})
		if err != nil {
			if errors.Is(err, grep.ErrNoMatch) {
				continue
			}
			logger.Warn(ctx, "Failed to search doc", tag.File(candidate.RelPath), tag.Error(err))
			continue
		}

		if len(results) == limit {
			hasMore = true
			nextCursor = exec.EncodeSearchCursor(docSearchCursor{
				Version:       docSearchCursorVersion,
				Query:         opts.Query,
				PathPrefix:    pathPrefix,
				ExcludedRoots: excludedRoots,
				ID:            results[len(results)-1].ID,
			})
			break
		}

		doc, parseErr := parseDocFile(data, candidate.ID)
		title := candidate.ID
		var description string
		if parseErr == nil {
			title = doc.Title
			description = doc.Description
		}
		item := docs.DocSearchResult{
			ID:             candidate.ID,
			Title:          title,
			Description:    description,
			Matches:        window.Matches,
			HasMoreMatches: window.HasMore,
		}
		if window.HasMore {
			item.NextMatchesCursor = exec.EncodeSearchCursor(docMatchCursor{
				Version:    docSearchCursorVersion,
				Query:      opts.Query,
				PathPrefix: pathPrefix,
				ID:         candidate.ID,
				Offset:     window.NextOffset,
			})
		}
		results = append(results, item)
	}

	return &exec.CursorResult[docs.DocSearchResult]{
		Items:      results,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}, nil
}

// SearchMatches returns cursor-based snippets for one document.
func (s *Store) SearchMatches(_ context.Context, id string, opts docs.SearchDocMatchesOptions) (*exec.CursorResult[*exec.Match], error) {
	if err := docs.ValidateDocID(id); err != nil {
		return nil, err
	}
	if opts.Query == "" {
		return &exec.CursorResult[*exec.Match]{Items: []*exec.Match{}}, nil
	}
	pathPrefix, err := cleanDocPathPrefix(opts.PathPrefix)
	if err != nil {
		return nil, err
	}

	cursor, err := decodeDocMatchCursor(opts.Cursor, opts.Query, pathPrefix, id)
	if err != nil {
		return nil, err
	}

	storedID, err := scopedDocID(pathPrefix, id)
	if err != nil {
		return nil, err
	}
	path, err := s.docFilePath(storedID)
	if err != nil {
		return nil, err
	}
	data, _, err := readRegularDocFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, docs.ErrDocNotFound
		}
		if errors.Is(err, docs.ErrDocNotFound) {
			return nil, docs.ErrDocNotFound
		}
		return nil, err
	}

	window, err := grep.GrepWindow(data, docSearchPattern(opts.Query), grep.GrepOptions{
		IsRegexp: true,
		Before:   grep.DefaultGrepOptions.Before,
		After:    grep.DefaultGrepOptions.After,
		Offset:   cursor.Offset,
		Limit:    max(opts.Limit, 1),
	})
	if err != nil {
		if errors.Is(err, grep.ErrNoMatch) {
			return &exec.CursorResult[*exec.Match]{Items: []*exec.Match{}}, nil
		}
		return nil, err
	}

	result := &exec.CursorResult[*exec.Match]{
		Items:   window.Matches,
		HasMore: window.HasMore,
	}
	if window.HasMore {
		result.NextCursor = exec.EncodeSearchCursor(docMatchCursor{
			Version:    docSearchCursorVersion,
			Query:      opts.Query,
			PathPrefix: pathPrefix,
			ID:         id,
			Offset:     window.NextOffset,
		})
	}
	return result, nil
}

// normalizeSortParams returns validated sort field and order with defaults.
func normalizeSortParams(sortField docs.DocSortField, sortOrder docs.DocSortOrder) (string, string) {
	sf := string(sortField)
	switch sf {
	case "name", "type", "mtime":
		// valid
	default:
		sf = "type"
	}
	so := string(sortOrder)
	switch so {
	case "asc", "desc":
		// valid
	default:
		so = "asc"
	}
	return sf, so
}

// buildTreeFromIndexLocked builds a tree of DocTreeNode from the cached index.
// s.mu must be held by the caller.
func (s *Store) buildTreeFromIndexLocked(pathPrefix, sortField, sortOrder string, excludedRoots []string) []*docs.DocTreeNode {
	dirNodes := make(map[string]*docs.DocTreeNode)
	dirEntries := make(map[string]docDirIndexEntry)
	var topLevel []*docs.DocTreeNode

	var ensureDirNode func(id string) *docs.DocTreeNode
	ensureDirNode = func(id string) *docs.DocTreeNode {
		if id == "" {
			return nil
		}
		if node, ok := dirNodes[id]; ok {
			return node
		}
		entry := dirEntries[id]
		node := &docs.DocTreeNode{
			ID:       id,
			Name:     filepath.Base(filepath.FromSlash(id)),
			Type:     "directory",
			Children: []*docs.DocTreeNode{},
			ModTime:  entry.ModTime,
		}
		dirNodes[id] = node
		parentID := parentDocID(id)
		if parentID == "" {
			topLevel = append(topLevel, node)
		} else {
			parent := ensureDirNode(parentID)
			parent.Children = append(parent.Children, node)
		}
		return node
	}

	dirIDs := make([]string, 0, len(s.dirs))
	for id := range s.dirs {
		if id != "" {
			dirIDs = append(dirIDs, id)
		}
	}
	sort.Strings(dirIDs)
	for _, fullID := range dirIDs {
		id, ok := relativeDocID(fullID, pathPrefix)
		if !ok || docPathRootExcluded(id, excludedRoots) {
			continue
		}
		if id != "" {
			dirEntries[id] = s.dirs[fullID]
			ensureDirNode(id)
		}
	}

	docIDs := make([]string, 0, len(s.docs))
	for id := range s.docs {
		docIDs = append(docIDs, id)
	}
	sort.Strings(docIDs)
	for _, fullID := range docIDs {
		doc := s.docs[fullID]
		id, ok := relativeDocID(fullID, pathPrefix)
		if !ok || docPathRootExcluded(id, excludedRoots) {
			continue
		}
		node := &docs.DocTreeNode{
			ID:      id,
			Name:    filepath.Base(filepath.FromSlash(id)) + ".md",
			Title:   doc.Title,
			Type:    "file",
			ModTime: doc.ModTime,
		}
		parentID := parentDocID(id)
		if parentID == "" {
			topLevel = append(topLevel, node)
			continue
		}
		parent := ensureDirNode(parentID)
		parent.Children = append(parent.Children, node)
	}

	if sortField == "mtime" {
		propagateModTime(topLevel)
	}
	sortTreeNodes(topLevel, sortField, sortOrder)
	return topLevel
}

// propagateModTime recursively sets each directory's ModTime to the max of
// its own ModTime and all descendant ModTimes.
func propagateModTime(nodes []*docs.DocTreeNode) time.Time {
	var maxTime time.Time
	for _, node := range nodes {
		t := node.ModTime
		if len(node.Children) > 0 {
			childMax := propagateModTime(node.Children)
			if childMax.After(t) {
				t = childMax
			}
			node.ModTime = t
		}
		if t.After(maxTime) {
			maxTime = t
		}
	}
	return maxTime
}

func compareNodeNames(a, b *docs.DocTreeNode) int {
	if cmp := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); cmp != 0 {
		return cmp
	}
	return strings.Compare(a.ID, b.ID)
}

func compareNodeModTime(a, b *docs.DocTreeNode) int {
	switch {
	case a.ModTime.Before(b.ModTime):
		return -1
	case a.ModTime.After(b.ModTime):
		return 1
	default:
		return compareNodeNames(a, b)
	}
}

func reverseCompare(cmp int) int {
	switch {
	case cmp < 0:
		return 1
	case cmp > 0:
		return -1
	default:
		return 0
	}
}

func compareTreeNodes(a, b *docs.DocTreeNode, sortField, sortOrder string) int {
	switch sortField {
	case "type":
		var cmp int
		switch a.Type {
		case b.Type:
			cmp = compareNodeNames(a, b)
		case "directory":
			cmp = -1
		default:
			cmp = 1
		}
		if sortOrder == "desc" {
			return reverseCompare(cmp)
		}
		return cmp
	case "mtime":
		if a.Type != b.Type {
			if a.Type == "directory" {
				return -1
			}
			return 1
		}
		if a.Type == "directory" {
			return compareNodeNames(a, b)
		}
		cmp := compareNodeModTime(a, b)
		if sortOrder == "desc" {
			return reverseCompare(cmp)
		}
		return cmp
	default: // "name"
		cmp := compareNodeNames(a, b)
		if sortOrder == "desc" {
			return reverseCompare(cmp)
		}
		return cmp
	}
}

// sortTreeNodes sorts nodes recursively according to the given sort field and order.
func sortTreeNodes(nodes []*docs.DocTreeNode, sortField, sortOrder string) {
	sort.Slice(nodes, func(i, j int) bool {
		return compareTreeNodes(nodes[i], nodes[j], sortField, sortOrder) < 0
	})
	for _, node := range nodes {
		if len(node.Children) > 0 {
			sortTreeNodes(node.Children, sortField, sortOrder)
		}
	}
}

// cleanEmptyParents removes empty parent directories up to baseDir.
func (s *Store) cleanEmptyParents(dir string) {
	for dir != s.baseDir && strings.HasPrefix(dir, s.baseDir) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := fileutil.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
