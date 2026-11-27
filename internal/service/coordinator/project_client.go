// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// errClientWithoutProjects refuses a call that must address a project
// through a client that cannot.
var errClientWithoutProjects = errors.New("the coordinator client cannot address a project")

// projectCaller addresses a project in the calls of a Client whose arguments
// name none.
type projectCaller interface {
	getDAGRunStatus(ctx context.Context, scope dispatch.ProjectScope, dagName, dagRunID string, rootRef *ir.DAGRunRef) (*dispatch.DAGRunStatusResult, error)
	requestCancel(ctx context.Context, scope dispatch.ProjectScope, dagName, dagRunID string, rootRef *ir.DAGRunRef) error
	getDAG(ctx context.Context, scope dispatch.ProjectScope, caller *coordinatorv1.CallerAttempt, name string) (string, error)
}

var _ projectCaller = (*clientImpl)(nil)

// InProject returns a client whose calls for runs address the runs that
// scope addresses: the tasks it dispatches, the statuses, logs, and
// artifacts it reports, and the runs and DAGs it reads or cancels. It
// returns c itself for the default project's own DAGs.
func InProject(c Client, scope dispatch.ProjectScope) Client {
	if c == nil || scope == (dispatch.ProjectScope{}) {
		return c
	}
	return &projectClient{Client: c, scope: scope, project: convert.ProjectScopeToProto(scope)}
}

// InAttempt returns a client whose calls for runs address the project of the
// attempt run names, as InProject does, and name that attempt where the
// coordinator ties a call to its caller. It returns c itself for an attempt
// of the default project's own DAGs.
func InAttempt(c Client, run SecretReferenceRun) Client {
	if c == nil || run.Project == (dispatch.ProjectScope{}) {
		return c
	}
	return &projectClient{
		Client:  c,
		scope:   run.Project,
		project: convert.ProjectScopeToProto(run.Project),
		caller:  callerAttemptOf(run),
	}
}

// projectClient is a Client that addresses one project's runs.
type projectClient struct {
	Client
	scope   dispatch.ProjectScope
	project *coordinatorv1.ProjectScope
	// caller is the attempt the client's calls act for, or nil.
	caller *coordinatorv1.CallerAttempt
}

func (c *projectClient) Dispatch(ctx context.Context, req dispatch.DispatchRequest) error {
	if req.Task != nil && req.Task.Project == (dispatch.ProjectScope{}) {
		task := *req.Task
		task.Project = c.scope
		req.Task = &task
	}
	return c.Client.Dispatch(ctx, req)
}

func (c *projectClient) GetDAGRunStatus(ctx context.Context, dagName, dagRunID string, rootRef *ir.DAGRunRef) (*dispatch.DAGRunStatusResult, error) {
	caller, ok := c.Client.(projectCaller)
	if !ok {
		return nil, errClientWithoutProjects
	}
	return caller.getDAGRunStatus(ctx, c.scope, dagName, dagRunID, rootRef)
}

func (c *projectClient) RequestCancel(ctx context.Context, dagName, dagRunID string, rootRef *ir.DAGRunRef) error {
	caller, ok := c.Client.(projectCaller)
	if !ok {
		return errClientWithoutProjects
	}
	return caller.requestCancel(ctx, c.scope, dagName, dagRunID, rootRef)
}

func (c *projectClient) GetDAG(ctx context.Context, name string) (string, error) {
	caller, ok := c.Client.(projectCaller)
	if !ok {
		return "", errClientWithoutProjects
	}
	return caller.getDAG(ctx, c.scope, c.caller, name)
}

func (c *projectClient) ReportStatus(ctx context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	req.Project = c.project
	return c.Client.ReportStatus(ctx, req)
}

func (c *projectClient) ReportStatusTo(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	req.Project = c.project
	return c.Client.ReportStatusTo(ctx, owner, req)
}

func (c *projectClient) StreamLogs(ctx context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	return c.logStream(c.Client.StreamLogs(ctx))
}

func (c *projectClient) StreamLogsTo(ctx context.Context, owner serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	return c.logStream(c.Client.StreamLogsTo(ctx, owner))
}

func (c *projectClient) logStream(stream coordinatorv1.CoordinatorService_StreamLogsClient, err error) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	if err != nil {
		return nil, err
	}
	return &projectLogStream{CoordinatorService_StreamLogsClient: stream, project: c.project}, nil
}

func (c *projectClient) StreamArtifacts(ctx context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	return c.artifactStream(c.Client.StreamArtifacts(ctx))
}

func (c *projectClient) StreamArtifactsTo(ctx context.Context, owner serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	return c.artifactStream(c.Client.StreamArtifactsTo(ctx, owner))
}

func (c *projectClient) artifactStream(stream coordinatorv1.CoordinatorService_StreamArtifactsClient, err error) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	if err != nil {
		return nil, err
	}
	return &projectArtifactStream{CoordinatorService_StreamArtifactsClient: stream, project: c.project}, nil
}

// PutWorkspaceBundle and GetWorkspaceBundle reach the server-wide bundle
// store, which every project shares.
func (c *projectClient) PutWorkspaceBundle(ctx context.Context, desc workspacebundle.Descriptor, data []byte) error {
	bundles, ok := c.Client.(workspacebundle.Client)
	if !ok {
		return errors.New("the coordinator client does not support workspace bundles")
	}
	return bundles.PutWorkspaceBundle(ctx, desc, data)
}

func (c *projectClient) GetWorkspaceBundle(ctx context.Context, digest string) ([]byte, error) {
	bundles, ok := c.Client.(workspacebundle.Client)
	if !ok {
		return nil, errors.New("the coordinator client does not support workspace bundles")
	}
	return bundles.GetWorkspaceBundle(ctx, digest)
}

// projectLogStream sends log chunks that address one project.
type projectLogStream struct {
	coordinatorv1.CoordinatorService_StreamLogsClient
	project *coordinatorv1.ProjectScope
}

func (s *projectLogStream) Send(chunk *coordinatorv1.LogChunk) error {
	chunk.Project = s.project
	return s.CoordinatorService_StreamLogsClient.Send(chunk)
}

// projectArtifactStream sends artifact chunks that address one project.
type projectArtifactStream struct {
	coordinatorv1.CoordinatorService_StreamArtifactsClient
	project *coordinatorv1.ProjectScope
}

func (s *projectArtifactStream) Send(chunk *coordinatorv1.ArtifactChunk) error {
	chunk.Project = s.project
	return s.CoordinatorService_StreamArtifactsClient.Send(chunk)
}
