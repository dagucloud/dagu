// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"errors"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/textsearch"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

var (
	ErrDAGAlreadyExists = errors.New("DAG already exists")
	ErrDAGNotFound      = errors.New("DAG is not found")
	ErrDAGReadOnly      = errors.New("DAG definition is read-only")
)

// DAGDefinition is a stored DAG specification.
type DAGDefinition struct {
	ID string
	// Location is an optional loader-readable source path.
	Location string
	Source   []byte
}

// DAGListItem contains a stable storage identity, metadata, and suspension state.
type DAGListItem struct {
	ID string
	*ir.DAG
	Suspended bool
}

// DAGCatalog is the unfiltered set of stored DAG definitions. Item order is unspecified.
type DAGCatalog struct {
	Items  []DAGListItem
	Issues []string
}

// DAGDefinitionStore persists DAG definitions without application-level queries.
type DAGDefinitionStore interface {
	Create(ctx context.Context, id string, source []byte) error
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (DAGDefinition, error)
	Update(ctx context.Context, id string, source []byte) error
	Rename(ctx context.Context, oldID, newID string) error
	GetMetadata(ctx context.Context, id string) (*ir.DAG, error)
	Catalog(ctx context.Context) (DAGCatalog, error)
	SetSuspended(ctx context.Context, id string, suspended bool) error
	IsSuspended(ctx context.Context, id string) (bool, error)
}

// DAGLoadOptions controls how a DAG specification is built.
type DAGLoadOptions struct {
	// AllowBuildErrors returns a partially built DAG with its build errors attached.
	AllowBuildErrors bool
}

// DAGRepositoryOptions configures DAG specification loading.
type DAGRepositoryOptions struct {
	BaseConfigPath         string
	WorkspaceBaseConfigDir string
}

// DAGRepository provides application-level access to DAG definitions.
type DAGRepository interface {
	Create(ctx context.Context, id string, source []byte) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, opts DAGListOptions) (pagination.PaginatedResult[DAGListItem], []string, error)
	GetMetadata(ctx context.Context, id string) (*ir.DAG, error)
	GetDetails(ctx context.Context, id string, opts DAGLoadOptions) (*ir.DAG, error)
	Grep(ctx context.Context, pattern string) ([]*DAGGrepResult, []string, error)
	SearchCursor(ctx context.Context, opts DAGSearchOptions) (*pagination.CursorResult[DAGSearchResult], []string, error)
	SearchMatches(ctx context.Context, id string, opts DAGMatchSearchOptions) (*pagination.CursorResult[*textsearch.Match], error)
	Rename(ctx context.Context, oldID, newID string) error
	GetSpec(ctx context.Context, id string) (string, error)
	UpdateSpec(ctx context.Context, id string, source []byte) error
	LoadSpec(ctx context.Context, source []byte, name string, opts DAGLoadOptions) (*ir.DAG, error)
	LabelList(ctx context.Context) ([]string, []string, error)
	SetSuspended(ctx context.Context, id string, suspended bool) error
	IsSuspended(ctx context.Context, id string) (bool, error)
}

// DAGListOptions contains parameters for paginated DAG listing.
type DAGListOptions struct {
	Paginator         *pagination.Paginator
	Name              string
	Labels            []string
	ActiveOnly        bool
	Sort              string
	Order             string
	Time              *time.Time
	NextRunProjection func(*ir.DAG, time.Time) time.Time
	WorkspaceFilter   *workspace.WorkspaceFilter
}

// DAGSearchOptions contains parameters for cursor-based DAG search.
type DAGSearchOptions struct {
	Cursor          string
	Limit           int
	Query           string
	MatchLimit      int
	Labels          []string
	WorkspaceFilter *workspace.WorkspaceFilter
}

// DAGMatchSearchOptions contains parameters for loading snippets from one DAG.
type DAGMatchSearchOptions struct {
	Cursor          string
	Limit           int
	Query           string
	Labels          []string
	WorkspaceFilter *workspace.WorkspaceFilter
}

// DAGGrepResult represents matches within one DAG definition.
type DAGGrepResult struct {
	Name    string
	DAG     *ir.DAG
	Matches []*textsearch.Match
}

// DAGSearchResult is a lightweight DAG search hit.
type DAGSearchResult struct {
	Name              string
	FileName          string
	Workspace         string
	Matches           []*textsearch.Match
	HasMoreMatches    bool
	NextMatchesCursor string
}
