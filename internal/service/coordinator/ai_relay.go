// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Spec 094, "The RPC" and "Identity checks". The coordinator serves the calls of
// the relay of a worker. It checks the identity of the worker, the lease of the
// attempt and the target of the step before it passes the request to the gateway.
// No message carries a key.

const (
	// defaultRelayRecheckInterval is how often an open stream checks its lease
	// again (Spec 094, "Identity checks").
	defaultRelayRecheckInterval = 10 * time.Second
	// relayFinalSendTimeout bounds the last message of a stream, which a client
	// that does not read can block.
	relayFinalSendTimeout = 5 * time.Second
	// relayBodyBuffer is the number of body chunks that the stream holds. The
	// flow control of gRPC slows the client when the buffer is full.
	relayBodyBuffer = 4
	// relayTransientChecks is the number of checks in a row that an open stream
	// lets fail with an error of the storage before it ends. A denial ends it at
	// once.
	relayTransientChecks = 3
)

const relayDenied = "AI relay access denied"

// relayIdentity is what a call of the relay names.
type relayIdentity struct {
	workerID   string
	attemptKey string
	attemptID  string
	dagName    string
	stepName   string
	target     *coordinatorv1.RelayTarget
}

// relayAuth is the result of the identity checks of one call.
type relayAuth struct {
	subject aigateway.Subject
	target  aigateway.Target
	// mcpAllow is the key allow of the step for a MCP target. It comes from the DAG
	// that the coordinator holds.
	mcpAllow []string
	identity relayIdentity
}

// CheckAIEndpoint implements the start check of a step that names an endpoint.
func (h *Handler) CheckAIEndpoint(ctx context.Context, req *coordinatorv1.CheckAIEndpointRequest) (*coordinatorv1.CheckAIEndpointResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "check request is required")
	}
	auth, err := h.authorizeAIRelay(ctx, relayIdentity{
		workerID: req.GetWorkerId(), attemptKey: req.GetAttemptKey(), attemptID: req.GetAttemptId(),
		dagName: req.GetDagName(), stepName: req.GetStepName(), target: req.GetTarget(),
	})
	if err != nil {
		return nil, relayStatusError(err)
	}
	if h.aiGateway == nil {
		return nil, failureToStatus(aigateway.Fail(aigateway.ReasonEndpointUnavailable, "the coordinator has no AI gateway"))
	}
	result, failure := h.aiGateway.CheckModel(ctx, auth.subject, auth.target, req.GetProvider(), req.GetModel())
	if failure != nil {
		return nil, failureToStatus(failure)
	}
	return &coordinatorv1.CheckAIEndpointResponse{Protocol: string(result.Protocol), ChatType: result.ChatType}, nil
}

// relayFailureError is a refusal of the target. A stream sends it as a message. A
// unary call turns it into a gRPC error, with the reason at the start of the
// message, as aigateway.ParseFailure reads it.
type relayFailureError struct{ failure *aigateway.Failure }

func (e *relayFailureError) Error() string { return e.failure.Error() }

func relayFailureStatus(f *aigateway.Failure) error {
	return &relayFailureError{failure: f}
}

// relayStatusError converts the error of the checks to the error of a unary call.
func relayStatusError(err error) error {
	if target, ok := errors.AsType[*relayFailureError](err); ok {
		return failureToStatus(target.failure)
	}
	return err
}

func failureToStatus(f *aigateway.Failure) error {
	code := codes.FailedPrecondition
	switch f.Reason { //nolint:exhaustive // the other reasons keep FailedPrecondition
	case aigateway.ReasonEndpointForbidden, aigateway.ReasonMCPServerForbidden:
		code = codes.PermissionDenied
	case aigateway.ReasonEndpointUnknown, aigateway.ReasonMCPServerUnknown:
		code = codes.NotFound
	case aigateway.ReasonEndpointUnavailable:
		code = codes.Unavailable
	}
	return status.Error(code, f.Error())
}

// authorizeAIRelay applies the checks 1 to 5 of Spec 094, "Identity checks". The
// first failure ends the call. A failure of the checks 1 to 4 is an error of
// gRPC. A failure of the check 5 is a failure of the gateway, as an error of gRPC
// with the reason in its message. The gateway makes the checks 6 to 9.
func (h *Handler) authorizeAIRelay(ctx context.Context, id relayIdentity) (*relayAuth, error) {
	// 1. A verified client certificate, also when the coordinator has no pools.
	if _, err := certificateIdentity(ctx); err != nil {
		return nil, status.Error(codes.PermissionDenied, "the AI relay needs mutual TLS: "+err.Error())
	}
	if id.workerID == "" || id.attemptKey == "" || id.attemptID == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id, attempt_key and attempt_id are required")
	}
	if id.stepName == "" {
		return nil, status.Error(codes.InvalidArgument, "step_name is required")
	}
	if id.target == nil || id.target.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "the target is required")
	}
	// 2 to 4. The pool, the lease, the attempt, the DAG and the status of the step.
	lease, dag, err := h.verifyRelayLease(ctx, id)
	if err != nil {
		return nil, err
	}
	// 5. The step names the target. The name comes from the DAG that the
	// coordinator holds, never from the request.
	steps := relaySteps(dag, id.stepName)
	if len(steps) == 0 {
		return nil, relayFailureStatus(aigateway.Fail(aigateway.ReasonEndpointForbidden,
			"the DAG %q has no step %q", dag.Name, id.stepName))
	}
	target := aigateway.Target{Kind: aigateway.TargetKind(id.target.GetKind()), Name: id.target.GetName()}
	var mcpAllow []string
	// A name can be the name of steps at more than one place, such as a step of a
	// foreach body. The request names no place, so each step of the name must
	// grant the target, and a MCP allow is the tools that each step allows.
	for i, step := range steps {
		switch target.Kind {
		case aigateway.TargetEndpoint:
			if !slices.Contains(step.AIEndpoints(), target.Name) {
				return nil, relayFailureStatus(aigateway.Fail(aigateway.ReasonEndpointForbidden,
					"the step %q does not name the endpoint %q", id.stepName, target.Name))
			}
		case aigateway.TargetMCP:
			if !slices.Contains(step.AIMCPServers(), target.Name) {
				return nil, relayFailureStatus(aigateway.Fail(aigateway.ReasonMCPServerForbidden,
					"the step %q does not name the MCP server %q", id.stepName, target.Name))
			}
			allow := step.MCPAllow(target.Name)
			if i == 0 {
				mcpAllow = slices.Clone(allow)
			} else {
				mcpAllow = slices.DeleteFunc(mcpAllow, func(tool string) bool { return !slices.Contains(allow, tool) })
			}
		default:
			return nil, status.Errorf(codes.InvalidArgument, "the target kind %q is not valid", id.target.GetKind())
		}
	}

	workspaceName, _ := workspace.WorkspaceNameFromLabels(dag.Labels)
	return &relayAuth{
		subject: aigateway.Subject{
			RunID:      lease.DAGRun.ID,
			DAGName:    dag.Name,
			Workspace:  workspaceName,
			StepName:   id.stepName,
			WorkerID:   id.workerID,
			AttemptKey: id.attemptKey,
		},
		target:   target,
		mcpAllow: mcpAllow,
		identity: id,
	}, nil
}

// verifyRelayLease applies the checks 2 to 4: the pool of the caller is the pool
// of the lease, the lease is fresh and matches the worker, the attempt key and the
// attempt ID, the step belongs to the DAG of the attempt, and neither the attempt
// nor the step has a final status.
func (h *Handler) verifyRelayLease(ctx context.Context, id relayIdentity) (*dispatch.DAGRunLease, *ir.DAG, error) {
	if h.dagRunLeaseStore == nil {
		return nil, nil, status.Error(codes.FailedPrecondition, "dag-run lease store is not configured")
	}
	if h.dagRunRepository == nil {
		return nil, nil, status.Error(codes.FailedPrecondition, "DAG-run repository is not configured")
	}
	lease, err := h.dagRunLeaseStore.Get(ctx, id.attemptKey)
	if err != nil {
		if errors.Is(err, dispatch.ErrDAGRunLeaseNotFound) {
			return nil, nil, status.Error(codes.PermissionDenied, relayDenied)
		}
		return nil, nil, status.Error(codes.Internal, err.Error())
	}
	if lease.WorkerID != id.workerID || lease.AttemptID != id.attemptID {
		return nil, nil, status.Error(codes.PermissionDenied, relayDenied)
	}
	if err := h.checkLeasePool(ctx, lease.WorkerPool); err != nil {
		return nil, nil, err
	}
	if !lease.IsFresh(time.Now().UTC(), h.staleLeaseThreshold) {
		return nil, nil, status.Error(codes.PermissionDenied, relayDenied)
	}
	attempt, err := h.authorizedAttempt(ctx, lease, relayDenied)
	if err != nil {
		return nil, nil, err
	}
	dag, err := h.authorizedDAG(ctx, attempt, id.dagName, relayDenied)
	if err != nil {
		return nil, nil, err
	}
	if len(relaySteps(dag, id.stepName)) == 0 && id.stepName != "" {
		return nil, nil, relayFailureStatus(aigateway.Fail(aigateway.ReasonEndpointForbidden,
			"the DAG %q has no step %q", dag.Name, id.stepName))
	}
	runStatus, err := attempt.ReadStatusUncached(ctx)
	if err != nil {
		// A read can fail while a writer replaces the file: the error is transient.
		return nil, nil, status.Error(codes.Unavailable, relayDenied+": the status of the attempt cannot be read")
	}
	if runStatus == nil {
		return nil, nil, status.Error(codes.PermissionDenied, relayDenied+": the status of the attempt is not available")
	}
	if !attemptMayFetchSecret(runStatus.Status) {
		return nil, nil, status.Errorf(codes.PermissionDenied, "%s: the attempt has the final status %s", relayDenied, runStatus.Status)
	}
	// The status of the attempt holds the steps of its own DAG only.
	if runStatus.Name == "" || runStatus.Name == dag.Name {
		if node := statusNode(runStatus, id.stepName); node != nil && !nodeMayFetchSecret(node.Status) {
			return nil, nil, status.Errorf(codes.PermissionDenied, "%s: the step %q has the final status %s", relayDenied, id.stepName, node.Status)
		}
	}
	return lease, dag, nil
}

// relaySteps returns each step of the DAG with the name: the steps, the steps of
// the foreach bodies and the handlers.
func relaySteps(dag *ir.DAG, name string) []*ir.Step {
	var steps []*ir.Step
	if dag == nil || name == "" {
		return nil
	}
	dag.WalkSteps(func(step *ir.Step) {
		if step.Name == name {
			steps = append(steps, step)
		}
	})
	return steps
}

// RelayAIRequest carries one request to the provider and its response.
func (h *Handler) RelayAIRequest(stream coordinatorv1.CoordinatorService_RelayAIRequestServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	open := first.GetOpen()
	if open == nil {
		return status.Error(codes.InvalidArgument, "the first message must be an open message")
	}
	if proto.Size(first) > aigateway.MaxMessageBytes {
		return status.Error(codes.ResourceExhausted, "the open message is too large")
	}
	auth, err := h.authorizeAIRelay(stream.Context(), relayIdentity{
		workerID: open.GetWorkerId(), attemptKey: open.GetAttemptKey(), attemptID: open.GetAttemptId(),
		dagName: open.GetDagName(), stepName: open.GetStepName(), target: open.GetTarget(),
	})
	if err != nil {
		// Each refusal has an audit line, with the reason that the worker reads. The
		// line has no run ID: the checks of the identity did not give one.
		target, isTarget := errors.AsType[*relayFailureError](err)
		if h.aiGateway != nil {
			failure := statusFailure(err)
			if isTarget {
				failure = target.failure
			}
			h.aiGateway.AuditRefusal(stream.Context(), aigateway.Subject{
				DAGName: open.GetDagName(), StepName: open.GetStepName(), WorkerID: open.GetWorkerId(),
			}, aigateway.Target{Kind: aigateway.TargetKind(open.GetTarget().GetKind()), Name: open.GetTarget().GetName()},
				open.GetMethod(), failure)
		}
		if isTarget {
			// A refusal of the target is a message of the stream.
			return h.refuseRelay(stream, target.failure)
		}
		return err
	}
	if h.aiGateway == nil {
		return h.refuseRelay(stream, aigateway.Fail(aigateway.ReasonEndpointUnavailable, "the coordinator has no AI gateway"))
	}
	if _, failure := h.aiGateway.Check(stream.Context(), auth.subject, auth.target, ""); failure != nil {
		h.aiGateway.AuditRefusal(stream.Context(), auth.subject, auth.target, open.GetMethod(), failure)
		return h.refuseRelay(stream, failure)
	}

	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	s := &relayStream{stream: stream}
	limits := h.aiGateway.Limits()
	if err := s.send(ctx, &coordinatorv1.RelayServerMessage{Kind: &coordinatorv1.RelayServerMessage_Accepted{
		Accepted: &coordinatorv1.RelayAccepted{
			MaxRequestBytes:  limits.MaxRequestBytes,
			MaxResponseBytes: limits.MaxResponseBytes,
			IdleTimeoutSec:   int32(min(limits.IdleTimeout/time.Second, 1<<30)), //nolint:gosec // bounded above
		},
	}}); err != nil {
		return nil
	}

	// The lease is checked again while the stream is open. A lease that ends
	// cancels the call to the provider.
	var leaseLost atomic.Bool
	go h.watchRelayLease(ctx, auth.identity, &leaseLost, cancel)

	body := newRelayBody(ctx, cancel)
	go body.receive(stream)

	header := http.Header{}
	for _, h := range open.GetHeaders() {
		header.Add(h.GetName(), h.GetValue())
	}
	sink := &relaySink{stream: s}
	failure := h.aiGateway.Do(ctx, aigateway.Request{
		Subject:       auth.subject,
		Target:        auth.target,
		MCPAllow:      auth.mcpAllow,
		Method:        open.GetMethod(),
		Path:          open.GetPath(),
		RawQuery:      open.GetQuery(),
		Header:        header,
		ContentLength: open.GetContentLength(),
		Body:          body,
	}, sink)

	final, cancel2 := context.WithTimeout(context.WithoutCancel(stream.Context()), relayFinalSendTimeout)
	defer cancel2()
	switch {
	case failure == nil:
		_ = s.send(final, &coordinatorv1.RelayServerMessage{Kind: &coordinatorv1.RelayServerMessage_End{
			End: &coordinatorv1.RelayEnd{ResponseBytes: sink.bytes.Load()},
		}})
	case failure.Reason == aigateway.ReasonCanceled:
		if leaseLost.Load() {
			// The call to the provider is canceled. The client learns why.
			logger.Warn(ctx, "AI relay stream canceled: the lease of the attempt ended")
			_ = s.send(final, relayErrorMessage(aigateway.Fail(aigateway.ReasonRelayUnavailable, "the lease of the attempt ended")))
		}
	default:
		_ = s.send(final, relayErrorMessage(failure))
	}
	return nil
}

// refuseRelay sends the failure as the last message of the stream.
func (h *Handler) refuseRelay(stream coordinatorv1.CoordinatorService_RelayAIRequestServer, failure *aigateway.Failure) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(stream.Context()), relayFinalSendTimeout)
	defer cancel()
	s := &relayStream{stream: stream}
	_ = s.send(ctx, relayErrorMessage(failure))
	return nil
}

func relayErrorMessage(failure *aigateway.Failure) *coordinatorv1.RelayServerMessage {
	return &coordinatorv1.RelayServerMessage{Kind: &coordinatorv1.RelayServerMessage_Error{
		Error: &coordinatorv1.RelayError{Reason: string(failure.Reason), Message: failure.Message},
	}}
}

// watchRelayLease checks the lease of the attempt again once every interval. It
// cancels the call when a check denies the call, or when relayTransientChecks
// checks in a row fail with an error of the storage. A stream that streams for
// a long time is not cut by one slow read of a shared file system.
func (h *Handler) watchRelayLease(ctx context.Context, id relayIdentity, lost *atomic.Bool, cancel context.CancelFunc) {
	interval := h.relayRecheckInterval
	if interval <= 0 {
		interval = defaultRelayRecheckInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _, err := h.verifyRelayLease(ctx, id)
			if err == nil || ctx.Err() != nil {
				failures = 0
				continue
			}
			if transientRelayCheck(err) {
				if failures++; failures < relayTransientChecks {
					logger.Warn(ctx, "AI relay: the check of the lease failed; the stream stays open",
						tag.AttemptKey(id.attemptKey), tag.Error(err))
					continue
				}
			}
			lost.Store(true)
			cancel()
			return
		}
	}
}

// transientRelayCheck reports whether a failed check of the lease comes from the
// storage, not from a denial.
func transientRelayCheck(err error) bool {
	switch status.Code(err) { //nolint:exhaustive // each other code is a denial
	case codes.Internal, codes.Unavailable:
		return true
	}
	return false
}

// relayStream serializes the sends of one stream. A send waits for the flow
// control of gRPC and ends with the context.
type relayStream struct {
	stream coordinatorv1.CoordinatorService_RelayAIRequestServer
	mu     sync.Mutex
}

func (s *relayStream) send(ctx context.Context, msg *coordinatorv1.RelayServerMessage) error {
	done := make(chan error, 1)
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		done <- s.stream.Send(msg)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// The goroutine ends when the handler returns and gRPC closes the stream.
		return ctx.Err()
	}
}

// relaySink writes the response of the gateway to the stream.
type relaySink struct {
	stream *relayStream
	bytes  atomic.Int64
}

func (s *relaySink) Head(ctx context.Context, statusCode int, header http.Header) error {
	head := &coordinatorv1.RelayResponseHead{Status: int32(statusCode)} //nolint:gosec // an HTTP status
	for name, values := range header {
		for _, value := range values {
			head.Headers = append(head.Headers, &coordinatorv1.RelayHeader{Name: name, Value: value})
		}
	}
	return s.stream.send(ctx, &coordinatorv1.RelayServerMessage{Kind: &coordinatorv1.RelayServerMessage_Head{Head: head}})
}

func (s *relaySink) Write(ctx context.Context, chunk []byte) error {
	for len(chunk) > 0 {
		n := min(len(chunk), aigateway.ChunkBytes)
		data := append([]byte(nil), chunk[:n]...)
		if err := s.stream.send(ctx, &coordinatorv1.RelayServerMessage{Kind: &coordinatorv1.RelayServerMessage_Body{
			Body: &coordinatorv1.RelayBody{Data: data},
		}}); err != nil {
			return err
		}
		s.bytes.Add(int64(n))
		chunk = chunk[n:]
	}
	return nil
}

// relayBody is the request body that the client sends in chunks. The channel is
// small, so a slow reader slows the client through the flow control of gRPC.
type relayBody struct {
	ctx    context.Context
	cancel context.CancelFunc
	ch     chan []byte
	rest   []byte
	done   bool
}

func newRelayBody(ctx context.Context, cancel context.CancelFunc) *relayBody {
	return &relayBody{ctx: ctx, cancel: cancel, ch: make(chan []byte, relayBodyBuffer)}
}

// receive reads the messages of the client until a cancel or an error. The
// reader sees the end of the body at the first message with the end flag, but
// the loop goes on: a cancel can come while the response streams. A cancel and an
// error cancel the call.
func (b *relayBody) receive(stream coordinatorv1.CoordinatorService_RelayAIRequestServer) {
	var closeOnce sync.Once
	endBody := func() { closeOnce.Do(func() { close(b.ch) }) }
	defer endBody()
	ended := false
	for {
		msg, err := stream.Recv()
		if err != nil {
			// A client that closes its side before the end of the body sent a
			// truncated body: the call does not go on with it.
			if !errors.Is(err, io.EOF) || !ended {
				b.cancel()
			}
			return
		}
		if proto.Size(msg) > aigateway.MaxMessageBytes {
			b.cancel()
			return
		}
		switch {
		case msg.GetCancel() != nil:
			b.cancel()
			return
		case msg.GetBody() != nil:
			body := msg.GetBody()
			if len(body.GetData()) > aigateway.ChunkBytes {
				b.cancel()
				return
			}
			if ended {
				// No data follows the end of the body.
				b.cancel()
				return
			}
			if len(body.GetData()) > 0 {
				select {
				case b.ch <- body.GetData():
				case <-b.ctx.Done():
					return
				}
			}
			if body.GetEnd() {
				ended = true
				endBody()
			}
		default:
			// A second open message is not valid.
			b.cancel()
			return
		}
	}
}

// Read implements io.Reader. It ends with the context.
func (b *relayBody) Read(p []byte) (int, error) {
	if len(b.rest) == 0 {
		if b.done {
			return 0, io.EOF
		}
		select {
		case chunk, ok := <-b.ch:
			if !ok {
				b.done = true
				if err := b.ctx.Err(); err != nil {
					return 0, err
				}
				return 0, io.EOF
			}
			b.rest = chunk
		case <-b.ctx.Done():
			return 0, b.ctx.Err()
		}
	}
	n := copy(p, b.rest)
	b.rest = b.rest[n:]
	return n, nil
}
