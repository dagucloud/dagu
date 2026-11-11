// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// relayCancelSendTimeout bounds the send of the cancel message of a client that
// went away.
const relayCancelSendTimeout = time.Second

// AIRelayCapableClient is the coordinator client that has the calls of the relay.
// The client that New returns implements it.
type AIRelayCapableClient interface {
	CheckAIEndpointTo(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.CheckAIEndpointRequest) (*coordinatorv1.CheckAIEndpointResponse, error)
	RelayAIRequestTo(ctx context.Context, owner serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_RelayAIRequestClient, error)
}

var _ AIRelayCapableClient = (*clientImpl)(nil)

// CheckAIEndpointTo makes the start check on the coordinator that owns the task.
// It uses any coordinator when the owner is not set.
func (cli *clientImpl) CheckAIEndpointTo(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.CheckAIEndpointRequest) (*coordinatorv1.CheckAIEndpointResponse, error) {
	var resp *coordinatorv1.CheckAIEndpointResponse
	call := func(ctx context.Context, _ serviceregistry.HostInfo, client *client) error {
		var err error
		resp, err = client.client.CheckAIEndpoint(ctx, req)
		return err
	}
	if completeCoordinatorOwner(owner) {
		if err := cli.callOwner(ctx, owner, false, call); err != nil {
			return nil, err
		}
		return resp, nil
	}
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return nil, err
	}
	if err := cli.attemptCall(ctx, members, call); err != nil {
		return nil, err
	}
	return resp, nil
}

// RelayAIRequestTo opens the stream of one request on the coordinator that owns
// the task. It uses any healthy coordinator when the owner is not set.
func (cli *clientImpl) RelayAIRequestTo(ctx context.Context, owner serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_RelayAIRequestClient, error) {
	if !completeCoordinatorOwner(owner) {
		return openStreamWithFailover(cli, ctx, "AI relay", func(ctx context.Context, client *client) (coordinatorv1.CoordinatorService_RelayAIRequestClient, error) {
			return client.client.RelayAIRequest(ctx)
		})
	}
	var stream coordinatorv1.CoordinatorService_RelayAIRequestClient
	err := cli.callOwner(ctx, owner, true, func(ctx context.Context, _ serviceregistry.HostInfo, client *client) error {
		var callErr error
		stream, callErr = client.client.RelayAIRequest(ctx)
		return callErr
	})
	if err != nil {
		return nil, err
	}
	return stream, nil
}

// AIRelayRun names the attempt that makes the calls. The values come from the
// task that the worker holds.
type AIRelayRun struct {
	WorkerID   string
	AttemptKey string
	AttemptID  string
}

// NewAIRelayClient returns the aigateway.Client of a worker. It sends the calls
// to the coordinator that owns the task. It returns nil when the client has no
// call of the relay.
func NewAIRelayClient(client Client, owner serviceregistry.HostInfo, run AIRelayRun) aigateway.Client {
	capable, ok := client.(AIRelayCapableClient)
	if !ok {
		return nil
	}
	return NewAIRelayClientOver(capable, owner, run)
}

// NewAIRelayClientOver returns the aigateway.Client over the calls of the relay.
func NewAIRelayClientOver(client AIRelayCapableClient, owner serviceregistry.HostInfo, run AIRelayRun) aigateway.Client {
	return &grpcRelayClient{client: client, owner: owner, run: run}
}

type grpcRelayClient struct {
	client AIRelayCapableClient
	owner  serviceregistry.HostInfo
	run    AIRelayRun
}

// Check implements aigateway.Client.
func (c *grpcRelayClient) Check(ctx context.Context, step aigateway.StepRef, target aigateway.Target, provider string) (aigateway.CheckResult, error) {
	return c.CheckModel(ctx, step, target, provider, "")
}

// CheckModel implements aigateway.ModelClient.
func (c *grpcRelayClient) CheckModel(ctx context.Context, step aigateway.StepRef, target aigateway.Target, provider, model string) (aigateway.CheckResult, error) {
	resp, err := c.client.CheckAIEndpointTo(ctx, c.owner, &coordinatorv1.CheckAIEndpointRequest{
		WorkerId:   c.run.WorkerID,
		AttemptKey: c.run.AttemptKey,
		AttemptId:  c.run.AttemptID,
		DagName:    step.DAGName,
		StepName:   step.StepName,
		Target:     &coordinatorv1.RelayTarget{Kind: string(target.Kind), Name: target.Name},
		Provider:   provider,
		Model:      model,
	})
	if err != nil {
		return aigateway.CheckResult{}, statusFailure(err)
	}
	return aigateway.CheckResult{Protocol: aiendpoint.Protocol(resp.GetProtocol()), ChatType: resp.GetChatType()}, nil
}

// statusFailure turns the error of a call into a failure of the gateway. A message
// that starts with a reason keeps it. A denial of the identity is
// endpoint_forbidden. Another error means that the coordinator is out of reach.
func statusFailure(err error) *aigateway.Failure {
	st, ok := status.FromError(err)
	if !ok {
		return aigateway.Fail(aigateway.ReasonRelayUnavailable, "the coordinator is out of reach: %s", err.Error())
	}
	if failure, ok := aigateway.ParseFailure(st.Message()); ok {
		return failure
	}
	switch st.Code() { //nolint:exhaustive // the other codes mean that the relay is not available
	case codes.PermissionDenied:
		return aigateway.Fail(aigateway.ReasonEndpointForbidden, "%s", st.Message())
	case codes.Unimplemented:
		return aigateway.Fail(aigateway.ReasonRelayUnavailable, "the coordinator has no AI relay: update the coordinator")
	case codes.Canceled:
		return aigateway.Fail(aigateway.ReasonCanceled, "the request was canceled")
	}
	return aigateway.Fail(aigateway.ReasonRelayUnavailable, "the coordinator refused the call (%s): %s", st.Code(), st.Message())
}

// Relay implements aigateway.Client: it sends the request over one stream and
// gives the response to the sink.
func (c *grpcRelayClient) Relay(ctx context.Context, step aigateway.StepRef, target aigateway.Target, req aigateway.HTTPRequest, body io.Reader, sink aigateway.ResponseSink) error {
	// The stream outlives the context of the request by one message: the
	// RelayCancel that tells the coordinator that the client went away.
	streamCtx, cancelStream := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelStream()
	stream, err := c.client.RelayAIRequestTo(streamCtx, c.owner)
	if err != nil {
		return aigateway.Fail(aigateway.ReasonRelayUnavailable, "the relay cannot reach the coordinator: %s", err.Error())
	}

	var sendMu sync.Mutex
	send := func(msg *coordinatorv1.RelayClientMessage) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(msg)
	}
	open := &coordinatorv1.RelayOpen{
		WorkerId:      c.run.WorkerID,
		AttemptKey:    c.run.AttemptKey,
		AttemptId:     c.run.AttemptID,
		DagName:       step.DAGName,
		StepName:      step.StepName,
		Target:        &coordinatorv1.RelayTarget{Kind: string(target.Kind), Name: target.Name},
		Method:        req.Method,
		Path:          req.Path,
		Query:         req.RawQuery,
		ContentLength: req.ContentLength,
	}
	for name, values := range req.Header {
		for _, value := range values {
			open.Headers = append(open.Headers, &coordinatorv1.RelayHeader{Name: name, Value: value})
		}
	}
	if err := send(&coordinatorv1.RelayClientMessage{Kind: &coordinatorv1.RelayClientMessage_Open{Open: open}}); err != nil {
		return aigateway.Fail(aigateway.ReasonRelayUnavailable, "the relay cannot send to the coordinator: %s", err.Error())
	}

	// A client that goes away makes the relay send RelayCancel and close the stream.
	// The upload can hold the send lock in a send that the flow control blocks, so
	// the wait for the cancel message is bounded: the stream closes anyway.
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			sent := make(chan struct{})
			go func() {
				defer close(sent)
				_ = send(&coordinatorv1.RelayClientMessage{Kind: &coordinatorv1.RelayClientMessage_Cancel{Cancel: &coordinatorv1.RelayCancel{}}})
			}()
			select {
			case <-sent:
			case <-time.After(relayCancelSendTimeout):
			}
			cancelStream()
		case <-finished:
		}
	}()

	first, err := stream.Recv()
	if err != nil {
		return c.recvFailure(ctx, err)
	}
	var maxRequest int64
	switch {
	case first.GetError() != nil:
		return relayErrorFailure(first.GetError())
	case first.GetAccepted() != nil:
		maxRequest = first.GetAccepted().GetMaxRequestBytes()
	default:
		return aigateway.Fail(aigateway.ReasonRelayUnavailable, "the coordinator sent an unexpected first message")
	}
	if maxRequest > 0 && req.ContentLength > maxRequest {
		_ = send(&coordinatorv1.RelayClientMessage{Kind: &coordinatorv1.RelayClientMessage_Cancel{Cancel: &coordinatorv1.RelayCancel{}}})
		return aigateway.Fail(aigateway.ReasonRequestTooLarge, "the request body has more than %d bytes", maxRequest)
	}

	// The body goes up while the loop below reads the answer. An error of the
	// upload ends the call.
	var uploadErr atomic.Pointer[error]
	var uploadDone sync.WaitGroup
	uploadDone.Go(func() {
		if err := uploadBody(body, maxRequest, send); err != nil {
			uploadErr.Store(&err)
			cancelStream()
		}
	})
	defer func() {
		cancelStream()
		uploadDone.Wait()
	}()

	for {
		msg, err := stream.Recv()
		if err != nil {
			if failed := uploadErr.Load(); failed != nil {
				return uploadFailure(*failed)
			}
			return c.recvFailure(ctx, err)
		}
		switch kind := msg.GetKind().(type) {
		case *coordinatorv1.RelayServerMessage_Head:
			if err := sink.Head(ctx, int(kind.Head.GetStatus()), relayHeaders(kind.Head.GetHeaders())); err != nil {
				return aigateway.Fail(aigateway.ReasonCanceled, "the client went away")
			}
		case *coordinatorv1.RelayServerMessage_Body:
			if proto.Size(msg) > aigateway.MaxMessageBytes {
				return aigateway.Fail(aigateway.ReasonResponseTooLarge, "a message of the response is too large")
			}
			if err := sink.Write(ctx, kind.Body.GetData()); err != nil {
				return aigateway.Fail(aigateway.ReasonCanceled, "the client went away")
			}
		case *coordinatorv1.RelayServerMessage_Error:
			return relayErrorFailure(kind.Error)
		case *coordinatorv1.RelayServerMessage_End:
			return nil
		}
	}
}

// uploadBody sends the request body in chunks of at most 65536 bytes, then the
// end of the body. A body of more than max bytes gives request_too_large.
func uploadBody(body io.Reader, max int64, send func(*coordinatorv1.RelayClientMessage) error) error {
	buf := make([]byte, aigateway.ChunkBytes)
	var total int64
	for {
		n := 0
		var readErr error
		if body != nil {
			n, readErr = body.Read(buf)
		} else {
			readErr = io.EOF
		}
		if n > 0 {
			total += int64(n)
			if max > 0 && total > max {
				return &aigateway.Failure{Reason: aigateway.ReasonRequestTooLarge, Message: fmt.Sprintf("the request body has more than %d bytes", max)}
			}
			data := append([]byte(nil), buf[:n]...)
			if err := send(&coordinatorv1.RelayClientMessage{Kind: &coordinatorv1.RelayClientMessage_Body{Body: &coordinatorv1.RelayBody{Data: data}}}); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return send(&coordinatorv1.RelayClientMessage{Kind: &coordinatorv1.RelayClientMessage_Body{Body: &coordinatorv1.RelayBody{End: true}}})
		}
		if readErr != nil {
			return readErr
		}
	}
}

func uploadFailure(err error) *aigateway.Failure {
	if failure, ok := errors.AsType[*aigateway.Failure](err); ok {
		return failure
	}
	return aigateway.Fail(aigateway.ReasonCanceled, "the request body could not be read: %s", err.Error())
}

// recvFailure maps the error of the stream. A context that ended means that the
// client went away.
func (c *grpcRelayClient) recvFailure(ctx context.Context, err error) *aigateway.Failure {
	if ctx.Err() != nil {
		return aigateway.Fail(aigateway.ReasonCanceled, "the request was canceled")
	}
	if errors.Is(err, io.EOF) {
		return aigateway.Fail(aigateway.ReasonRelayUnavailable, "the coordinator closed the stream with no response")
	}
	return statusFailure(err)
}

func relayErrorFailure(e *coordinatorv1.RelayError) *aigateway.Failure {
	reason := aigateway.Reason(e.GetReason())
	if !reason.Known() {
		return aigateway.Fail(aigateway.ReasonUpstreamError, "the coordinator failed the request with an unknown reason: %s", e.GetMessage())
	}
	return &aigateway.Failure{Reason: reason, Message: e.GetMessage()}
}

func relayHeaders(in []*coordinatorv1.RelayHeader) http.Header {
	out := http.Header{}
	for _, header := range in {
		out.Add(header.GetName(), header.GetValue())
	}
	return out
}
