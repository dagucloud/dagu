// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wiki

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

// Sentinel errors for page store operations.
var (
	ErrPageNotFound           = errors.New("page not found")
	ErrPageAlreadyExists      = errors.New("page already exists")
	ErrPagePathConflict       = errors.New("page path conflicts with another node")
	ErrInvalidPageID          = errors.New("invalid page ID")
	ErrPageRevisionNotFound   = errors.New("page revision not found")
	ErrPageAttachmentNotFound = errors.New("page attachment not found")
	ErrInvalidAttachmentName  = errors.New("invalid attachment name")
)

// Page is the domain entity for a markdown page.
type Page struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Content     string   `json:"content"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
}

// PageMetadata is a lightweight page view excluding Content.
type PageMetadata struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	ModTime     time.Time `json:"modTime"`
}

// PageTreeNode represents a file or directory in the page tree.
type PageTreeNode struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Title    string          `json:"title,omitempty"`
	Tags     []string        `json:"tags,omitempty"`
	Type     string          `json:"type"` // "file" or "directory"
	Children []*PageTreeNode `json:"children,omitempty"`
	ModTime  time.Time       `json:"modTime"`
}

// PageSortField defines the field to sort pages by.
type PageSortField string

const (
	PageSortFieldName  PageSortField = "name"
	PageSortFieldType  PageSortField = "type"
	PageSortFieldMTime PageSortField = "mtime"
)

// PageSortOrder defines the sort direction.
type PageSortOrder string

const (
	PageSortOrderAsc  PageSortOrder = "asc"
	PageSortOrderDesc PageSortOrder = "desc"
)

// ListPagesOptions holds parameters for listing pages.
type ListPagesOptions struct {
	Page             int
	PerPage          int
	Sort             PageSortField
	Order            PageSortOrder
	PathPrefix       string
	Tags             []string
	ExcludePathRoots []string
}

// SearchPagesOptions configures a paginated page search query.
type SearchPagesOptions struct {
	Cursor           string
	Limit            int
	Query            string
	MatchLimit       int
	PathPrefix       string
	FilterPrefix     string
	Tags             []string
	ExcludePathRoots []string
}

// SearchPageMatchesOptions configures cursor-based snippet loading for one page.
type SearchPageMatchesOptions struct {
	Cursor     string
	Limit      int
	Query      string
	PathPrefix string
}

// PageSearchResult holds a page ID/title and its grep matches.
type PageSearchResult struct {
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

// PageRevision is a stored prior version of a page.
type PageRevision struct {
	Rev     string    `json:"rev"`
	SavedAt time.Time `json:"savedAt"`
	Size    int64     `json:"size"`
	Content string    `json:"content,omitempty"`
}

// PageAttachment is a binary file attached to a page.
type PageAttachment struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	SavedAt time.Time `json:"savedAt"`
}

// DeleteError represents a single item failure in a batch delete operation.
type DeleteError struct {
	ID    string
	Error string
}

// PageStore defines the interface for page persistence.
type PageStore interface {
	List(ctx context.Context, opts ListPagesOptions) (*pagination.PaginatedResult[*PageTreeNode], error)
	ListFlat(ctx context.Context, opts ListPagesOptions) (*pagination.PaginatedResult[PageMetadata], error)
	Get(ctx context.Context, id string) (*Page, error)
	Create(ctx context.Context, id, content string) error
	Update(ctx context.Context, id, content string) error
	Delete(ctx context.Context, id string) error
	DeleteBatch(ctx context.Context, ids []string) (deleted []string, failed []DeleteError, err error)
	Rename(ctx context.Context, oldID, newID string) error
	// Backlinks returns metadata for pages linking to target. Target is a
	// stored page ID or a scheme-prefixed wiki-link target such as
	// "dag:name". A relative link held by a page under pathPrefix also
	// matches when pathPrefix + "/" + link equals target.
	Backlinks(ctx context.Context, target, pathPrefix string) ([]PageMetadata, error)
	// ListRevisions returns stored prior versions of a page, newest
	// first, without content. Stores without revision support return an
	// empty list.
	ListRevisions(ctx context.Context, id string) ([]PageRevision, error)
	// GetRevision returns one stored revision including its content.
	GetRevision(ctx context.Context, id, rev string) (*PageRevision, error)
	// PutAttachment stores an attachment for an existing page,
	// replacing any attachment with the same name.
	PutAttachment(ctx context.Context, id, name string, content io.Reader) (*PageAttachment, error)
	// OpenAttachment opens an attachment for reading. The caller closes the
	// returned reader.
	OpenAttachment(ctx context.Context, id, name string) (io.ReadCloser, *PageAttachment, error)
	Search(ctx context.Context, query string) ([]*PageSearchResult, error)
	SearchCursor(ctx context.Context, opts SearchPagesOptions) (*pagination.CursorResult[PageSearchResult], error)
	SearchMatches(ctx context.Context, id string, opts SearchPageMatchesOptions) (*pagination.CursorResult[*dagstore.Match], error)
}

// validPageIDRegexp matches a valid page ID: segments separated by slashes.
// Each segment starts with alphanumeric or underscore and can contain alphanumeric, underscore, dot, hyphen, or space.
const validPageIDPattern = `^[a-zA-Z0-9_][a-zA-Z0-9_. -]*(/[a-zA-Z0-9_][a-zA-Z0-9_. -]*)*$`

var (
	validPageIDRegexp      = regexp.MustCompile(validPageIDPattern)
	windowsReservedSegment = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\..*)?$`)
)

// maxPageIDLength is the maximum allowed length for a page ID.
const maxPageIDLength = 252

// validAttachmentNameRegexp matches a single-segment attachment file name
// using the page-ID segment charset.
var validAttachmentNameRegexp = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_. -]*$`)

// maxAttachmentNameLength is the maximum allowed attachment name length.
const maxAttachmentNameLength = 128

// ValidateAttachmentName validates that name is a safe attachment file name:
// a single path segment following the page-ID segment rules. Extensions used
// by pages and DAG definitions are reserved so attachment files can
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
		return fmt.Errorf("%w: extension is reserved for pages and DAG definitions", ErrInvalidAttachmentName)
	}
	return nil
}

// ValidatePageID validates that id is a safe, well-formed page identifier.
func ValidatePageID(id string) error {
	if id == "" {
		return ErrInvalidPageID
	}
	if len(id) > maxPageIDLength {
		return fmt.Errorf("%w: exceeds maximum length of %d", ErrInvalidPageID, maxPageIDLength)
	}
	if !validPageIDRegexp.MatchString(id) {
		return fmt.Errorf("%w: must match pattern %s", ErrInvalidPageID, validPageIDPattern)
	}
	for segment := range strings.SplitSeq(id, "/") {
		if strings.HasSuffix(segment, " ") || strings.HasSuffix(segment, ".") {
			return fmt.Errorf("%w: path segments must not end with spaces or dots", ErrInvalidPageID)
		}
		if windowsReservedSegment.MatchString(segment) {
			return fmt.Errorf("%w: path segments must not use reserved device names", ErrInvalidPageID)
		}
	}
	return nil
}
