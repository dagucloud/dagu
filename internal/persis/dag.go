// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/textsearch"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

var (
	ErrDAGAlreadyExists = errors.New("DAG already exists")
	ErrDAGNotFound      = errors.New("DAG is not found")
	ErrDAGReadOnly      = errors.New("DAG definition is read-only")
	// ErrDAGNameReserved reports a DAG name whose file would be a workflow
	// test file, which is never a DAG.
	ErrDAGNameReserved = errors.New("the .test suffix is reserved for workflow test files")
)

// ValidateNewDAGName returns an error that wraps ErrDAGNameReserved when a
// DAG created or renamed under name would be stored as a workflow test file.
func ValidateNewDAGName(name string) error {
	if spec.IsTestFile(fileutil.EnsureYAMLExtension(filepath.Base(name))) {
		return fmt.Errorf("DAG name %s: %w", name, ErrDAGNameReserved)
	}
	return nil
}

// DAGDefinition is a stored DAG specification.
type DAGDefinition struct {
	ID string
	// SourcePath is the optional path where the definition was authored.
	SourcePath string
	Source     []byte
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

// DAGDefinitionStore persists definitions and exposes their listing projection.
type DAGDefinitionStore interface {
	// Create and Rename return an error that wraps ErrDAGNameReserved for an
	// id that would name a workflow test file.
	Create(ctx context.Context, id string, source []byte) error
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (DAGDefinition, error)
	Update(ctx context.Context, id string, source []byte) error
	// UpdateIfMatch is Update for a source that is still the one with the
	// SHA-256 expectedSHA256, in hex. Otherwise it writes nothing and returns an
	// error that wraps ErrConflict. The check and the write are atomic for every
	// process that shares the store.
	UpdateIfMatch(ctx context.Context, id, expectedSHA256 string, source []byte) error
	Rename(ctx context.Context, oldID, newID string) error
	GetMetadata(ctx context.Context, id string) (*ir.DAG, error)
	// MetadataByIDs returns the metadata of each DAG that an id names, by id.
	// An id that names no DAG, or a DAG whose metadata cannot be read, is
	// left out.
	MetadataByIDs(ctx context.Context, ids []string) (map[string]*ir.DAG, error)
	Catalog(ctx context.Context) (DAGCatalog, error)
	// CatalogIncludingSearchPaths is like Catalog but also includes DAG
	// definitions found under the store's additional search paths (for example
	// alt_dags_dir). Stores without additional search paths may return the same
	// result as Catalog.
	CatalogIncludingSearchPaths(ctx context.Context) (DAGCatalog, error)
	// ServiceDAGNames returns the names of the DAGs that set a restart policy.
	// It may read a listing saved earlier, so a definition changed outside the
	// store can count only after the next Catalog.
	ServiceDAGNames(ctx context.Context) ([]string, error)
	// SavedServiceDAGNames is ServiceDAGNames that never builds a listing: it
	// reports false when no saved listing can be read.
	SavedServiceDAGNames() ([]string, bool)
	MigrateSuspensionState(ctx context.Context) error
	SetSuspended(ctx context.Context, id string, suspended bool) error
	IsSuspended(ctx context.Context, id string) (bool, error)
	// SuspendedSince reports when the suspension of a DAG began, or false
	// when the DAG is not suspended. Suspending a suspended DAG keeps it.
	SuspendedSince(ctx context.Context, id string) (time.Time, bool, error)
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
	// Calendars loads the calendar files that schedules name.
	Calendars *spec.CalendarSource
}

// DAGListOptions contains parameters for paginated DAG listing.
type DAGListOptions struct {
	Paginator  *pagination.Paginator
	Name       string
	Labels     []string
	ActiveOnly bool
	// OnlyServiceDAGs keeps the DAGs that set a restart policy.
	OnlyServiceDAGs   bool
	Sort              string
	Order             string
	Time              *time.Time
	NextRunProjection func(*ir.DAG, time.Time) time.Time
	WorkspaceFilter   *workspace.WorkspaceFilter
	// PinnedIDs holds DAG IDs listed before all other DAGs. Sort and Order
	// apply within the pinned and unpinned parts. Nil disables pinning.
	PinnedIDs map[string]struct{}
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
