// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
)

// Sentinel errors for doc store operations.
var (
	ErrDocNotFound      = errors.New("doc not found")
	ErrDocAlreadyExists = errors.New("doc already exists")
	ErrInvalidDocID     = errors.New("invalid doc ID")
)

// Doc is the domain entity for a markdown document.
type Doc struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Content     string `json:"content"`
	FilePath    string `json:"filePath,omitempty"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// DocMetadata is a lightweight doc view excluding Content.
type DocMetadata struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	ModTime     time.Time `json:"modTime"`
}

// DocTreeNode represents a file or directory in the doc tree.
type DocTreeNode struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Title    string         `json:"title,omitempty"`
	Type     string         `json:"type"` // "file" or "directory"
	Children []*DocTreeNode `json:"children,omitempty"`
	ModTime  time.Time      `json:"modTime"`
}

// DocSortField defines the field to sort documents by.
type DocSortField string

const (
	DocSortFieldName  DocSortField = "name"
	DocSortFieldType  DocSortField = "type"
	DocSortFieldMTime DocSortField = "mtime"
)

// DocSortOrder defines the sort direction.
type DocSortOrder string

const (
	DocSortOrderAsc  DocSortOrder = "asc"
	DocSortOrderDesc DocSortOrder = "desc"
)

// ListDocsOptions holds parameters for listing documents.
type ListDocsOptions struct {
	Page             int
	PerPage          int
	Sort             DocSortField
	Order            DocSortOrder
	PathPrefix       string
	ExcludePathRoots []string
}

// SearchDocsOptions configures a paginated document search query.
type SearchDocsOptions struct {
	Cursor           string
	Limit            int
	Query            string
	MatchLimit       int
	PathPrefix       string
	ExcludePathRoots []string
}

// SearchDocMatchesOptions configures cursor-based snippet loading for one document.
type SearchDocMatchesOptions struct {
	Cursor     string
	Limit      int
	Query      string
	PathPrefix string
}

// DocSearchResult holds a doc ID/title and its grep matches.
type DocSearchResult struct {
	ID                string        `json:"id"`
	Title             string        `json:"title"`
	Description       string        `json:"description,omitempty"`
	Matches           []*exec.Match `json:"matches"`
	HasMoreMatches    bool          `json:"hasMoreMatches"`
	NextMatchesCursor string        `json:"nextMatchesCursor,omitempty"`
}

// DeleteError represents a single item failure in a batch delete operation.
type DeleteError struct {
	ID    string
	Error string
}

// DocStore defines the interface for doc persistence.
type DocStore interface {
	List(ctx context.Context, opts ListDocsOptions) (*exec.PaginatedResult[*DocTreeNode], error)
	ListFlat(ctx context.Context, opts ListDocsOptions) (*exec.PaginatedResult[DocMetadata], error)
	Get(ctx context.Context, id string) (*Doc, error)
	Create(ctx context.Context, id, content string) error
	Update(ctx context.Context, id, content string) error
	Delete(ctx context.Context, id string) error
	DeleteBatch(ctx context.Context, ids []string) (deleted []string, failed []DeleteError, err error)
	Rename(ctx context.Context, oldID, newID string) error
	Search(ctx context.Context, query string) ([]*DocSearchResult, error)
	SearchCursor(ctx context.Context, opts SearchDocsOptions) (*exec.CursorResult[DocSearchResult], error)
	SearchMatches(ctx context.Context, id string, opts SearchDocMatchesOptions) (*exec.CursorResult[*exec.Match], error)
}

// validDocIDRegexp matches a valid doc ID: segments separated by slashes.
// Each segment starts with alphanumeric or underscore and can contain alphanumeric, underscore, dot, hyphen, or space.
const validDocIDPattern = `^[a-zA-Z0-9_][a-zA-Z0-9_. -]*(/[a-zA-Z0-9_][a-zA-Z0-9_. -]*)*$`

var validDocIDRegexp = regexp.MustCompile(validDocIDPattern)

// maxDocIDLength is the maximum allowed length for a doc ID.
const maxDocIDLength = 256

// ValidateDocID validates that id is a safe, well-formed doc identifier.
func ValidateDocID(id string) error {
	if id == "" {
		return ErrInvalidDocID
	}
	if len(id) > maxDocIDLength {
		return fmt.Errorf("%w: exceeds maximum length of %d", ErrInvalidDocID, maxDocIDLength)
	}
	if !validDocIDRegexp.MatchString(id) {
		return fmt.Errorf("%w: must match pattern %s", ErrInvalidDocID, validDocIDPattern)
	}
	return nil
}
