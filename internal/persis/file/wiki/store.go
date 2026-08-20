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
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	wikimodel "github.com/dagucloud/dagu/v2/internal/wiki"
	"github.com/goccy/go-yaml"
)

// Verify Store implements wikimodel.PageStore at compile time.
var _ wikimodel.PageStore = (*Store)(nil)

const (
	wikiDirPermissions      = 0750
	filePermissions         = 0600
	pageSearchCursorVersion = 1
	pageIndexCheckInterval  = 5 * time.Second
)

// pageFrontmatter holds the YAML fields in the page file frontmatter.
type pageFrontmatter struct {
	Title       string      `yaml:"title,omitempty"`
	Description string      `yaml:"description,omitempty"`
	Tags        pageTagList `yaml:"tags,omitempty"`
}

// pageTagList accepts either a YAML sequence of tags or a single
// comma-separated scalar. Unparseable values are ignored so that a malformed
// tags field never invalidates the rest of the frontmatter.
type pageTagList []string

func (t *pageTagList) UnmarshalYAML(data []byte) error {
	var list []string
	if err := yaml.Unmarshal(data, &list); err == nil {
		*t = normalizePageTags(list)
		return nil
	}
	var scalar string
	if err := yaml.Unmarshal(data, &scalar); err == nil {
		*t = normalizePageTags(strings.Split(scalar, ","))
		return nil
	}
	*t = nil
	return nil
}

// normalizePageTags trims whitespace, drops empties, and removes
// case-insensitive duplicates while preserving authored order and casing.
func normalizePageTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tags))
	normalized := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, tag)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// Store implements a file-based page store.
// Pages are stored as files: {baseDir}/{id}.md
// Each file contains optional YAML frontmatter (title, description, tags) and a Markdown body.
type Store struct {
	baseDir          string
	dataDir          string
	metadataFileName string
	legacyLayout     bool

	mutationMu         sync.Mutex
	mu                 sync.RWMutex
	indexBuilt         bool
	indexCheckedAt     time.Time
	indexCheckInterval time.Duration
	pages              map[string]pageIndexEntry
	dirs               map[string]pageDirIndexEntry
}

// Option configures a Store.
type Option func(*Store)

// WithDataDir sets the directory holding store sidecar data such as page
// revisions. Without it, revision snapshots are skipped and revision queries
// return empty results.
func WithDataDir(dir string) Option {
	return func(s *Store) {
		if dir == "" {
			return
		}
		s.dataDir = filepath.Clean(dir)
	}
}

// WithLegacyLayout keeps newly created hidden state in the legacy layout.
func WithLegacyLayout(legacy bool) Option {
	return func(s *Store) {
		s.legacyLayout = legacy
	}
}

type pageIndexEntry struct {
	ID          string
	RelPath     string
	AbsPath     string
	Title       string
	Description string
	Tags        []string
	OutLinks    []string
	ModTime     time.Time
	Size        int64
	Mode        os.FileMode
	Readable    bool
}

// outLinksFromContent returns the deduplicated wiki-link targets in content,
// anchors stripped, in first-seen order.
func outLinksFromContent(content string) []string {
	links := wikimodel.ExtractWikiLinks(content)
	if len(links) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(links))
	targets := make([]string, 0, len(links))
	for _, link := range links {
		if _, ok := seen[link.Target]; ok {
			continue
		}
		seen[link.Target] = struct{}{}
		targets = append(targets, link.Target)
	}
	return targets
}

type pageDirIndexEntry struct {
	ID      string
	AbsPath string
	ModTime time.Time
	Size    int64
	Mode    os.FileMode
}

// New creates a new file-based page store.
func New(baseDir string, opts ...Option) (*Store, error) {
	baseDir = filepath.Clean(baseDir)
	store := &Store{
		baseDir:            baseDir,
		indexCheckInterval: pageIndexCheckInterval,
		pages:              make(map[string]pageIndexEntry),
		dirs:               make(map[string]pageDirIndexEntry),
	}
	for _, opt := range opts {
		opt(store)
	}
	metadataFileName, err := resolveMetadataFileName(baseDir, store.legacyLayout)
	if err != nil {
		return nil, err
	}
	store.metadataFileName = metadataFileName
	if err := os.MkdirAll(baseDir, wikiDirPermissions); err != nil {
		return nil, fmt.Errorf("filewiki: create base directory %s: %w", baseDir, err)
	}
	return store, nil
}

// safePath validates that the given path stays within baseDir (preventing
// path traversal, including via symlinks) and returns the cleaned absolute path.
func (s *Store) safePath(p string, id string) (string, error) {
	cleaned := filepath.Clean(p)
	baseDir := filepath.Clean(s.baseDir)
	if !pathWithinDir(baseDir, cleaned) {
		return "", fmt.Errorf("filewiki: path traversal detected for id %q", id)
	}

	resolvedBase, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		return "", fmt.Errorf("filewiki: cannot resolve base dir: %w", err)
	}

	existing := filepath.Dir(cleaned)
	missing := []string{filepath.Base(cleaned)}
	for {
		_, statErr := os.Lstat(existing)
		if statErr == nil {
			resolved, resolveErr := filepath.EvalSymlinks(existing)
			if resolveErr != nil {
				return "", fmt.Errorf("filewiki: cannot resolve path for id %q: %w", id, resolveErr)
			}
			for _, m := range slices.Backward(missing) {
				resolved = filepath.Join(resolved, m)
			}
			if !pathWithinDir(resolvedBase, resolved) {
				return "", fmt.Errorf("filewiki: path traversal detected for id %q", id)
			}
			return cleaned, nil
		}
		if !os.IsNotExist(statErr) {
			return "", fmt.Errorf("filewiki: cannot inspect path for id %q: %w", id, statErr)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("filewiki: cannot resolve path for id %q", id)
		}
		missing = append(missing, filepath.Base(existing))
		existing = parent
	}
}

func pathWithinDir(baseDir, path string) bool {
	rel, err := filepath.Rel(baseDir, path)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// wikiPageFilePath returns the .md file path for a page ID with path-traversal validation.
func (s *Store) wikiPageFilePath(id string) (string, error) {
	return s.safePath(filepath.Join(s.baseDir, id+".md"), id)
}

func cleanPagePathPrefix(prefix string) (string, error) {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return "", nil
	}
	if err := wikimodel.ValidatePageID(prefix); err != nil {
		return "", err
	}
	return prefix, nil
}

func scopedPageID(prefix, id string) (string, error) {
	prefix, err := cleanPagePathPrefix(prefix)
	if err != nil {
		return "", err
	}
	if prefix == "" {
		return id, nil
	}
	if err := wikimodel.ValidatePageID(id); err != nil {
		return "", err
	}
	return prefix + "/" + id, nil
}

func joinPageID(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "/" + child
}

func parentPageID(id string) string {
	idx := strings.LastIndex(id, "/")
	if idx < 0 {
		return ""
	}
	return id[:idx]
}

func relativePageID(id, prefix string) (string, bool) {
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

func statRegularPageFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, wikimodel.ErrPageNotFound
	}
	return info, nil
}

func statPageDir(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, wikimodel.ErrPageNotFound
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

func (s *Store) pathExists(id string) (pageExists, directoryExists bool, err error) {
	filePath, err := s.wikiPageFilePath(id)
	if err != nil {
		return false, false, err
	}
	pageExists, err = pathExistsNoFollow(filePath)
	if err != nil {
		return false, false, err
	}
	dirPath, err := s.dirPath(id)
	if err != nil {
		return false, false, err
	}
	directoryExists, err = pathExistsNoFollow(dirPath)
	if err != nil {
		return false, false, err
	}
	return pageExists, directoryExists, nil
}

func (s *Store) ensureTargetAvailable(id string) error {
	pageExists, directoryExists, err := s.pathExists(id)
	if err != nil {
		return err
	}
	if pageExists || directoryExists {
		return wikimodel.ErrPageAlreadyExists
	}

	for parent := parentPageID(id); parent != ""; parent = parentPageID(parent) {
		parentFilePath, err := s.wikiPageFilePath(parent)
		if err != nil {
			return err
		}
		exists, err := pathExistsNoFollow(parentFilePath)
		if err != nil {
			return err
		}
		if exists {
			return wikimodel.ErrPagePathConflict
		}
	}
	return nil
}

func readRegularPageFile(path string) ([]byte, os.FileInfo, error) {
	initialInfo, err := statRegularPageFile(path)
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
		return nil, nil, wikimodel.ErrPageNotFound
	}
	currentInfo, err := statRegularPageFile(path)
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(initialInfo, currentInfo) || !os.SameFile(openedInfo, currentInfo) {
		return nil, nil, fmt.Errorf("filewiki: page changed while opening: %s", path)
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
	s.pages = make(map[string]pageIndexEntry)
	s.dirs = make(map[string]pageDirIndexEntry)

	info, err := os.Stat(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			s.indexBuilt = true
			return nil
		}
		return fmt.Errorf("filewiki: failed to access pages directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("filewiki: pages path %s is not a directory", s.baseDir)
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
			s.pages = make(map[string]pageIndexEntry)
			s.dirs = make(map[string]pageDirIndexEntry)
			return nil
		}
		return fmt.Errorf("filewiki: failed to access pages directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("filewiki: pages path %s is not a directory", s.baseDir)
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
		info, err := statPageDir(entry.AbsPath)
		if err != nil {
			if os.IsNotExist(err) {
				s.removeDirSubtreeLocked(id)
				continue
			}
			if errors.Is(err, wikimodel.ErrPageNotFound) {
				s.removeDirSubtreeLocked(id)
				continue
			}
			logger.Warn(ctx, "Skipping unreadable page directory", tag.File(entry.AbsPath), tag.Error(err))
			continue
		}
		s.recordDirLocked(id, entry.AbsPath, info)
		if err := s.scanDirLocked(ctx, id, entry.AbsPath, false); err != nil {
			return err
		}
	}

	pageIDs := make([]string, 0, len(s.pages))
	for id := range s.pages {
		pageIDs = append(pageIDs, id)
	}
	sort.Strings(pageIDs)
	for _, id := range pageIDs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		entry, ok := s.pages[id]
		if !ok {
			continue
		}
		info, err := statRegularPageFile(entry.AbsPath)
		if err != nil {
			if os.IsNotExist(err) {
				delete(s.pages, id)
				continue
			}
			if errors.Is(err, wikimodel.ErrPageNotFound) {
				delete(s.pages, id)
				continue
			}
			logger.Warn(ctx, "Skipping unreadable page file", tag.File(entry.RelPath), tag.Error(err))
			continue
		}
		if fingerprintsEqual(entry.ModTime, entry.Size, entry.Mode, info) {
			continue
		}
		if err := s.upsertWikiPageLocked(ctx, id, entry.AbsPath, info); err != nil {
			logger.Warn(ctx, "Skipping page with changed metadata", tag.File(entry.RelPath), tag.Error(err))
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
			logger.Warn(ctx, "Skipping unreadable page directory", tag.File(absPath), tag.Error(err))
			return nil
		}
		return fmt.Errorf("filewiki: failed to read pages directory %s: %w", absPath, err)
	}

	seenPages := make(map[string]struct{})
	seenDirs := make(map[string]struct{})
	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		name := entry.Name()
		childAbsPath := filepath.Join(absPath, name)
		info, err := entry.Info()
		if err != nil {
			logger.Warn(ctx, "Skipping unreadable page path", tag.File(childAbsPath), tag.Error(err))
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.IsDir() {
			childID := joinPageID(dirID, name)
			if err := wikimodel.ValidatePageID(childID); err != nil {
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

		childID := joinPageID(dirID, strings.TrimSuffix(name, ".md"))
		relPath := filepath.ToSlash(joinPageID(dirID, name))
		if err := wikimodel.ValidatePageID(childID); err != nil {
			logger.Debug(ctx, "Skipping non-conforming page file", tag.File(relPath), tag.Reason(err.Error()))
			continue
		}

		if !info.Mode().IsRegular() {
			continue
		}
		seenPages[childID] = struct{}{}
		current, exists := s.pages[childID]
		if exists && fingerprintsEqual(current.ModTime, current.Size, current.Mode, info) {
			continue
		}
		if err := s.upsertWikiPageLocked(ctx, childID, childAbsPath, info); err != nil {
			logger.Warn(ctx, "Skipping page file", tag.File(relPath), tag.Error(err))
		}
	}

	for id := range s.pages {
		if parentPageID(id) != dirID {
			continue
		}
		if _, ok := seenPages[id]; !ok {
			delete(s.pages, id)
		}
	}
	for id := range s.dirs {
		if id == "" || parentPageID(id) != dirID {
			continue
		}
		if _, ok := seenDirs[id]; !ok {
			s.removeDirSubtreeLocked(id)
		}
	}

	return nil
}

func (s *Store) recordDirLocked(id, absPath string, info os.FileInfo) {
	s.dirs[id] = pageDirIndexEntry{
		ID:      id,
		AbsPath: absPath,
		ModTime: info.ModTime(),
		Size:    info.Size(),
		Mode:    info.Mode(),
	}
}

func (s *Store) upsertWikiPageLocked(ctx context.Context, id, absPath string, info os.FileInfo) error {
	title := titleFromID(id)
	var description string
	var tags []string
	var outLinks []string
	readable := false
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return wikimodel.ErrPageNotFound
	}
	data, readInfo, err := readRegularPageFile(absPath)
	if err == nil {
		info = readInfo
		page, parseErr := parsePageFile(data, id)
		if parseErr != nil {
			return parseErr
		}
		title = page.Title
		description = page.Description
		tags = page.Tags
		outLinks = outLinksFromContent(page.Content)
		readable = true
	} else if errors.Is(err, wikimodel.ErrPageNotFound) {
		return err
	}
	s.pages[id] = pageIndexEntry{
		ID:          id,
		RelPath:     filepath.ToSlash(id + ".md"),
		AbsPath:     absPath,
		Title:       title,
		Description: description,
		Tags:        tags,
		OutLinks:    outLinks,
		ModTime:     info.ModTime(),
		Size:        info.Size(),
		Mode:        info.Mode(),
		Readable:    readable,
	}
	return ctx.Err()
}

func (s *Store) upsertPageIndexAfterMutation(ctx context.Context, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.indexBuilt {
		return
	}
	filePath, err := s.wikiPageFilePath(id)
	if err != nil {
		logger.Warn(ctx, "Failed to update page index", tag.File(id), tag.Error(err))
		return
	}
	info, err := statRegularPageFile(filePath)
	if err != nil {
		logger.Warn(ctx, "Failed to stat page for index update", tag.File(id), tag.Error(err))
		return
	}
	if err := s.upsertWikiPageLocked(ctx, id, filePath, info); err != nil {
		logger.Warn(ctx, "Failed to update page index", tag.File(id), tag.Error(err))
		return
	}
	s.recordParentDirsLocked(ctx, id)
	s.markIndexCheckedLocked()
}

func (s *Store) removePageIndexAfterDelete(ctx context.Context, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.indexBuilt {
		return
	}
	delete(s.pages, id)
	s.pruneMissingParentsLocked(ctx, parentPageID(id))
	s.markIndexCheckedLocked()
}

func (s *Store) removeDirIndexAfterDelete(ctx context.Context, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.indexBuilt {
		return
	}
	s.removeDirSubtreeLocked(id)
	s.pruneMissingParentsLocked(ctx, parentPageID(id))
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
		logger.Warn(ctx, "Failed to rebuild page index", tag.Error(err))
		return
	}
	s.markIndexCheckedLocked()
}

func (s *Store) removeDirSubtreeLocked(id string) {
	delete(s.dirs, id)
	prefix := id + "/"
	for pageID := range s.pages {
		if strings.HasPrefix(pageID, prefix) {
			delete(s.pages, pageID)
		}
	}
	for dirID := range s.dirs {
		if strings.HasPrefix(dirID, prefix) {
			delete(s.dirs, dirID)
		}
	}
}

func (s *Store) recordParentDirsLocked(ctx context.Context, id string) {
	parent := parentPageID(id)
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
		parent = parentPageID(parent)
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
		id = parentPageID(id)
	}
	if info, err := os.Stat(s.baseDir); err == nil && info.IsDir() {
		s.recordDirLocked("", s.baseDir, info)
	}
}

// parsePageFile parses a page .md file into an wikimodel.Page.
// The file format is optional YAML frontmatter between --- delimiters, followed by markdown body.
// Content always contains the full file (including frontmatter); frontmatter is parsed to extract title and description.
func parsePageFile(data []byte, id string) (*wikimodel.Page, error) {
	content := string(data)
	parsedContent := strings.ReplaceAll(content, "\r\n", "\n")

	var title string
	var description string
	var tags []string

	if strings.HasPrefix(parsedContent, "---\n") {
		rest := parsedContent[4:]

		closingIdx := strings.Index(rest, "\n---\n")
		if closingIdx == -1 {
			if strings.HasSuffix(rest, "\n---") {
				closingIdx = len(rest) - 4
			}
		}

		if closingIdx >= 0 {
			frontmatterStr := rest[:closingIdx]

			var fm pageFrontmatter
			if err := yaml.Unmarshal([]byte(frontmatterStr), &fm); err == nil {
				title = fm.Title
				description = fm.Description
				tags = fm.Tags
			}
		}
	}

	if title == "" {
		title = titleFromID(id)
	}

	return &wikimodel.Page{
		ID:          id,
		Title:       title,
		Description: description,
		Tags:        tags,
		Content:     content,
	}, nil
}

// titleFromID derives a display title from a page ID.
// E.g., "pages/deploy-guide" → "deploy-guide"
func titleFromID(id string) string {
	parts := strings.Split(id, "/")
	return parts[len(parts)-1]
}

// List returns a paginated tree of page nodes.
func (s *Store) List(ctx context.Context, opts wikimodel.ListPagesOptions) (*pagination.PaginatedResult[*wikimodel.PageTreeNode], error) {
	sortField, sortOrder := normalizeSortParams(opts.Sort, opts.Order)
	pathPrefix, err := cleanPagePathPrefix(opts.PathPrefix)
	if err != nil {
		return nil, err
	}
	if err := s.ensureFreshIndex(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	tree := s.buildTreeFromIndexLocked(pathPrefix, sortField, sortOrder, opts.ExcludePathRoots)
	s.mu.RUnlock()

	pg := pagination.NewPaginator(opts.Page, opts.PerPage)
	total := len(tree)
	offset := min(pg.Offset(), total)
	end := min(offset+pg.Limit(), total)
	pageItems := tree[offset:end]

	result := pagination.NewPaginatedResult(pageItems, total, pg)
	return &result, nil
}

// flatWikiPageItem is an intermediate struct for flat listing with sort support.
type flatWikiPageItem struct {
	meta wikimodel.PageMetadata
}

// ListFlat returns a paginated flat list of page metadata.
func (s *Store) ListFlat(ctx context.Context, opts wikimodel.ListPagesOptions) (*pagination.PaginatedResult[wikimodel.PageMetadata], error) {
	sortField, sortOrder := normalizeSortParams(opts.Sort, opts.Order)
	pathPrefix, err := cleanPagePathPrefix(opts.PathPrefix)
	if err != nil {
		return nil, err
	}
	if err := s.ensureFreshIndex(ctx); err != nil {
		return nil, err
	}

	tagFilter := normalizePageTagFilter(opts.Tags)

	s.mu.RLock()
	items := make([]flatWikiPageItem, 0, len(s.pages))
	for _, page := range s.pages {
		if !page.Readable || wikiPagePathRootExcluded(page.ID, opts.ExcludePathRoots) {
			continue
		}
		if !wikiPageTagsMatch(page.Tags, tagFilter) {
			continue
		}
		id, ok := relativePageID(page.ID, pathPrefix)
		if !ok {
			continue
		}
		items = append(items, flatWikiPageItem{
			meta: wikimodel.PageMetadata{
				ID:          id,
				Title:       page.Title,
				Description: page.Description,
				Tags:        page.Tags,
				ModTime:     page.ModTime,
			},
		})
	}
	s.mu.RUnlock()

	sort.Slice(items, func(i, j int) bool {
		var cmp int
		switch sortField {
		case "mtime":
			switch {
			case items[i].meta.ModTime.Before(items[j].meta.ModTime):
				cmp = -1
			case items[i].meta.ModTime.After(items[j].meta.ModTime):
				cmp = 1
			default:
				cmp = strings.Compare(items[i].meta.ID, items[j].meta.ID)
			}
		case "type":
			cmp = strings.Compare(items[i].meta.ID, items[j].meta.ID)
		default: // "name"
			cmp = strings.Compare(strings.ToLower(items[i].meta.ID), strings.ToLower(items[j].meta.ID))
			if cmp == 0 {
				cmp = strings.Compare(items[i].meta.ID, items[j].meta.ID)
			}
		}
		if sortOrder == "desc" {
			return cmp > 0
		}
		return cmp < 0
	})

	metadata := make([]wikimodel.PageMetadata, len(items))
	for i, item := range items {
		metadata[i] = item.meta
	}

	pg := pagination.NewPaginator(opts.Page, opts.PerPage)
	total := len(metadata)
	offset := min(pg.Offset(), total)
	end := min(offset+pg.Limit(), total)
	pageItems := metadata[offset:end]

	result := pagination.NewPaginatedResult(pageItems, total, pg)
	return &result, nil
}

func wikiPagePathRootExcluded(id string, excludedRoots []string) bool {
	if len(excludedRoots) == 0 {
		return false
	}
	root, _, _ := strings.Cut(id, "/")
	return slices.Contains(excludedRoots, root)
}

// normalizePageTagFilter lowercases, sorts, and dedupes a requested tag
// filter so it can be matched and embedded in cursors deterministically.
func normalizePageTagFilter(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag != "" {
			normalized = append(normalized, tag)
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	sort.Strings(normalized)
	return slices.Compact(normalized)
}

// wikiPageTagsMatch reports whether every tag in filter (already lowercased) is
// present in wikiPageTags, compared case-insensitively.
func wikiPageTagsMatch(wikiPageTags, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	if len(wikiPageTags) == 0 {
		return false
	}
	for _, want := range filter {
		found := false
		for _, tag := range wikiPageTags {
			if strings.ToLower(tag) == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Get retrieves a page by its ID.
func (s *Store) Get(_ context.Context, id string) (*wikimodel.Page, error) {
	if err := wikimodel.ValidatePageID(id); err != nil {
		return nil, err
	}

	filePath, err := s.wikiPageFilePath(id)
	if err != nil {
		return nil, err
	}

	data, info, err := readRegularPageFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, wikimodel.ErrPageNotFound
		}
		if errors.Is(err, wikimodel.ErrPageNotFound) {
			return nil, wikimodel.ErrPageNotFound
		}
		return nil, fmt.Errorf("filewiki: failed to read file %s: %w", filePath, err)
	}

	page, err := parsePageFile(data, id)
	if err != nil {
		return nil, fmt.Errorf("filewiki: failed to parse page %s: %w", id, err)
	}

	page.CreatedAt = s.pageCreatedAt(id, filePath, info)
	page.UpdatedAt = info.ModTime().UTC().Format(time.RFC3339)

	return page, nil
}

// Create creates a new page file.
func (s *Store) Create(ctx context.Context, id, content string) error {
	if err := wikimodel.ValidatePageID(id); err != nil {
		return err
	}

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	filePath, err := s.wikiPageFilePath(id)
	if err != nil {
		return err
	}
	if err := s.ensureTargetAvailable(id); err != nil {
		return err
	}

	// Ensure parent directories exist.
	if err := os.MkdirAll(filepath.Dir(filePath), wikiDirPermissions); err != nil {
		return fmt.Errorf("filewiki: failed to create parent directories: %w", err)
	}

	data := []byte(content)

	// Use O_EXCL for atomic create — prevents race between concurrent creates.
	f, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePermissions) //nolint:gosec // filePath is validated by wikiPageFilePath
	if err != nil {
		if os.IsExist(err) {
			return wikimodel.ErrPageAlreadyExists
		}
		return fmt.Errorf("filewiki: failed to create file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("filewiki: failed to write file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("filewiki: failed to close file: %w", err)
	}
	if err := s.setPageCreatedAt(id, createdAtNow()); err != nil {
		logger.Warn(ctx, "Failed to record page metadata", tag.File(filePath), tag.Error(err))
	}

	s.upsertPageIndexAfterMutation(ctx, id)
	return nil
}

// Update modifies an existing page file.
func (s *Store) Update(ctx context.Context, id, content string) error {
	if err := wikimodel.ValidatePageID(id); err != nil {
		return err
	}

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	filePath, err := s.wikiPageFilePath(id)
	if err != nil {
		return err
	}

	info, err := statRegularPageFile(filePath)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, wikimodel.ErrPageNotFound) {
			return wikimodel.ErrPageNotFound
		}
		return fmt.Errorf("filewiki: failed to stat file %s: %w", filePath, err)
	}
	createdAt := s.pageCreatedAt(id, filePath, info)

	data := []byte(content)
	if prior, _, readErr := readRegularPageFile(filePath); readErr == nil {
		if err := s.snapshotRevision(id, prior, data); err != nil {
			logger.Warn(ctx, "Failed to snapshot page revision", tag.File(filePath), tag.Error(err))
		}
	}
	if err := fileutil.WriteFileAtomic(filePath, data, filePermissions); err != nil {
		return fmt.Errorf("filewiki: failed to write file: %w", err)
	}
	if err := s.setPageCreatedAt(id, createdAt); err != nil {
		logger.Warn(ctx, "Failed to preserve page metadata", tag.File(filePath), tag.Error(err))
	}

	s.upsertPageIndexAfterMutation(ctx, id)
	return nil
}

// Delete removes a page file or directory and cleans up empty parent directories.
func (s *Store) Delete(ctx context.Context, id string) error {
	if err := wikimodel.ValidatePageID(id); err != nil {
		return err
	}

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	filePath, err := s.wikiPageFilePath(id)
	if err != nil {
		return err
	}
	dirPath, err := s.dirPath(id)
	if err != nil {
		return err
	}
	_, fileErr := statRegularPageFile(filePath)
	_, dirErr := statPageDir(dirPath)
	if fileErr == nil && dirErr == nil {
		return wikimodel.ErrPagePathConflict
	}
	if fileErr == nil {
		if err := fileutil.Remove(filePath); err != nil {
			return fmt.Errorf("filewiki: failed to delete file: %w", err)
		}
		if err := s.deletePageCreatedAt(id); err != nil {
			logger.Warn(ctx, "Failed to remove page metadata", tag.File(filePath), tag.Error(err))
		}
		if err := s.deleteRevisions(id); err != nil {
			logger.Warn(ctx, "Failed to remove page revisions", tag.File(filePath), tag.Error(err))
		}
		if err := s.deleteAttachments(id); err != nil {
			logger.Warn(ctx, "Failed to remove page attachments", tag.File(filePath), tag.Error(err))
		}
		s.cleanEmptyParents(filepath.Dir(filePath))
		s.removePageIndexAfterDelete(ctx, id)
		return nil
	}
	if fileErr != nil && !os.IsNotExist(fileErr) && !errors.Is(fileErr, wikimodel.ErrPageNotFound) {
		return fmt.Errorf("filewiki: failed to stat file %s: %w", filePath, fileErr)
	}
	if dirErr != nil {
		if !os.IsNotExist(dirErr) && !errors.Is(dirErr, wikimodel.ErrPageNotFound) {
			return fmt.Errorf("filewiki: failed to stat directory %s: %w", dirPath, dirErr)
		}
		return wikimodel.ErrPageNotFound
	}
	if err := s.safeDeleteDir(dirPath); err != nil {
		return fmt.Errorf("filewiki: failed to delete directory: %w", err)
	}
	if err := s.deletePageCreatedAtPrefix(id); err != nil {
		logger.Warn(ctx, "Failed to remove page metadata", tag.File(dirPath), tag.Error(err))
	}
	if err := s.deleteRevisionsPrefix(id); err != nil {
		logger.Warn(ctx, "Failed to remove page revisions", tag.File(dirPath), tag.Error(err))
	}
	if err := s.deleteAttachmentsPrefix(id); err != nil {
		logger.Warn(ctx, "Failed to remove page attachments", tag.File(dirPath), tag.Error(err))
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

// DeleteBatch deletes multiple pages/directories in one operation.
// Not-found items are treated as success (idempotency for safe retries).
func (s *Store) DeleteBatch(ctx context.Context, ids []string) ([]string, []wikimodel.DeleteError, error) {
	var deleted []string
	var failed []wikimodel.DeleteError

	// Validate all IDs upfront, separate valid from invalid.
	validIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if err := wikimodel.ValidatePageID(id); err != nil {
			failed = append(failed, wikimodel.DeleteError{ID: id, Error: err.Error()})
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

		filePath, err := s.wikiPageFilePath(id)
		if err != nil {
			failed = append(failed, wikimodel.DeleteError{ID: id, Error: err.Error()})
			continue
		}
		dirPath, err := s.dirPath(id)
		if err != nil {
			failed = append(failed, wikimodel.DeleteError{ID: id, Error: err.Error()})
			continue
		}
		_, fileErr := statRegularPageFile(filePath)
		_, dirErr := statPageDir(dirPath)
		if fileErr == nil && dirErr == nil {
			failed = append(failed, wikimodel.DeleteError{ID: id, Error: wikimodel.ErrPagePathConflict.Error()})
			continue
		}
		if fileErr == nil {
			if err := fileutil.Remove(filePath); err != nil {
				failed = append(failed, wikimodel.DeleteError{ID: id, Error: err.Error()})
				continue
			}
			if err := s.deletePageCreatedAt(id); err != nil {
				logger.Warn(ctx, "Failed to remove page metadata", tag.File(filePath), tag.Error(err))
			}
			if err := s.deleteRevisions(id); err != nil {
				logger.Warn(ctx, "Failed to remove page revisions", tag.File(filePath), tag.Error(err))
			}
			if err := s.deleteAttachments(id); err != nil {
				logger.Warn(ctx, "Failed to remove page attachments", tag.File(filePath), tag.Error(err))
			}
			s.cleanEmptyParents(filepath.Dir(filePath))
			s.removePageIndexAfterDelete(ctx, id)
			deleted = append(deleted, id)
			continue
		}
		if !os.IsNotExist(fileErr) && !errors.Is(fileErr, wikimodel.ErrPageNotFound) {
			failed = append(failed, wikimodel.DeleteError{ID: id, Error: fileErr.Error()})
			continue
		}

		if os.IsNotExist(dirErr) || errors.Is(dirErr, wikimodel.ErrPageNotFound) {
			// Not found → treat as success (idempotency).
			s.removePageIndexAfterDelete(ctx, id)
			s.removeDirIndexAfterDelete(ctx, id)
			deleted = append(deleted, id)
			continue
		}
		if dirErr != nil {
			failed = append(failed, wikimodel.DeleteError{ID: id, Error: dirErr.Error()})
			continue
		}
		if err := s.safeDeleteDir(dirPath); err != nil {
			failed = append(failed, wikimodel.DeleteError{ID: id, Error: err.Error()})
			continue
		}
		if err := s.deletePageCreatedAtPrefix(id); err != nil {
			logger.Warn(ctx, "Failed to remove page metadata", tag.File(dirPath), tag.Error(err))
		}
		if err := s.deleteRevisionsPrefix(id); err != nil {
			logger.Warn(ctx, "Failed to remove page revisions", tag.File(dirPath), tag.Error(err))
		}
		if err := s.deleteAttachmentsPrefix(id); err != nil {
			logger.Warn(ctx, "Failed to remove page attachments", tag.File(dirPath), tag.Error(err))
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

// dirPath returns the directory path for a page ID with path-traversal validation.
func (s *Store) dirPath(id string) (string, error) {
	return s.safePath(filepath.Join(s.baseDir, id), id)
}

// PathExists reports whether id identifies a page or a page directory.
func (s *Store) PathExists(_ context.Context, id string) (pageExists, directoryExists bool, err error) {
	if err := wikimodel.ValidatePageID(id); err != nil {
		return false, false, err
	}

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	return s.pathExists(id)
}

// Rename moves a page or every page under a directory path.
func (s *Store) Rename(ctx context.Context, oldID, newID string) error {
	if err := wikimodel.ValidatePageID(oldID); err != nil {
		return err
	}
	if err := wikimodel.ValidatePageID(newID); err != nil {
		return err
	}

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	oldFilePath, err := s.wikiPageFilePath(oldID)
	if err != nil {
		return err
	}
	oldDirPath, err := s.dirPath(oldID)
	if err != nil {
		return err
	}
	_, fileErr := statRegularPageFile(oldFilePath)
	_, dirErr := statPageDir(oldDirPath)
	if fileErr == nil && dirErr == nil {
		return wikimodel.ErrPagePathConflict
	}
	if fileErr == nil {
		return s.renameFileLocked(ctx, oldID, newID, oldFilePath)
	}
	if fileErr != nil && !os.IsNotExist(fileErr) && !errors.Is(fileErr, wikimodel.ErrPageNotFound) {
		return fileErr
	}
	if dirErr == nil {
		return s.renameDirectoryLocked(ctx, oldID, newID, oldDirPath)
	}
	if dirErr != nil && !os.IsNotExist(dirErr) && !errors.Is(dirErr, wikimodel.ErrPageNotFound) {
		return dirErr
	}
	return wikimodel.ErrPageNotFound
}

func (s *Store) renameFileLocked(ctx context.Context, oldID, newID, oldFilePath string) error {
	if err := s.ensureTargetAvailable(newID); err != nil {
		return err
	}
	newFilePath, err := s.wikiPageFilePath(newID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(newFilePath), wikiDirPermissions); err != nil {
		return fmt.Errorf("filewiki: failed to create target directories: %w", err)
	}
	if err := renameNoReplace(oldFilePath, newFilePath); err != nil {
		if errors.Is(err, wikimodel.ErrPageAlreadyExists) {
			return wikimodel.ErrPageAlreadyExists
		}
		return fmt.Errorf("filewiki: failed to rename file: %w", err)
	}
	if err := s.renamePageCreatedAt(oldID, newID); err != nil {
		logger.Warn(ctx, "Failed to rename page metadata", tag.File(newFilePath), tag.Error(err))
	}
	if err := s.renameRevisions(oldID, newID); err != nil {
		logger.Warn(ctx, "Failed to rename page revisions", tag.File(newFilePath), tag.Error(err))
	}
	if err := s.renameAttachments(oldID, newID); err != nil {
		logger.Warn(ctx, "Failed to rename page attachments", tag.File(newFilePath), tag.Error(err))
	}
	s.cleanEmptyParents(filepath.Dir(oldFilePath))
	s.removePageIndexAfterDelete(ctx, oldID)
	s.upsertPageIndexAfterMutation(ctx, newID)
	return nil
}

func (s *Store) renameDirectoryLocked(ctx context.Context, oldID, newID, oldDirPath string) error {
	if newID == oldID || strings.HasPrefix(newID, oldID+"/") {
		return wikimodel.ErrPagePathConflict
	}
	if err := s.ensureTargetAvailable(newID); err != nil {
		return err
	}
	newDirPath, err := s.dirPath(newID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(newDirPath), wikiDirPermissions); err != nil {
		return fmt.Errorf("filewiki: failed to create target directories: %w", err)
	}
	if err := renameNoReplace(oldDirPath, newDirPath); err != nil {
		if errors.Is(err, wikimodel.ErrPageAlreadyExists) {
			return wikimodel.ErrPageAlreadyExists
		}
		return fmt.Errorf("filewiki: failed to rename directory: %w", err)
	}
	if err := s.renamePageCreatedAtPrefix(oldID, newID); err != nil {
		logger.Warn(ctx, "Failed to rename page metadata", tag.File(newDirPath), tag.Error(err))
	}
	if err := s.renameRevisionsPrefix(oldID, newID); err != nil {
		logger.Warn(ctx, "Failed to rename page revisions", tag.File(newDirPath), tag.Error(err))
	}
	if err := s.renameAttachmentsPrefix(oldID, newID); err != nil {
		logger.Warn(ctx, "Failed to rename page attachments", tag.File(newDirPath), tag.Error(err))
	}
	s.cleanEmptyParents(filepath.Dir(oldDirPath))
	s.rebuildIndexAfterMutation(ctx)
	return nil
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
