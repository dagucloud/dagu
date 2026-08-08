// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagstore"
	"github.com/dagucloud/dagu/v2/internal/pagination"
)

// Sentinel errors for doc store operations.
var (
	ErrDocNotFound           = errors.New("doc not found")
	ErrDocAlreadyExists      = errors.New("doc already exists")
	ErrDocPathConflict       = errors.New("doc path conflicts with another node")
	ErrInvalidDocID          = errors.New("invalid doc ID")
	ErrDocRevisionNotFound   = errors.New("doc revision not found")
	ErrDocAttachmentNotFound = errors.New("doc attachment not found")
	ErrInvalidAttachmentName = errors.New("invalid attachment name")
)

// Doc is the domain entity for a markdown document.
type Doc struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Content     string   `json:"content"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
}

// DocMetadata is a lightweight doc view excluding Content.
type DocMetadata struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	ModTime     time.Time `json:"modTime"`
}

// DocTreeNode represents a file or directory in the doc tree.
type DocTreeNode struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Title    string         `json:"title,omitempty"`
	Tags     []string       `json:"tags,omitempty"`
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
	Tags             []string
	ExcludePathRoots []string
}

// SearchDocsOptions configures a paginated document search query.
type SearchDocsOptions struct {
	Cursor           string
	Limit            int
	Query            string
	MatchLimit       int
	PathPrefix       string
	FilterPrefix     string
	Tags             []string
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
	ID                string            `json:"id"`
	Title             string            `json:"title"`
	Description       string            `json:"description,omitempty"`
	Tags              []string          `json:"tags,omitempty"`
	ModTime           time.Time         `json:"modTime"`
	Matches           []*dagstore.Match `json:"matches"`
	MatchCount        int               `json:"matchCount,omitempty"`
	HasMoreMatches    bool              `json:"hasMoreMatches"`
	NextMatchesCursor string            `json:"nextMatchesCursor,omitempty"`
}

// DocRevision is a stored prior version of a document.
type DocRevision struct {
	Rev     string    `json:"rev"`
	SavedAt time.Time `json:"savedAt"`
	Size    int64     `json:"size"`
	Content string    `json:"content,omitempty"`
}

// DocAttachment is a binary file attached to a document.
type DocAttachment struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	SavedAt time.Time `json:"savedAt"`
}

// DeleteError represents a single item failure in a batch delete operation.
type DeleteError struct {
	ID    string
	Error string
}

// DocStore defines the interface for doc persistence.
type DocStore interface {
	List(ctx context.Context, opts ListDocsOptions) (*pagination.PaginatedResult[*DocTreeNode], error)
	ListFlat(ctx context.Context, opts ListDocsOptions) (*pagination.PaginatedResult[DocMetadata], error)
	Get(ctx context.Context, id string) (*Doc, error)
	Create(ctx context.Context, id, content string) error
	Update(ctx context.Context, id, content string) error
	Delete(ctx context.Context, id string) error
	DeleteBatch(ctx context.Context, ids []string) (deleted []string, failed []DeleteError, err error)
	Rename(ctx context.Context, oldID, newID string) error
	// Backlinks returns metadata for documents linking to target. Target is a
	// stored document ID or a scheme-prefixed wiki-link target such as
	// "dag:name". A relative link held by a document under pathPrefix also
	// matches when pathPrefix + "/" + link equals target.
	Backlinks(ctx context.Context, target, pathPrefix string) ([]DocMetadata, error)
	// ListRevisions returns stored prior versions of a document, newest
	// first, without content. Stores without revision support return an
	// empty list.
	ListRevisions(ctx context.Context, id string) ([]DocRevision, error)
	// GetRevision returns one stored revision including its content.
	GetRevision(ctx context.Context, id, rev string) (*DocRevision, error)
	// PutAttachment stores an attachment for an existing document,
	// replacing any attachment with the same name.
	PutAttachment(ctx context.Context, id, name string, content io.Reader) (*DocAttachment, error)
	// OpenAttachment opens an attachment for reading. The caller closes the
	// returned reader.
	OpenAttachment(ctx context.Context, id, name string) (io.ReadCloser, *DocAttachment, error)
	Search(ctx context.Context, query string) ([]*DocSearchResult, error)
	SearchCursor(ctx context.Context, opts SearchDocsOptions) (*pagination.CursorResult[DocSearchResult], error)
	SearchMatches(ctx context.Context, id string, opts SearchDocMatchesOptions) (*pagination.CursorResult[*dagstore.Match], error)
}

// validDocIDRegexp matches a valid doc ID: segments separated by slashes.
// Each segment starts with alphanumeric or underscore and can contain alphanumeric, underscore, dot, hyphen, or space.
const validDocIDPattern = `^[a-zA-Z0-9_][a-zA-Z0-9_. -]*(/[a-zA-Z0-9_][a-zA-Z0-9_. -]*)*$`

var (
	validDocIDRegexp       = regexp.MustCompile(validDocIDPattern)
	windowsReservedSegment = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\..*)?$`)
)

// maxDocIDLength is the maximum allowed length for a doc ID.
const maxDocIDLength = 252

// validAttachmentNameRegexp matches a single-segment attachment file name
// using the doc-ID segment charset.
var validAttachmentNameRegexp = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_. -]*$`)

// maxAttachmentNameLength is the maximum allowed attachment name length.
const maxAttachmentNameLength = 128

// ValidateAttachmentName validates that name is a safe attachment file name:
// a single path segment following the doc-ID segment rules. Extensions used
// by documents and DAG definitions are reserved so attachment files can
// never be mistaken for either.
func ValidateAttachmentName(name string) error {
	if name == "" {
		return ErrInvalidAttachmentName
	}
	if len(name) > maxAttachmentNameLength {
		return fmt.Errorf("%w: exceeds maximum length of %d", ErrInvalidAttachmentName, maxAttachmentNameLength)
	}
	if !validAttachmentNameRegexp.MatchString(name) {
		return fmt.Errorf("%w: must be a single path segment", ErrInvalidAttachmentName)
	}
	if strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
		return fmt.Errorf("%w: must not end with spaces or dots", ErrInvalidAttachmentName)
	}
	if windowsReservedSegment.MatchString(name) {
		return fmt.Errorf("%w: must not use reserved device names", ErrInvalidAttachmentName)
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".yaml", ".yml":
		return fmt.Errorf("%w: extension is reserved for documents and DAG definitions", ErrInvalidAttachmentName)
	}
	return nil
}

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
	for segment := range strings.SplitSeq(id, "/") {
		if strings.HasSuffix(segment, " ") || strings.HasSuffix(segment, ".") {
			return fmt.Errorf("%w: path segments must not end with spaces or dots", ErrInvalidDocID)
		}
		if windowsReservedSegment.MatchString(segment) {
			return fmt.Errorf("%w: path segments must not use reserved device names", ErrInvalidDocID)
		}
	}
	return nil
}
