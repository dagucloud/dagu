// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"fmt"
	"io"

	"github.com/dagucloud/dagu/v2/internal/agentsession"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
	profilepkg "github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ProjectStores are the stores in which a coordinator keeps the runs of one
// project, and the settings its runs on workers resolve. A nil store leaves
// the calls that need it unavailable.
type ProjectStores struct {
	DAGRunRepository         *persis.DAGRunRepository
	LogDir                   string
	ArtifactDir              string
	StateStore               dagrun.StateStore
	DAGRepository            *persis.DAGRepository
	SecretStore              secretpkg.Store
	ProfileStore             profilepkg.Store
	NodeInventory            inventory.Store
	TrustedHostKeys          runctx.TrustedHostKeys
	AgentSessionCleanupQueue *agentsession.CleanupQueue
}

// ProjectSource finds the stores of the runs of registered projects other
// than those of the default project's own DAGs.
type ProjectSource interface {
	// Stores returns the stores of the runs that scope addresses. It fails
	// when no registered project has the scope's ID.
	Stores(ctx context.Context, scope dispatch.ProjectScope) (*ProjectStores, error)
	// Projects returns the scopes of the own DAGs of the registered
	// projects other than the default one.
	Projects() []dispatch.ProjectScope
}

// scopedStoresKey carries the scopedStores of the project a call addresses.
type scopedStoresKey struct{}

// scopedStores are the stores of the runs that scope addresses.
type scopedStores struct {
	scope  dispatch.ProjectScope
	stores *ProjectStores
}

// inProject returns ctx addressing the runs of scope. Every store the
// handler reads or writes for the call comes from the returned context, and
// runs are cached and locked by scope as well as by run ID, since a migrated
// project shares the default project's run IDs. A scope the coordinator
// does not serve is an error.
func (h *Handler) inProject(ctx context.Context, scope dispatch.ProjectScope) (context.Context, error) {
	scoped := scopedStores{scope: scope, stores: &h.defaults}
	if scope == (dispatch.ProjectScope{}) && ctx.Value(scopedStoresKey{}) == nil {
		return ctx, nil
	}
	if scope != (dispatch.ProjectScope{}) {
		if h.projects == nil {
			return nil, status.Error(codes.FailedPrecondition, "this coordinator serves only the default project's own DAGs")
		}
		stores, err := h.projects.Stores(ctx, scope)
		if err != nil {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		scoped.stores = stores
	}
	return context.WithValue(ctx, scopedStoresKey{}, scoped), nil
}

// inProjectOf returns ctx addressing the runs of the project a call names,
// unset for the default project's own DAGs.
func (h *Handler) inProjectOf(ctx context.Context, project *coordinatorv1.ProjectScope) (context.Context, error) {
	return h.inProject(ctx, convert.ProtoToProjectScope(project))
}

// stores returns the stores of the runs ctx addresses: those of the default
// project's own DAGs unless inProject chose others.
func (h *Handler) stores(ctx context.Context) *ProjectStores {
	if scoped, ok := ctx.Value(scopedStoresKey{}).(scopedStores); ok {
		return scoped.stores
	}
	return &h.defaults
}

// scopeOf returns the scope of the runs ctx addresses.
func scopeOf(ctx context.Context) dispatch.ProjectScope {
	scoped, _ := ctx.Value(scopedStoresKey{}).(scopedStores)
	return scoped.scope
}

// runKey returns the key that caches and locks the run dagRunID of the runs
// ctx addresses. It is dagRunID for the default project's own DAGs.
func (h *Handler) runKey(ctx context.Context, dagRunID string) string {
	scope := scopeOf(ctx)
	if scope == (dispatch.ProjectScope{}) {
		return dagRunID
	}
	return fmt.Sprintf("%s\x00%t\x00%s", scope.ID, scope.Library, dagRunID)
}

// checkLeaseProject refuses a call for the attempt of lease unless the call
// addresses the attempt's project. A call that names no project addresses
// the default project, so it is refused for another project's attempt.
func (h *Handler) checkLeaseProject(ctx context.Context, lease *dispatch.DAGRunLease) error {
	if lease != nil && lease.Project != scopeOf(ctx) {
		return status.Error(codes.PermissionDenied, "the attempt belongs to another project than the call addresses")
	}
	return nil
}

// inLeaseProject returns ctx addressing the project of the attempt whose
// lease attemptKey names, for calls that name an attempt but no project.
// Without such a lease, ctx keeps addressing the default project's own DAGs.
func (h *Handler) inLeaseProject(ctx context.Context, attemptKey string) (context.Context, error) {
	if h.projects == nil || h.dagRunLeaseStore == nil || attemptKey == "" {
		return ctx, nil
	}
	lease, err := h.dagRunLeaseStore.Get(ctx, attemptKey)
	if err != nil || lease == nil {
		return ctx, nil
	}
	return h.inProject(ctx, lease.Project)
}

// eachProject calls fn with ctx addressing the own DAGs of the default
// project and then of each other registered project. A project whose stores
// cannot open is skipped.
func (h *Handler) eachProject(ctx context.Context, fn func(context.Context) bool) {
	if !fn(ctx) || h.projects == nil {
		return
	}
	for _, scope := range h.projects.Projects() {
		scoped, err := h.inProject(ctx, scope)
		if err != nil {
			continue
		}
		if !fn(scoped) {
			return
		}
	}
}

// errStreamProjectChanged refuses a stream whose chunks address more than
// one project.
var errStreamProjectChanged = status.Error(codes.InvalidArgument, "stream chunks address more than one project")

// scopedLogStream is a log stream whose context addresses the project of its
// first chunk, which every later chunk must address too.
type scopedLogStream struct {
	coordinatorv1.CoordinatorService_StreamLogsServer
	ctx   context.Context
	first *coordinatorv1.LogChunk
}

// scopeLogStream reads the first chunk of stream and returns the stream
// addressing its project. It returns nil once it answers a stream that
// ends before any chunk.
func (h *Handler) scopeLogStream(stream coordinatorv1.CoordinatorService_StreamLogsServer) (*scopedLogStream, error) {
	first, err := stream.Recv()
	if err == io.EOF {
		return nil, stream.SendAndClose(&coordinatorv1.StreamLogsResponse{})
	}
	if err != nil {
		return nil, fmt.Errorf("failed to receive chunk: %w", err)
	}
	ctx, err := h.inProjectOf(stream.Context(), first.GetProject())
	if err != nil {
		return nil, err
	}
	return &scopedLogStream{CoordinatorService_StreamLogsServer: stream, ctx: ctx, first: first}, nil
}

func (s *scopedLogStream) Context() context.Context { return s.ctx }

func (s *scopedLogStream) Recv() (*coordinatorv1.LogChunk, error) {
	chunk := s.first
	s.first = nil
	if chunk == nil {
		var err error
		if chunk, err = s.CoordinatorService_StreamLogsServer.Recv(); err != nil {
			return nil, err
		}
	}
	if convert.ProtoToProjectScope(chunk.GetProject()) != scopeOf(s.ctx) {
		return nil, errStreamProjectChanged
	}
	return chunk, nil
}

// scopedArtifactStream is the artifact stream counterpart of
// scopedLogStream.
type scopedArtifactStream struct {
	coordinatorv1.CoordinatorService_StreamArtifactsServer
	ctx   context.Context
	first *coordinatorv1.ArtifactChunk
}

// scopeArtifactStream is the artifact stream counterpart of scopeLogStream.
func (h *Handler) scopeArtifactStream(stream coordinatorv1.CoordinatorService_StreamArtifactsServer) (*scopedArtifactStream, error) {
	first, err := stream.Recv()
	if err == io.EOF {
		return nil, stream.SendAndClose(&coordinatorv1.StreamArtifactsResponse{})
	}
	if err != nil {
		return nil, fmt.Errorf("failed to receive artifact chunk: %w", err)
	}
	ctx, err := h.inProjectOf(stream.Context(), first.GetProject())
	if err != nil {
		return nil, err
	}
	return &scopedArtifactStream{CoordinatorService_StreamArtifactsServer: stream, ctx: ctx, first: first}, nil
}

func (s *scopedArtifactStream) Context() context.Context { return s.ctx }

func (s *scopedArtifactStream) Recv() (*coordinatorv1.ArtifactChunk, error) {
	chunk := s.first
	s.first = nil
	if chunk == nil {
		var err error
		if chunk, err = s.CoordinatorService_StreamArtifactsServer.Recv(); err != nil {
			return nil, err
		}
	}
	if convert.ProtoToProjectScope(chunk.GetProject()) != scopeOf(s.ctx) {
		return nil, errStreamProjectChanged
	}
	return chunk, nil
}
