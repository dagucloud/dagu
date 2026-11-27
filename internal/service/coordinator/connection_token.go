// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/ir"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	connectionAccessDenied = "connection access denied"
	// connectionField is the YAML key of every field that declares a
	// connection.
	connectionField = "connection"
)

// ConnectionTokenClient answers, for a run on a worker, the connections its
// DAG declares.
type ConnectionTokenClient interface {
	// ResolveConnectionToken sends req to owner, or to any coordinator when
	// owner is empty. An error from the coordinator keeps its gRPC status.
	ResolveConnectionToken(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.ResolveConnectionTokenRequest) (*coordinatorv1.ResolveConnectionTokenResponse, error)
}

func (cli *clientImpl) ResolveConnectionToken(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.ResolveConnectionTokenRequest) (*coordinatorv1.ResolveConnectionTokenResponse, error) {
	if !emptyCoordinatorOwner(owner) && !completeCoordinatorOwner(owner) {
		return nil, fmt.Errorf("connection token owner coordinator endpoint is incomplete")
	}
	var resp *coordinatorv1.ResolveConnectionTokenResponse
	call := func(ctx context.Context, member serviceregistry.HostInfo, client *client) error {
		var err error
		resp, err = client.client.ResolveConnectionToken(ctx, req)
		if err != nil {
			return err
		}
		if resp == nil {
			return fmt.Errorf("coordinator %s returned empty connection token response", member.ID)
		}
		return nil
	}
	var err error
	if completeCoordinatorOwner(owner) {
		err = cli.callOwner(ctx, owner, false, call)
	} else {
		var members []serviceregistry.HostInfo
		if members, err = cli.getCoordinatorMembers(ctx); err == nil {
			err = cli.attemptCall(ctx, members, call)
		}
	}
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// ResolveConnectionToken answers a connection that the DAG of an attempt the
// worker holds declares, resolved in the DAG's workspace. The notification
// connection, which belongs to no workspace, never resolves.
func (h *Handler) ResolveConnectionToken(ctx context.Context, req *coordinatorv1.ResolveConnectionTokenRequest) (*coordinatorv1.ResolveConnectionTokenResponse, error) {
	if h.connectionStore == nil || h.connectionTokens == nil {
		return nil, status.Error(codes.Unavailable, "connection store is not configured")
	}
	if req.GetConnection() == "" {
		return nil, status.Error(codes.InvalidArgument, "connection is required")
	}
	dag, err := h.leaseDAG(ctx, req.GetWorkerId(), req.GetAttemptKey(), req.GetAttemptId(), req.GetDagName(), connectionAccessDenied)
	if err != nil {
		return nil, h.connectionLeaseError(ctx, req, err)
	}
	ref, ok := declaredConnection(dag, req.GetConnection())
	if !ok {
		return nil, status.Error(codes.PermissionDenied, connectionAccessDenied)
	}
	ws, state := workspace.WorkspaceLabelFromLabels(dag.Labels)
	if state == workspace.WorkspaceLabelInvalid {
		return nil, status.Errorf(codes.InvalidArgument, "connection %q: the DAG's workspace labels name no single valid workspace", ref.Name)
	}
	key := connection.Key{Workspace: secretpkg.NormalizeWorkspace(ws), Name: ref.Name}

	if req.GetCheckOnly() {
		if err := connection.Admit(ctx, h.connectionStore, ws, ref.Name, ref.Servers); err != nil {
			return nil, connectionRPCError(err, codes.InvalidArgument)
		}
	}
	conn, err := h.connectionStore.Get(ctx, key)
	if err != nil {
		return nil, connectionRPCError(fmt.Errorf("connection %q: %w", ref.Name, err), codes.Internal)
	}
	resp := &coordinatorv1.ResolveConnectionTokenResponse{Binding: connectionBinding(conn)}
	if req.GetCheckOnly() {
		return resp, nil
	}

	// At least a second, so a token never expires before its answer is read.
	minTTL := time.Duration(max(req.GetMinTtlSeconds(), 1)) * time.Second
	var expiresIn time.Duration
	if rejected := req.GetRejectedToken(); rejected != "" {
		resp.AccessToken, expiresIn, err = h.connectionTokens.Rejected(ctx, conn, req.GetResource(), rejected, minTTL)
	} else {
		resp.AccessToken, expiresIn, err = h.connectionTokens.Token(ctx, conn, req.GetResource(), minTTL)
	}
	if err != nil {
		return nil, connectionRPCError(err, codes.Internal)
	}
	resp.ExpiresInSeconds = expiresInSeconds(expiresIn)
	return resp, nil
}

// connectionLeaseError returns the error of a request whose lease check
// failed. A coordinator that cannot check leases fails as Internal, since
// FailedPrecondition tells the worker that the connection needs
// reconnecting. A child the run reaches only by a reference is denied with
// the reason.
func (h *Handler) connectionLeaseError(ctx context.Context, req *coordinatorv1.ResolveConnectionTokenRequest, err error) error {
	switch status.Code(err) { //nolint:exhaustive // Only codes that would mislead the worker are rewritten.
	case codes.FailedPrecondition:
		return status.Error(codes.Internal, status.Convert(err).Message())
	case codes.PermissionDenied:
		if req.GetDagName() == "" {
			return err
		}
		if _, rootErr := h.leaseDAG(ctx, req.GetWorkerId(), req.GetAttemptKey(), req.GetAttemptId(), "", connectionAccessDenied); rootErr != nil {
			return err
		}
		return status.Errorf(codes.PermissionDenied,
			"connection %q: %s: DAG %q is not a child the run names as written, and a child named by a reference cannot use connections on the worker that runs its parent; name the child as written or give it a worker_selector",
			req.GetConnection(), connectionAccessDenied, req.GetDagName())
	}
	return err
}

// declaresConnections reports whether the definition of task, or one of
// its local DAGs, declares connections. The load runs no eval, so it has no
// side effect. A definition that fails to load declares none here;
// preparing its attempt reports the failure.
func declaresConnections(ctx context.Context, task *coordinatorv1.Task) bool {
	// Every field that declares a connection is named connection, so a
	// definition without the word declares none and is not loaded. A
	// double-quoted YAML key can spell the word with escapes, which need a
	// backslash.
	if !mayDeclareConnections(task.Definition) && !mayDeclareConnections(task.BaseConfig) {
		return false
	}
	loadOpts := []spec.LoadOption{spec.WithName(task.Target), spec.WithoutEval()}
	if task.BaseConfig != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfigContent([]byte(task.BaseConfig)))
	}
	dag, err := spec.LoadYAML(ctx, []byte(task.Definition), loadOpts...)
	if err != nil {
		return false
	}
	return dag.DeclaresConnections()
}

func mayDeclareConnections(yaml string) bool {
	return strings.Contains(yaml, connectionField) || strings.Contains(yaml, `\`)
}

// declaredConnection returns the connection named name that dag declares.
func declaredConnection(dag *ir.DAG, name string) (ir.ConnectionRef, bool) {
	for _, ref := range dag.Connections {
		if ref.Name == name {
			return ref, true
		}
	}
	return ir.ConnectionRef{}, false
}

func connectionBinding(conn *connection.Connection) *coordinatorv1.ConnectionBinding {
	binding := &coordinatorv1.ConnectionBinding{
		Provider:   string(conn.Provider),
		BoundHosts: conn.AllBoundHosts(),
		Resource:   conn.Resource,
	}
	for _, svc := range conn.Services {
		binding.Services = append(binding.Services, &coordinatorv1.ConnectionService{Name: svc.Name, Access: string(svc.Access)})
	}
	return binding
}

// expiresInSeconds rounds up, and answers 1 for a token already past its
// expiry, so only a token without an expiry answers 0.
func expiresInSeconds(d time.Duration) int64 {
	switch {
	case d == 0:
		return 0
	case d < 0:
		return 1
	}
	return int64((d + time.Second - 1) / time.Second)
}

// connectionRPCError maps err to a status. fallback is the code of an error
// of no known kind.
func connectionRPCError(err error, fallback codes.Code) error {
	code := fallback
	switch {
	case errors.Is(err, connection.ErrNotFound):
		code = codes.NotFound
	case errors.Is(err, broker.ErrReconnectRequired):
		code = codes.FailedPrecondition
	case errors.Is(err, connection.ErrInvalid):
		code = codes.InvalidArgument
	case errors.Is(err, context.Canceled):
		code = codes.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		code = codes.DeadlineExceeded
	}
	return status.Error(code, err.Error())
}
