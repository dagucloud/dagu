// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileagentsoul

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/goccy/go-yaml"
)

// Verify Store implements agent.SoulStore at compile time.
var _ agent.SoulStore = (*Store)(nil)

const (
	soulDirPermissions = 0750
	filePermissions    = 0600
)

// soulFrontmatter holds the YAML fields in the soul file frontmatter.
// The ID is derived from the filename, not stored in the file.
type soulFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
}

// soulIndexEntry caches soul metadata in memory for fast search without file I/O.
type soulIndexEntry struct {
	name        string
	description string
	contentSize int
}

// Store implements a file-based soul store.
// Souls are stored as flat files: {baseDir}/{id}.md
// Each file contains YAML frontmatter (metadata) and a Markdown body (content).
// The in-memory index caches metadata for zero-I/O search operations.
// Thread-safe through internal locking.
type Store struct {
	baseDir string

	mu     sync.RWMutex
	byID   map[string]*soulIndexEntry // soul ID -> metadata
	byName map[string]string          // soul name -> soul ID
}

// New creates a new file-based soul store.
// The baseDir is the directory where soul files are stored.
func New(ctx context.Context, baseDir string) (*Store, error) {
	if baseDir == "" {
		return nil, errors.New("fileagentsoul: baseDir cannot be empty")
	}

	store := &Store{
		baseDir: baseDir,
		byID:    make(map[string]*soulIndexEntry),
		byName:  make(map[string]string),
	}

	if err := os.MkdirAll(baseDir, soulDirPermissions); err != nil {
		return nil, fmt.Errorf("fileagentsoul: failed to create directory %s: %w", baseDir, err)
	}

	if err := store.rebuildIndex(ctx); err != nil {
		return nil, fmt.Errorf("fileagentsoul: failed to build index: %w", err)
	}

	return store, nil
}

// rebuildIndex scans the directory and rebuilds the in-memory index with cached metadata.
func (s *Store) rebuildIndex(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.byID = make(map[string]*soulIndexEntry)
	s.byName = make(map[string]string)

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return fmt.Errorf("failed to read directory %s: %w", s.baseDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".md" {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".md")
		filePath := filepath.Join(s.baseDir, entry.Name())

		soul, err := loadSoulFromFile(filePath, id)
		if err != nil {
			logger.Warn(ctx, "Failed to load soul file during index rebuild",
				slog.String("file", filePath),
				tag.Error(err))
			continue
		}

		s.byID[soul.ID] = &soulIndexEntry{
			name:        soul.Name,
			description: soul.Description,
			contentSize: len(soul.Content),
		}
		if existingID, exists := s.byName[soul.Name]; exists {
			logger.Warn(ctx, "Duplicate soul name in store, last file wins",
				slog.String("name", soul.Name),
				slog.String("existingID", existingID),
				slog.String("newID", soul.ID))
		}
		s.byName[soul.Name] = soul.ID
	}

	return nil
}

// parseSoulFile parses a soul .md file into an agent.Soul.
// The file format is YAML frontmatter between --- delimiters, followed by markdown body.
func parseSoulFile(data []byte, id string) (*agent.Soul, error) {
	// Normalize CRLF to LF upfront.
	content := strings.ReplaceAll(string(data), "\r\n", "\n")

	if !strings.HasPrefix(content, "---\n") {
		return nil, fmt.Errorf("missing opening frontmatter delimiter")
	}

	rest := content[4:] // skip opening "---\n"

	// Find closing delimiter as a whole line.
	closingIdx := strings.Index(rest, "\n---\n")
	if closingIdx == -1 {
		// Check for closing "---" at EOF without trailing newline.
		if strings.HasSuffix(rest, "\n---") {
			closingIdx = len(rest) - 4 // point at the \n before ---
		} else {
			return nil, fmt.Errorf("missing closing frontmatter delimiter")
		}
	}

	frontmatterStr := rest[:closingIdx]

	// Extract body: skip past the "\n---\n" delimiter.
	afterDelim := closingIdx + 5 // len("\n---\n")
	var body string
	if afterDelim <= len(rest) {
		body = rest[afterDelim:]
	}

	var fm soulFrontmatter
	if err := yaml.Unmarshal([]byte(frontmatterStr), &fm); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	if fm.Name == "" {
		return nil, fmt.Errorf("frontmatter field 'name' is required")
	}

	return &agent.Soul{
		ID:          id,
		Name:        fm.Name,
		Description: fm.Description,
		Content:     strings.TrimRight(body, "\n"),
	}, nil
}

// marshalSoulFile produces the soul file content from an agent.Soul.
func marshalSoulFile(soul *agent.Soul) ([]byte, error) {
	fm := soulFrontmatter{
		Name:        soul.Name,
		Description: soul.Description,
	}

	fmBytes, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal frontmatter: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(fmBytes)
	buf.WriteString("---\n")
	if soul.Content != "" {
		buf.WriteString(soul.Content)
		buf.WriteString("\n")
	}

	return buf.Bytes(), nil
}

// loadSoulFromFile reads and parses a soul .md file.
func loadSoulFromFile(filePath, id string) (*agent.Soul, error) {
	data, err := fileutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	soul, err := parseSoulFile(data, id)
	if err != nil {
		return nil, fmt.Errorf("failed to parse soul file %s: %w", filePath, err)
	}

	return soul, nil
}

// writeSoulToFile writes a soul to a .md file atomically.
func writeSoulToFile(filePath string, soul *agent.Soul) error {
	data, err := marshalSoulFile(soul)
	if err != nil {
		return fmt.Errorf("fileagentsoul: failed to marshal soul: %w", err)
	}
	if err := fileutil.WriteFileAtomic(filePath, data, filePermissions); err != nil {
		return fmt.Errorf("fileagentsoul: %w", err)
	}
	return nil
}

// soulFilePath returns the file path for a soul ID.
// Callers must validate the ID before calling this method.
func (s *Store) soulFilePath(id string) (string, error) {
	p := filepath.Join(s.baseDir, id+".md")
	// Defense-in-depth: ensure the resolved path stays within baseDir
	if !strings.HasPrefix(p, filepath.Clean(s.baseDir)+string(filepath.Separator)) {
		return "", fmt.Errorf("fileagentsoul: path traversal detected for id %q", id)
	}
	return p, nil
}

// Create stores a new soul.
func (s *Store) Create(_ context.Context, soul *agent.Soul) error {
	if soul == nil {
		return errors.New("fileagentsoul: soul cannot be nil")
	}
	if err := agent.ValidateSoulID(soul.ID); err != nil {
		return err
	}
	if soul.Name == "" {
		return errors.New("fileagentsoul: soul name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byID[soul.ID]; exists {
		return agent.ErrSoulAlreadyExists
	}
	if _, exists := s.byName[soul.Name]; exists {
		return agent.ErrSoulNameAlreadyExists
	}

	filePath, err := s.soulFilePath(soul.ID)
	if err != nil {
		return err
	}

	if err := writeSoulToFile(filePath, soul); err != nil {
		return err
	}

	s.byID[soul.ID] = &soulIndexEntry{
		name:        soul.Name,
		description: soul.Description,
		contentSize: len(soul.Content),
	}
	s.byName[soul.Name] = soul.ID

	return nil
}

// GetByID retrieves a soul by its unique ID.
// This reads the full soul file (including Content) from disk.
func (s *Store) GetByID(_ context.Context, id string) (*agent.Soul, error) {
	if err := agent.ValidateSoulID(id); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	_, exists := s.byID[id]
	if !exists {
		return nil, agent.ErrSoulNotFound
	}

	filePath, err := s.soulFilePath(id)
	if err != nil {
		return nil, err
	}

	soul, err := loadSoulFromFile(filePath, id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, agent.ErrSoulNotFound
		}
		return nil, fmt.Errorf("fileagentsoul: failed to load soul %s: %w", id, err)
	}

	return soul, nil
}

// List returns all souls, sorted by name.
// This reads full soul files from disk (including Content).
func (s *Store) List(_ context.Context) ([]*agent.Soul, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.byID))
	for id := range s.byID {
		ids = append(ids, id)
	}

	souls := make([]*agent.Soul, 0, len(ids))
	for _, id := range ids {
		filePath, err := s.soulFilePath(id)
		if err != nil {
			continue
		}
		soul, err := loadSoulFromFile(filePath, id)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("fileagentsoul: failed to load soul: %w", err)
		}
		souls = append(souls, soul)
	}

	slices.SortFunc(souls, func(a, b *agent.Soul) int {
		return strings.Compare(a.Name, b.Name)
	})

	return souls, nil
}

// Search filters souls from the in-memory metadata cache with pagination.
// This performs zero file I/O — all filtering is done against cached metadata.
func (s *Store) Search(_ context.Context, opts agent.SearchSoulsOptions) (*exec.PaginatedResult[agent.SoulMetadata], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	queryLower := strings.ToLower(opts.Query)

	var matched []agent.SoulMetadata
	for id, entry := range s.byID {
		if queryLower != "" && !matchesSoulEntry(entry, id, queryLower) {
			continue
		}
		matched = append(matched, agent.SoulMetadata{
			ID:          id,
			Name:        entry.name,
			Description: entry.description,
			ContentSize: entry.contentSize,
		})
	}

	slices.SortFunc(matched, func(a, b agent.SoulMetadata) int {
		return strings.Compare(a.Name, b.Name)
	})

	total := len(matched)
	pg := opts.Paginator
	if pg.Limit() == 0 {
		pg = exec.DefaultPaginator()
	}
	offset := pg.Offset()
	limit := pg.Limit()
	if offset > total {
		offset = total
	}
	end := min(offset+limit, total)
	page := matched[offset:end]

	result := exec.NewPaginatedResult(page, total, pg)
	return &result, nil
}

// matchesSoulEntry checks if a cached soul entry matches a query against name, description, and id.
func matchesSoulEntry(entry *soulIndexEntry, id, queryLower string) bool {
	if strings.Contains(strings.ToLower(entry.name), queryLower) {
		return true
	}
	if strings.Contains(strings.ToLower(entry.description), queryLower) {
		return true
	}
	if strings.Contains(strings.ToLower(id), queryLower) {
		return true
	}
	return false
}

// Update modifies an existing soul.
func (s *Store) Update(_ context.Context, soul *agent.Soul) error {
	if soul == nil {
		return errors.New("fileagentsoul: soul cannot be nil")
	}
	if err := agent.ValidateSoulID(soul.ID); err != nil {
		return err
	}
	if soul.Name == "" {
		return errors.New("fileagentsoul: soul name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.byID[soul.ID]
	if !exists {
		return agent.ErrSoulNotFound
	}

	oldName := entry.name

	filePath, err := s.soulFilePath(soul.ID)
	if err != nil {
		return err
	}

	nameChanged := oldName != soul.Name
	if nameChanged {
		if takenByID, taken := s.byName[soul.Name]; taken && takenByID != soul.ID {
			return agent.ErrSoulNameAlreadyExists
		}
	}

	if err := writeSoulToFile(filePath, soul); err != nil {
		return err
	}

	// Update cached metadata.
	entry.name = soul.Name
	entry.description = soul.Description
	entry.contentSize = len(soul.Content)

	if nameChanged {
		delete(s.byName, oldName)
		s.byName[soul.Name] = soul.ID
	}

	return nil
}

// Delete removes a soul by its ID.
func (s *Store) Delete(_ context.Context, id string) error {
	if err := agent.ValidateSoulID(id); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.byID[id]
	if !exists {
		return agent.ErrSoulNotFound
	}

	filePath, err := s.soulFilePath(id)
	if err != nil {
		return err
	}

	if err := fileutil.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("fileagentsoul: failed to delete soul file: %w", err)
	}

	delete(s.byName, entry.name)
	delete(s.byID, id)

	return nil
}
