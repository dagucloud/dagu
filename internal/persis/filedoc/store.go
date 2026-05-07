// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package filedoc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/filedag/grep"
	"github.com/goccy/go-yaml"
)

// Verify Store implements agent.DocStore at compile time.
var _ agent.DocStore = (*Store)(nil)

const (
	docDirPermissions      = 0750
	filePermissions        = 0600
	docSearchCursorVersion = 1
)

// docFrontmatter holds the YAML fields in the doc file frontmatter.
type docFrontmatter struct {
	Title       string `yaml:"title,omitempty"`
	Description string `yaml:"description,omitempty"`
}

// Store implements a file-based doc store.
// Docs are stored as files: {baseDir}/{id}.md
// Each file contains optional YAML frontmatter (title, description) and a Markdown body.
// Unlike the soul store, this has no cached index — it scans the filesystem
// on every call, following the DAG store pattern.
type Store struct {
	baseDir string
}

// New creates a new file-based doc store.
func New(baseDir string) *Store {
	_ = os.MkdirAll(baseDir, docDirPermissions) // best effort
	return &Store{baseDir: baseDir}
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
	if err := agent.ValidateDocID(prefix); err != nil {
		return "", err
	}
	return prefix, nil
}

func (s *Store) scopedRoot(prefix string) (string, error) {
	prefix, err := cleanDocPathPrefix(prefix)
	if err != nil {
		return "", err
	}
	if prefix == "" {
		return s.baseDir, nil
	}
	return s.safePath(filepath.Join(s.baseDir, prefix), prefix)
}

func scopedDocID(prefix, id string) (string, error) {
	prefix, err := cleanDocPathPrefix(prefix)
	if err != nil {
		return "", err
	}
	if prefix == "" {
		return id, nil
	}
	if err := agent.ValidateDocID(id); err != nil {
		return "", err
	}
	return prefix + "/" + id, nil
}

// parseDocFile parses a doc .md file into an agent.Doc.
// The file format is optional YAML frontmatter between --- delimiters, followed by markdown body.
// Content always contains the full file (including frontmatter); frontmatter is parsed to extract title and description.
func parseDocFile(data []byte, id string) (*agent.Doc, error) {
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

	return &agent.Doc{
		ID:          id,
		Title:       title,
		Description: description,
		Content:     content,
	}, nil
}

// titleFromID derives a display title from a doc ID.
// E.g., "runbooks/deploy-guide" → "deploy-guide"
func titleFromID(id string) string {
	parts := strings.Split(id, "/")
	return parts[len(parts)-1]
}

// List returns a paginated tree of doc nodes.
func (s *Store) List(ctx context.Context, opts agent.ListDocsOptions) (*exec.PaginatedResult[*agent.DocTreeNode], error) {
	sortField, sortOrder := normalizeSortParams(opts.Sort, opts.Order)
	rootDir, err := s.scopedRoot(opts.PathPrefix)
	if err != nil {
		return nil, err
	}
	tree, err := s.buildTree(ctx, rootDir, sortField, sortOrder, opts.ExcludePathRoots)
	if err != nil {
		return nil, err
	}

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
	meta agent.DocMetadata
}

// ListFlat returns a paginated flat list of doc metadata.
func (s *Store) ListFlat(ctx context.Context, opts agent.ListDocsOptions) (*exec.PaginatedResult[agent.DocMetadata], error) {
	sortField, sortOrder := normalizeSortParams(opts.Sort, opts.Order)
	rootDir, err := s.scopedRoot(opts.PathPrefix)
	if err != nil {
		return nil, err
	}
	if info, statErr := os.Stat(rootDir); statErr != nil {
		if os.IsNotExist(statErr) {
			pg := exec.NewPaginator(opts.Page, opts.PerPage)
			result := exec.NewPaginatedResult([]agent.DocMetadata{}, 0, pg)
			return &result, nil
		}
		return nil, fmt.Errorf("filedoc: failed to access docs directory: %w", statErr)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("filedoc: docs path %s is not a directory", rootDir)
	}

	var items []flatDocItem

	needMtime := sortField == "mtime"

	err = filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}

		relPath, err := filepath.Rel(rootDir, path)
		if err != nil {
			return nil
		}
		id := strings.TrimSuffix(filepath.ToSlash(relPath), ".md")

		if err := agent.ValidateDocID(id); err != nil {
			logger.Debug(ctx, "Skipping non-conforming doc file", tag.File(relPath), tag.Reason(err.Error()))
			return nil
		}
		if docPathRootExcluded(id, opts.ExcludePathRoots) {
			return nil
		}

		data, err := os.ReadFile(path) //nolint:gosec // path constructed from baseDir
		if err != nil {
			return nil
		}

		doc, err := parseDocFile(data, id)
		if err != nil {
			return nil
		}

		var modTime time.Time
		if needMtime {
			if info, infoErr := d.Info(); infoErr == nil {
				modTime = info.ModTime()
			}
		}

		items = append(items, flatDocItem{
			meta: agent.DocMetadata{ID: doc.ID, Title: doc.Title, Description: doc.Description, ModTime: modTime},
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("filedoc: failed to walk directory: %w", err)
	}

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

	metadata := make([]agent.DocMetadata, len(items))
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
func (s *Store) Get(_ context.Context, id string) (*agent.Doc, error) {
	if err := agent.ValidateDocID(id); err != nil {
		return nil, err
	}

	filePath, err := s.docFilePath(id)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(filePath) //nolint:gosec // path validated by docFilePath
	if err != nil {
		if os.IsNotExist(err) {
			return nil, agent.ErrDocNotFound
		}
		return nil, fmt.Errorf("filedoc: failed to read file %s: %w", filePath, err)
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("filedoc: failed to stat file %s: %w", filePath, err)
	}

	doc, err := parseDocFile(data, id)
	if err != nil {
		return nil, fmt.Errorf("filedoc: failed to parse doc %s: %w", id, err)
	}

	doc.FilePath = filePath
	doc.CreatedAt = fileCreationTime(info).UTC().Format(time.RFC3339)
	doc.UpdatedAt = info.ModTime().UTC().Format(time.RFC3339)

	return doc, nil
}

// Create creates a new doc file.
func (s *Store) Create(_ context.Context, id, content string) error {
	if err := agent.ValidateDocID(id); err != nil {
		return err
	}

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
			return agent.ErrDocAlreadyExists
		}
		return fmt.Errorf("filedoc: failed to create file: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("filedoc: failed to write file: %w", err)
	}

	return nil
}

// Update modifies an existing doc file.
func (s *Store) Update(_ context.Context, id, content string) error {
	if err := agent.ValidateDocID(id); err != nil {
		return err
	}

	filePath, err := s.docFilePath(id)
	if err != nil {
		return err
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return agent.ErrDocNotFound
	}

	data := []byte(content)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	if err := fileutil.WriteFileAtomic(filePath, data, filePermissions); err != nil {
		return fmt.Errorf("filedoc: failed to write file: %w", err)
	}

	return nil
}

// Delete removes a doc file or directory and cleans up empty parent directories.
// File takes precedence: if both foo.md and foo/ exist, Delete("foo") deletes the file.
func (s *Store) Delete(_ context.Context, id string) error {
	if err := agent.ValidateDocID(id); err != nil {
		return err
	}

	// Try as file first (existing behavior).
	filePath, err := s.docFilePath(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filePath); err == nil {
		if err := os.Remove(filePath); err != nil {
			return fmt.Errorf("filedoc: failed to delete file: %w", err)
		}
		s.cleanEmptyParents(filepath.Dir(filePath))
		return nil
	}

	// Try as directory.
	dirPath, err := s.dirPath(id)
	if err != nil {
		return err
	}
	info, err := os.Stat(dirPath)
	if err != nil || !info.IsDir() {
		return agent.ErrDocNotFound
	}
	if err := s.safeDeleteDir(dirPath); err != nil {
		return fmt.Errorf("filedoc: failed to delete directory: %w", err)
	}
	s.cleanEmptyParents(filepath.Dir(dirPath))
	return nil
}

// safeDeleteDir removes a directory tree safely without using os.RemoveAll.
// It walks depth-first and uses os.Remove for each entry, which never follows
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
		// os.Remove: deletes file/symlink/empty-dir. Never follows symlinks.
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			lastErr = err
		}
	}
	return lastErr
}

// DeleteBatch deletes multiple docs/directories in one operation.
// Not-found items are treated as success (idempotency for safe retries).
func (s *Store) DeleteBatch(_ context.Context, ids []string) ([]string, []agent.DeleteError, error) {
	var deleted []string
	var failed []agent.DeleteError

	// Validate all IDs upfront, separate valid from invalid.
	validIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if err := agent.ValidateDocID(id); err != nil {
			failed = append(failed, agent.DeleteError{ID: id, Error: err.Error()})
		} else {
			validIDs = append(validIDs, id)
		}
	}

	// Sort shortest-first for parent-before-child deduplication.
	sort.Slice(validIDs, func(i, j int) bool { return len(validIDs[i]) < len(validIDs[j]) })

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
			failed = append(failed, agent.DeleteError{ID: id, Error: err.Error()})
			continue
		}
		if _, err := os.Stat(filePath); err == nil {
			if err := os.Remove(filePath); err != nil {
				failed = append(failed, agent.DeleteError{ID: id, Error: err.Error()})
				continue
			}
			s.cleanEmptyParents(filepath.Dir(filePath))
			deleted = append(deleted, id)
			continue
		}

		// Try as directory.
		dirPath, err := s.dirPath(id)
		if err != nil {
			failed = append(failed, agent.DeleteError{ID: id, Error: err.Error()})
			continue
		}
		info, err := os.Stat(dirPath)
		if err != nil || !info.IsDir() {
			// Not found → treat as success (idempotency).
			deleted = append(deleted, id)
			continue
		}
		if err := s.safeDeleteDir(dirPath); err != nil {
			failed = append(failed, agent.DeleteError{ID: id, Error: err.Error()})
			continue
		}
		s.cleanEmptyParents(filepath.Dir(dirPath))
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
func (s *Store) Rename(_ context.Context, oldID, newID string) error {
	if err := agent.ValidateDocID(oldID); err != nil {
		return err
	}
	if err := agent.ValidateDocID(newID); err != nil {
		return err
	}

	// Try as file first (existing behavior).
	oldFilePath, err := s.docFilePath(oldID)
	if err != nil {
		return err
	}
	newFilePath, err := s.docFilePath(newID)
	if err != nil {
		return err
	}

	if _, err := os.Stat(oldFilePath); err == nil {
		// Old file exists — rename as file.
		if _, err := os.Stat(newFilePath); err == nil {
			return agent.ErrDocAlreadyExists
		}
		if err := os.MkdirAll(filepath.Dir(newFilePath), docDirPermissions); err != nil {
			return fmt.Errorf("filedoc: failed to create target directories: %w", err)
		}
		if err := os.Rename(oldFilePath, newFilePath); err != nil {
			return fmt.Errorf("filedoc: failed to rename file: %w", err)
		}
		s.cleanEmptyParents(filepath.Dir(oldFilePath))
		return nil
	}

	// Try as directory.
	oldDirPath, err := s.dirPath(oldID)
	if err != nil {
		return err
	}
	info, err := os.Stat(oldDirPath)
	if err != nil || !info.IsDir() {
		return agent.ErrDocNotFound
	}

	newDirPath, err := s.dirPath(newID)
	if err != nil {
		return err
	}
	// Check that neither a directory nor a file with the target name exists.
	if _, err := os.Stat(newDirPath); err == nil {
		return agent.ErrDocAlreadyExists
	}
	if _, err := os.Stat(newFilePath); err == nil {
		return agent.ErrDocAlreadyExists
	}

	if err := os.MkdirAll(filepath.Dir(newDirPath), docDirPermissions); err != nil {
		return fmt.Errorf("filedoc: failed to create target directories: %w", err)
	}
	if err := os.Rename(oldDirPath, newDirPath); err != nil {
		return fmt.Errorf("filedoc: failed to rename directory: %w", err)
	}
	s.cleanEmptyParents(filepath.Dir(oldDirPath))
	return nil
}

// Search searches all docs for the given query pattern.
func (s *Store) Search(ctx context.Context, query string) ([]*agent.DocSearchResult, error) {
	var results []*agent.DocSearchResult

	err := filepath.WalkDir(s.baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}

		relPath, err := filepath.Rel(s.baseDir, path)
		if err != nil {
			return nil
		}
		id := strings.TrimSuffix(filepath.ToSlash(relPath), ".md")

		if err := agent.ValidateDocID(id); err != nil {
			logger.Debug(ctx, "Skipping non-conforming doc file", tag.File(relPath), tag.Reason(err.Error()))
			return nil
		}

		data, err := os.ReadFile(path) //nolint:gosec // path constructed from baseDir
		if err != nil {
			return nil
		}

		matches, err := grep.Grep(data, query, grep.DefaultGrepOptions)
		if err != nil {
			return nil // no match or error — skip
		}

		doc, parseErr := parseDocFile(data, id)
		title := id
		var description string
		if parseErr == nil {
			title = doc.Title
			description = doc.Description
		}

		results = append(results, &agent.DocSearchResult{
			ID:          id,
			Title:       title,
			Description: description,
			Matches:     matches,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("filedoc: failed to search: %w", err)
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

func ensureSearchRoot(rootDir string) (bool, error) {
	info, err := os.Stat(rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("filedoc: failed to access docs directory %s: %w", rootDir, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("filedoc: docs directory %s is not a directory", rootDir)
	}
	if _, err := os.ReadDir(rootDir); err != nil {
		return false, fmt.Errorf("filedoc: failed to read docs directory %s: %w", rootDir, err)
	}
	return true, nil
}

func listSearchCandidates(ctx context.Context, rootDir string) ([]docSearchCandidate, error) {
	candidates := make([]docSearchCandidate, 0)
	err := filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			logger.Warn(ctx, "Skipping unreadable doc search entry", tag.File(path), tag.Error(err))
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}

		relPath, err := filepath.Rel(rootDir, path)
		if err != nil {
			logger.Warn(ctx, "Skipping doc with invalid relative path", tag.File(path), tag.Error(err))
			return nil
		}
		id := strings.TrimSuffix(filepath.ToSlash(relPath), ".md")
		if err := agent.ValidateDocID(id); err != nil {
			logger.Debug(ctx, "Skipping non-conforming doc file", tag.File(relPath), tag.Reason(err.Error()))
			return nil
		}

		candidates = append(candidates, docSearchCandidate{
			ID:      id,
			RelPath: filepath.ToSlash(relPath),
			AbsPath: path,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("filedoc: failed to list searchable docs: %w", err)
	}

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
func (s *Store) SearchCursor(ctx context.Context, opts agent.SearchDocsOptions) (*exec.CursorResult[agent.DocSearchResult], error) {
	if opts.Query == "" {
		return &exec.CursorResult[agent.DocSearchResult]{Items: []agent.DocSearchResult{}}, nil
	}
	pathPrefix, err := cleanDocPathPrefix(opts.PathPrefix)
	if err != nil {
		return nil, err
	}
	excludedRoots := normalizeExcludedPathRoots(opts.ExcludePathRoots)
	rootDir, err := s.scopedRoot(pathPrefix)
	if err != nil {
		return nil, err
	}
	exists, err := ensureSearchRoot(rootDir)
	if err != nil {
		return nil, err
	}
	if !exists {
		return &exec.CursorResult[agent.DocSearchResult]{Items: []agent.DocSearchResult{}}, nil
	}

	cursor, err := decodeDocSearchCursor(opts.Cursor, opts.Query, pathPrefix, excludedRoots)
	if err != nil {
		return nil, err
	}

	limit := max(opts.Limit, 1)
	matchLimit := max(opts.MatchLimit, 1)
	results := make([]agent.DocSearchResult, 0, limit)
	pattern := docSearchPattern(opts.Query)
	var hasMore bool
	var nextCursor string

	candidates, err := listSearchCandidates(ctx, rootDir)
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

		data, err := os.ReadFile(candidate.AbsPath) //nolint:gosec // path constructed from baseDir
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
		item := agent.DocSearchResult{
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

	return &exec.CursorResult[agent.DocSearchResult]{
		Items:      results,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}, nil
}

// SearchMatches returns cursor-based snippets for one document.
func (s *Store) SearchMatches(_ context.Context, id string, opts agent.SearchDocMatchesOptions) (*exec.CursorResult[*exec.Match], error) {
	if err := agent.ValidateDocID(id); err != nil {
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
	data, err := os.ReadFile(path) //nolint:gosec // validated path within baseDir
	if err != nil {
		if os.IsNotExist(err) {
			return nil, agent.ErrDocNotFound
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
func normalizeSortParams(sortField agent.DocSortField, sortOrder agent.DocSortOrder) (string, string) {
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

// buildTree builds a tree of DocTreeNode from the filesystem.
func (s *Store) buildTree(ctx context.Context, rootDir, sortField, sortOrder string, excludedRoots []string) ([]*agent.DocTreeNode, error) {
	if info, err := os.Stat(rootDir); err != nil {
		if os.IsNotExist(err) {
			return []*agent.DocTreeNode{}, nil
		}
		return nil, fmt.Errorf("filedoc: failed to access docs directory: %w", err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("filedoc: docs path %s is not a directory", rootDir)
	}

	root := make(map[string]*agent.DocTreeNode)
	var topLevel []*agent.DocTreeNode
	needMtime := sortField == "mtime"

	err := filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == rootDir {
			return nil
		}

		relPath, err := filepath.Rel(rootDir, path)
		if err != nil {
			return nil
		}
		relPath = filepath.ToSlash(relPath)

		if d.IsDir() {
			if docPathRootExcluded(relPath, excludedRoots) {
				return filepath.SkipDir
			}
			var modTime time.Time
			if needMtime {
				if info, infoErr := d.Info(); infoErr == nil {
					modTime = info.ModTime()
				}
			}
			node := &agent.DocTreeNode{
				ID:       relPath,
				Name:     d.Name(),
				Type:     "directory",
				Children: []*agent.DocTreeNode{},
				ModTime:  modTime,
			}
			root[relPath] = node

			parentDir := filepath.Dir(relPath)
			if parentDir == "." {
				topLevel = append(topLevel, node)
			} else if parent, ok := root[parentDir]; ok {
				parent.Children = append(parent.Children, node)
			}
			return nil
		}

		if filepath.Ext(path) != ".md" {
			return nil
		}

		id := strings.TrimSuffix(relPath, ".md")
		if docPathRootExcluded(id, excludedRoots) {
			return nil
		}

		if err := agent.ValidateDocID(id); err != nil {
			logger.Debug(ctx, "Skipping non-conforming doc file", tag.File(relPath), tag.Reason(err.Error()))
			return nil
		}

		data, readErr := os.ReadFile(path) //nolint:gosec // path constructed from baseDir
		var title string
		if readErr == nil {
			if doc, parseErr := parseDocFile(data, id); parseErr == nil {
				title = doc.Title
			}
		}
		if title == "" {
			title = titleFromID(id)
		}

		var modTime time.Time
		if needMtime {
			if info, infoErr := d.Info(); infoErr == nil {
				modTime = info.ModTime()
			}
		}

		node := &agent.DocTreeNode{
			ID:      id,
			Name:    d.Name(),
			Title:   title,
			Type:    "file",
			ModTime: modTime,
		}

		parentDir := filepath.Dir(relPath)
		if parentDir == "." {
			topLevel = append(topLevel, node)
		} else if parent, ok := root[parentDir]; ok {
			parent.Children = append(parent.Children, node)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("filedoc: failed to build tree: %w", err)
	}

	if needMtime {
		propagateModTime(topLevel)
	}
	sortTreeNodes(topLevel, sortField, sortOrder)

	return topLevel, nil
}

// propagateModTime recursively sets each directory's ModTime to the max of
// its own ModTime and all descendant ModTimes.
func propagateModTime(nodes []*agent.DocTreeNode) time.Time {
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

func compareNodeNames(a, b *agent.DocTreeNode) int {
	if cmp := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); cmp != 0 {
		return cmp
	}
	return strings.Compare(a.ID, b.ID)
}

func compareNodeModTime(a, b *agent.DocTreeNode) int {
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

func compareTreeNodes(a, b *agent.DocTreeNode, sortField, sortOrder string) int {
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
func sortTreeNodes(nodes []*agent.DocTreeNode, sortField, sortOrder string) {
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
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
