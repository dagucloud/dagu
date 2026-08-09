// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagstore

import (
	"context"
	"errors"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// DAGLoadOptions selects the loading behaviour a DAGStore caller can choose.
type DAGLoadOptions struct {
	// AllowBuildErrors reports whether a DAG whose build produced errors should be
	// returned instead of failing, so callers can present the errors alongside the
	// partially built DAG.
	AllowBuildErrors bool
}

// Errors for DAG file operations
var (
	ErrDAGAlreadyExists = errors.New("DAG already exists")
	ErrDAGNotFound      = errors.New("DAG is not found")
	ErrDAGReadOnly      = errors.New("DAG definition is read-only")
)

// DAGStore is an interface for interacting with underlying DAG storage systems.
// It allows for different implementations (e.g., local file system, database) to be used interchangeably.
type DAGStore interface {
	// Create stores a new DAG definition with the given name and returns its file name
	Create(ctx context.Context, fileName string, spec []byte) error
	// Delete removes a DAG definition by name
	Delete(ctx context.Context, fileName string) error
	// List returns a paginated list of DAG definitions with filtering options
	List(ctx context.Context, params ListDAGsOptions) (pagination.PaginatedResult[*ir.DAG], []string, error)
	// GetMetadata retrieves only the metadata of a DAG definition (faster than full load)
	GetMetadata(ctx context.Context, fileName string) (*ir.DAG, error)
	// GetDetails retrieves the complete DAG definition including all fields
	GetDetails(ctx context.Context, fileName string, opts DAGLoadOptions) (*ir.DAG, error)
	// Grep searches for a pattern in all DAG definitions and returns matching results
	Grep(ctx context.Context, pattern string) (ret []*GrepDAGsResult, errs []string, err error)
	// SearchCursor returns lightweight, cursor-based search hits for DAG definitions.
	SearchCursor(ctx context.Context, opts SearchDAGsOptions) (*pagination.CursorResult[SearchDAGResult], []string, error)
	// SearchMatches returns cursor-based match snippets for a specific DAG definition.
	SearchMatches(ctx context.Context, fileName string, opts SearchDAGMatchesOptions) (*pagination.CursorResult[*Match], error)
	// Rename changes a DAG's identifier from oldID to newID
	Rename(ctx context.Context, oldID, newID string) error
	// GetSpec retrieves the raw YAML specification of a DAG
	GetSpec(ctx context.Context, fileName string) (string, error)
	// UpdateSpec modifies the specification of an existing DAG
	UpdateSpec(ctx context.Context, fileName string, spec []byte) error
	// LoadSpec builds a DAG with the given name from raw YAML without storing it
	LoadSpec(ctx context.Context, source []byte, name string, opts DAGLoadOptions) (*ir.DAG, error)
	// LabelList returns all unique labels across all DAGs with any errors encountered
	LabelList(ctx context.Context) ([]string, []string, error)
	// ToggleSuspend changes the suspension state of a DAG by ID
	ToggleSuspend(ctx context.Context, fileName string, suspend bool) error
	// IsSuspended checks if a DAG is currently suspended
	IsSuspended(ctx context.Context, fileName string) bool
}

// ListDAGsOptions contains parameters for paginated DAG listing
type ListDAGsOptions struct {
	Paginator         *pagination.Paginator
	Name              string                             // Optional search filter for DAG name or file name
	Labels            []string                           // Optional labels filter (AND logic - all labels must match)
	ActiveOnly        bool                               // Include only scheduled DAGs that are not suspended
	Sort              string                             // Optional sort field (name, updated_at, created_at, nextRun)
	Order             string                             // Optional sort order (asc, desc)
	Time              *time.Time                         // Optional reference time for nextRun sorting/projection (defaults to time.Now())
	NextRunProjection func(*ir.DAG, time.Time) time.Time // Optional scheduler-aware nextRun projector used when Sort == "nextRun"
	WorkspaceFilter   *workspace.WorkspaceFilter         // Optional workspace visibility filter
}

// SearchDAGsOptions contains parameters for cursor-based DAG search.
type SearchDAGsOptions struct {
	Cursor          string
	Limit           int
	Query           string
	MatchLimit      int
	Labels          []string
	WorkspaceFilter *workspace.WorkspaceFilter
}

// SearchDAGMatchesOptions contains parameters for cursor-based snippet loading.
type SearchDAGMatchesOptions struct {
	Cursor          string
	Limit           int
	Query           string
	Labels          []string
	WorkspaceFilter *workspace.WorkspaceFilter
}

// GrepDAGsResult represents the result of a pattern search within a DAG definition
type GrepDAGsResult struct {
	Name    string   // Name of the DAG
	DAG     *ir.DAG  // The DAG object
	Matches []*Match // Matching lines and their context
}

// SearchDAGResult represents a lightweight DAG search hit for paginated UIs.
type SearchDAGResult struct {
	Name              string
	FileName          string
	Workspace         string
	Matches           []*Match
	HasMoreMatches    bool
	NextMatchesCursor string
}

// Match contains matched line number and line content.
type Match struct {
	Line       string
	LineNumber int
	StartLine  int
}
