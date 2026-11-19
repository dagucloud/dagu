// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	testRelayRun  = coordinator.AIRelayRun{WorkerID: "worker-1", AttemptKey: "attempt-key-1", AttemptID: "attempt-1"}
	testRelayStep = aigateway.StepRef{RunID: "run-1", DAGName: "fix-bug", StepName: "agent"}
)

// The start check names the attempt, the step, the target, the provider and the
// model, and returns the protocol and the chat type of the endpoint. It reaches
// the owner of the task or, with no owner, any coordinator.
func TestAIRelayClientCheck(t *testing.T) {
	t.Parallel()

	requests := make(chan *coordinatorv1.CheckAIEndpointRequest, 1)
	clients := newRelayClients(t, &relayCoordinator{
		check: func(_ context.Context, req *coordinatorv1.CheckAIEndpointRequest) (*coordinatorv1.CheckAIEndpointResponse, error) {
			requests <- req
			return &coordinatorv1.CheckAIEndpointResponse{Protocol: "openai", ChatType: "openai"}, nil
		},
	})
	for route, client := range clients {
		result, err := aigateway.CheckStart(t.Context(), client, testRelayStep, aigateway.EndpointTarget("team"), "codex", "gpt-5")
		require.NoError(t, err, route)
		assert.Equal(t, aiendpoint.ProtocolOpenAI, result.Protocol, route)
		assert.Equal(t, "openai", result.ChatType, route)

		req := <-requests
		assert.Equal(t, testRelayRun.WorkerID, req.GetWorkerId(), route)
		assert.Equal(t, testRelayRun.AttemptKey, req.GetAttemptKey(), route)
		assert.Equal(t, testRelayRun.AttemptID, req.GetAttemptId(), route)
		assert.Equal(t, testRelayStep.DAGName, req.GetDagName(), route)
		assert.Equal(t, testRelayStep.StepName, req.GetStepName(), route)
		assert.Equal(t, string(aigateway.TargetEndpoint), req.GetTarget().GetKind(), route)
		assert.Equal(t, "team", req.GetTarget().GetName(), route)
		assert.Equal(t, "codex", req.GetProvider(), route)
		assert.Equal(t, "gpt-5", req.GetModel(), route)
	}
}

// A refusal of the start check keeps the reason that the coordinator put in the
// message. A denied identity is endpoint_forbidden, and a coordinator with no
// relay asks for its update.
func TestAIRelayClientCheckFailures(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err     error // nil leaves the call unimplemented
		reason  aigateway.Reason
		message string
	}{
		"reason of the coordinator": {
			err:     status.Error(codes.NotFound, string(aigateway.ReasonEndpointUnknown)+`: no endpoint "team"`),
			reason:  aigateway.ReasonEndpointUnknown,
			message: `no endpoint "team"`,
		},
		"denied identity": {
			err:     status.Error(codes.PermissionDenied, "the attempt is not active"),
			reason:  aigateway.ReasonEndpointForbidden,
			message: "the attempt is not active",
		},
		"coordinator with no relay": {
			reason:  aigateway.ReasonRelayUnavailable,
			message: "update the coordinator",
		},
		"other error": {
			err:     status.Error(codes.Internal, "store failed"),
			reason:  aigateway.ReasonRelayUnavailable,
			message: "store failed",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			service := &relayCoordinator{}
			if tt.err != nil {
				service.check = func(context.Context, *coordinatorv1.CheckAIEndpointRequest) (*coordinatorv1.CheckAIEndpointResponse, error) {
					return nil, tt.err
				}
			}
			client := newRelayClients(t, service)["any coordinator"]
			_, err := client.Check(t.Context(), testRelayStep, aigateway.EndpointTarget("team"), "claude")
			requireRelayFailure(t, err, tt.reason, tt.message)
		})
	}
}

// A request goes up as an open message with its head and the body in chunks of
// at most ChunkBytes, then the end of the body. The response comes down to the
// sink as it streams.
func TestAIRelayClientRelay(t *testing.T) {
	t.Parallel()

	body := bytes.Repeat([]byte("x"), 2*aigateway.ChunkBytes+10)
	type received struct {
		open    *coordinatorv1.RelayOpen
		body    []byte
		largest int
	}
	got := make(chan received, 1)
	clients := newRelayClients(t, &relayCoordinator{
		relay: func(stream coordinatorv1.CoordinatorService_RelayAIRequestServer) error {
			first, err := stream.Recv()
			if err != nil {
				return err
			}
			if err := stream.Send(acceptedMessage(int64(len(body)))); err != nil {
				return err
			}
			data, largest, err := receiveBody(stream)
			if err != nil {
				return err
			}
			got <- received{open: first.GetOpen(), body: data, largest: largest}
			return sendAll(stream,
				headMessage(http.StatusOK, "Content-Type", "text/event-stream"),
				bodyMessage("event: one\n"),
				bodyMessage("event: two\n"),
				endMessage(),
			)
		},
	})
	for route, client := range clients {
		header := http.Header{}
		header.Set("Content-Type", "application/json")
		header.Set("Anthropic-Version", "2023-06-01")
		req := aigateway.HTTPRequest{
			Method:        http.MethodPost,
			Path:          "/v1/messages",
			RawQuery:      "beta=true",
			Header:        header,
			ContentLength: int64(len(body)),
		}
		sink := &responseRecorder{}
		err := client.Relay(t.Context(), testRelayStep, aigateway.EndpointTarget("team"), req, bytes.NewReader(body), sink)
		require.NoError(t, err, route)
		assert.Equal(t, http.StatusOK, sink.status, route)
		assert.Equal(t, "text/event-stream", sink.header.Get("Content-Type"), route)
		assert.Equal(t, "event: one\nevent: two\n", sink.body.String(), route)

		seen := <-got
		assert.Equal(t, body, seen.body, route)
		assert.Equal(t, aigateway.ChunkBytes, seen.largest, route)
		open := seen.open
		assert.Equal(t, testRelayRun.WorkerID, open.GetWorkerId(), route)
		assert.Equal(t, testRelayRun.AttemptKey, open.GetAttemptKey(), route)
		assert.Equal(t, testRelayRun.AttemptID, open.GetAttemptId(), route)
		assert.Equal(t, testRelayStep.DAGName, open.GetDagName(), route)
		assert.Equal(t, testRelayStep.StepName, open.GetStepName(), route)
		assert.Equal(t, string(aigateway.TargetEndpoint), open.GetTarget().GetKind(), route)
		assert.Equal(t, "team", open.GetTarget().GetName(), route)
		assert.Equal(t, http.MethodPost, open.GetMethod(), route)
		assert.Equal(t, "/v1/messages", open.GetPath(), route)
		assert.Equal(t, "beta=true", open.GetQuery(), route)
		assert.Equal(t, int64(len(body)), open.GetContentLength(), route)
		var headers []string
		for _, h := range open.GetHeaders() {
			headers = append(headers, h.GetName()+": "+h.GetValue())
		}
		assert.ElementsMatch(t, []string{"Content-Type: application/json", "Anthropic-Version: 2023-06-01"}, headers, route)
	}
}

// A refusal or a failure of the coordinator ends the call with its reason, at the
// start or while the response streams. A reason that the worker does not know is
// upstream_error.
func TestAIRelayClientRelayFailures(t *testing.T) {
	t.Parallel()

	refuse := func(msg *coordinatorv1.RelayServerMessage) func(coordinatorv1.CoordinatorService_RelayAIRequestServer) error {
		return func(stream coordinatorv1.CoordinatorService_RelayAIRequestServer) error {
			if _, err := stream.Recv(); err != nil {
				return err
			}
			return stream.Send(msg)
		}
	}
	tests := map[string]struct {
		relay   func(coordinatorv1.CoordinatorService_RelayAIRequestServer) error // nil leaves the call unimplemented
		reason  aigateway.Reason
		message string
	}{
		"refusal": {
			relay:   refuse(errorMessage(aigateway.ReasonEndpointForbidden, `the step may not use "team"`)),
			reason:  aigateway.ReasonEndpointForbidden,
			message: `the step may not use "team"`,
		},
		"unknown reason": {
			relay:   refuse(errorMessage("teapot", "short and stout")),
			reason:  aigateway.ReasonUpstreamError,
			message: "short and stout",
		},
		"status with a reason": {
			relay: func(stream coordinatorv1.CoordinatorService_RelayAIRequestServer) error {
				if _, err := stream.Recv(); err != nil {
					return err
				}
				return status.Error(codes.FailedPrecondition, string(aigateway.ReasonEndpointKeyMissing)+": the endpoint has no key")
			},
			reason:  aigateway.ReasonEndpointKeyMissing,
			message: "the endpoint has no key",
		},
		"no response": {
			relay: func(stream coordinatorv1.CoordinatorService_RelayAIRequestServer) error {
				_, err := stream.Recv()
				return err
			},
			reason:  aigateway.ReasonRelayUnavailable,
			message: "no response",
		},
		"coordinator with no relay": {
			reason:  aigateway.ReasonRelayUnavailable,
			message: "update the coordinator",
		},
		"failure after the head": {
			relay: func(stream coordinatorv1.CoordinatorService_RelayAIRequestServer) error {
				if _, err := stream.Recv(); err != nil {
					return err
				}
				if err := stream.Send(acceptedMessage(1024)); err != nil {
					return err
				}
				if _, _, err := receiveBody(stream); err != nil {
					return err
				}
				return sendAll(stream,
					headMessage(http.StatusOK),
					bodyMessage("data: {}\n"),
					errorMessage(aigateway.ReasonUpstreamTimeout, "no byte came for 1m0s"),
				)
			},
			reason:  aigateway.ReasonUpstreamTimeout,
			message: "no byte came for 1m0s",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client := newRelayClients(t, &relayCoordinator{relay: tt.relay})["any coordinator"]
			err := client.Relay(t.Context(), testRelayStep, aigateway.EndpointTarget("team"), jsonRequest(2), bytes.NewReader([]byte("{}")), &responseRecorder{})
			requireRelayFailure(t, err, tt.reason, tt.message)
		})
	}
}

// A body larger than the limit of the coordinator gives request_too_large, for a
// declared length and for a streamed body, and no byte of it goes up. The
// coordinator sees the end of the call.
func TestAIRelayClientRequestTooLarge(t *testing.T) {
	t.Parallel()

	for name, contentLength := range map[string]int64{"declared length": 16, "streamed body": -1} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			next := make(chan clientMessage, 1)
			client := newRelayClients(t, &relayCoordinator{
				relay: func(stream coordinatorv1.CoordinatorService_RelayAIRequestServer) error {
					if _, err := stream.Recv(); err != nil {
						return err
					}
					if err := stream.Send(acceptedMessage(8)); err != nil {
						return err
					}
					return receiveNext(stream, next)
				},
			})["any coordinator"]
			body := bytes.Repeat([]byte("x"), 16)
			err := client.Relay(t.Context(), testRelayStep, aigateway.EndpointTarget("team"), jsonRequest(contentLength), bytes.NewReader(body), &responseRecorder{})
			requireRelayFailure(t, err, aigateway.ReasonRequestTooLarge, "more than 8 bytes")
			requireCallEnd(t, next)
		})
	}
}

// A client that goes away ends the call with canceled, and the coordinator sees
// the end of the call, so that it stops the call to the provider.
func TestAIRelayClientCancel(t *testing.T) {
	t.Parallel()

	uploaded := make(chan struct{})
	next := make(chan clientMessage, 1)
	client := newRelayClients(t, &relayCoordinator{
		relay: func(stream coordinatorv1.CoordinatorService_RelayAIRequestServer) error {
			if _, err := stream.Recv(); err != nil {
				return err
			}
			if err := stream.Send(acceptedMessage(1024)); err != nil {
				return err
			}
			if _, _, err := receiveBody(stream); err != nil {
				return err
			}
			close(uploaded)
			return receiveNext(stream, next)
		},
	})["any coordinator"]

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- client.Relay(ctx, testRelayStep, aigateway.EndpointTarget("team"), jsonRequest(2), bytes.NewReader([]byte("{}")), &responseRecorder{})
	}()
	<-uploaded
	cancel()
	requireRelayFailure(t, <-result, aigateway.ReasonCanceled, "canceled")
	requireCallEnd(t, next)
}

// A message of the response larger than MaxMessageBytes ends the call with
// response_too_large.
func TestAIRelayClientResponseMessageTooLarge(t *testing.T) {
	t.Parallel()

	client := newRelayClients(t, &relayCoordinator{
		relay: func(stream coordinatorv1.CoordinatorService_RelayAIRequestServer) error {
			if _, err := stream.Recv(); err != nil {
				return err
			}
			if err := stream.Send(acceptedMessage(1024)); err != nil {
				return err
			}
			if _, _, err := receiveBody(stream); err != nil {
				return err
			}
			return sendAll(stream,
				headMessage(http.StatusOK),
				bodyMessage(string(bytes.Repeat([]byte("x"), aigateway.MaxMessageBytes))),
			)
		},
	})["any coordinator"]
	err := client.Relay(t.Context(), testRelayStep, aigateway.EndpointTarget("team"), jsonRequest(2), bytes.NewReader([]byte("{}")), &responseRecorder{})
	requireRelayFailure(t, err, aigateway.ReasonResponseTooLarge, "too large")
}

// A client that stops in the middle of its body still gets the answer of the
// coordinator: the call does not wait for a read of the body that only the
// client can end.
func TestAIRelayClientAnswersWhileTheBodyStalls(t *testing.T) {
	t.Parallel()

	client := newRelayClients(t, &relayCoordinator{
		relay: func(stream coordinatorv1.CoordinatorService_RelayAIRequestServer) error {
			if _, err := stream.Recv(); err != nil {
				return err
			}
			if err := stream.Send(acceptedMessage(1024)); err != nil {
				return err
			}
			return stream.Send(errorMessage(aigateway.ReasonUpstreamTimeout, "no byte came for 1m0s"))
		},
	})["any coordinator"]
	release := make(chan struct{})
	defer close(release)
	result := make(chan error, 1)
	go func() {
		result <- client.Relay(t.Context(), testRelayStep, aigateway.EndpointTarget("team"), jsonRequest(-1), stalledBody(release), &responseRecorder{})
	}()
	select {
	case err := <-result:
		requireRelayFailure(t, err, aigateway.ReasonUpstreamTimeout, "no byte came for 1m0s")
	case <-time.After(10 * time.Second):
		t.Fatal("the call waits for a body that does not move")
	}
}

// stalledBody is a body whose read blocks until release is closed.
type stalledBody chan struct{}

func (b stalledBody) Read([]byte) (int, error) {
	<-b
	return 0, io.EOF
}

// A send returns io.EOF when the coordinator has already ended the stream (see
// grpc.ClientStream.SendMsg). The call then reads the answer of the coordinator
// instead of reporting the failed send, before the open message and during the
// upload of the body.
func TestAIRelayClientReadsTheAnswerOfAnEndedStream(t *testing.T) {
	t.Parallel()

	t.Run("before the open", func(t *testing.T) {
		t.Parallel()

		stream := &endedStream{answers: make(chan *coordinatorv1.RelayServerMessage), status: status.Error(codes.Unimplemented, "unknown method")}
		close(stream.answers)
		client := coordinator.NewAIRelayClientOver(streamOpener{stream: stream}, serviceregistry.HostInfo{}, testRelayRun)
		err := client.Relay(t.Context(), testRelayStep, aigateway.EndpointTarget("team"), jsonRequest(2), bytes.NewReader([]byte("{}")), &responseRecorder{})
		requireRelayFailure(t, err, aigateway.ReasonRelayUnavailable, "update the coordinator")
	})

	t.Run("during the upload", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			stream := &endedStream{sends: 1, answers: make(chan *coordinatorv1.RelayServerMessage, 2)}
			stream.answers <- acceptedMessage(1024)
			client := coordinator.NewAIRelayClientOver(streamOpener{stream: stream}, serviceregistry.HostInfo{}, testRelayRun)
			result := make(chan error, 1)
			go func() {
				result <- client.Relay(t.Context(), testRelayStep, aigateway.EndpointTarget("team"), jsonRequest(2), bytes.NewReader([]byte("{}")), &responseRecorder{})
			}()
			// The upload has met the end of the stream. The answer of the
			// coordinator comes after it.
			synctest.Wait()
			stream.answers <- errorMessage(aigateway.ReasonEndpointUnavailable, "the provider is down")
			requireRelayFailure(t, <-result, aigateway.ReasonEndpointUnavailable, "the provider is down")
		})
	})
}

// relayCoordinator answers the calls of the relay with the functions of a test.
// A nil function leaves the call unimplemented, as on a coordinator with no relay.
type relayCoordinator struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer

	check func(context.Context, *coordinatorv1.CheckAIEndpointRequest) (*coordinatorv1.CheckAIEndpointResponse, error)
	relay func(coordinatorv1.CoordinatorService_RelayAIRequestServer) error
}

func (c *relayCoordinator) CheckAIEndpoint(ctx context.Context, req *coordinatorv1.CheckAIEndpointRequest) (*coordinatorv1.CheckAIEndpointResponse, error) {
	if c.check == nil {
		return c.UnimplementedCoordinatorServiceServer.CheckAIEndpoint(ctx, req)
	}
	return c.check(ctx, req)
}

func (c *relayCoordinator) RelayAIRequest(stream coordinatorv1.CoordinatorService_RelayAIRequestServer) error {
	if c.relay == nil {
		return c.UnimplementedCoordinatorServiceServer.RelayAIRequest(stream)
	}
	return c.relay(stream)
}

// newRelayClients starts the coordinator and returns the relay clients of a
// worker by route: one names the coordinator as the owner of the task, the other
// finds it through discovery.
func newRelayClients(t *testing.T, service *relayCoordinator) map[string]aigateway.Client {
	t.Helper()

	server, addr := startMockServer(t, service)
	t.Cleanup(server.Stop)
	host, port := parseHostPort(addr)
	member := serviceregistry.HostInfo{ID: "coord-1", Host: host, Port: port, Status: serviceregistry.ServiceStatusActive}
	client := coordinator.New(&mockServiceMonitor{members: []serviceregistry.HostInfo{member}}, coordinator.DefaultConfig())
	clients := map[string]aigateway.Client{
		"owner":           coordinator.NewAIRelayClient(client, member, testRelayRun),
		"any coordinator": coordinator.NewAIRelayClient(client, serviceregistry.HostInfo{}, testRelayRun),
	}
	for route, relay := range clients {
		require.NotNil(t, relay, route)
	}
	return clients
}

func requireRelayFailure(t *testing.T, err error, reason aigateway.Reason, message string) {
	t.Helper()

	var failure *aigateway.Failure
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, reason, failure.Reason, failure.Error())
	assert.Contains(t, failure.Message, message)
}

// clientMessage is what the coordinator receives after the open message: a
// message or the end of the stream.
type clientMessage struct {
	msg *coordinatorv1.RelayClientMessage
	err error
}

func receiveNext(stream coordinatorv1.CoordinatorService_RelayAIRequestServer, next chan<- clientMessage) error {
	msg, err := stream.Recv()
	next <- clientMessage{msg: msg, err: err}
	return err
}

// requireCallEnd checks that the coordinator saw the end of the call and no body.
// The client sends a cancel and then ends the stream. On the coordinator, the
// stream can end before the cancel is read: either one ends the call.
func requireCallEnd(t *testing.T, next <-chan clientMessage) {
	t.Helper()

	select {
	case got := <-next:
		assert.Nil(t, got.msg.GetBody(), "no body goes up")
		assert.True(t, got.err != nil || got.msg.GetCancel() != nil, "the coordinator sees the end of the call")
	case <-time.After(10 * time.Second):
		t.Fatal("the coordinator did not see the end of the call")
	}
}

func jsonRequest(contentLength int64) aigateway.HTTPRequest {
	return aigateway.HTTPRequest{
		Method:        http.MethodPost,
		Path:          "/v1/messages",
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		ContentLength: contentLength,
	}
}

// receiveBody reads the chunks of a request body up to the end flag. It returns
// the body and the size of its largest chunk.
func receiveBody(stream coordinatorv1.CoordinatorService_RelayAIRequestServer) ([]byte, int, error) {
	var body []byte
	largest := 0
	for {
		msg, err := stream.Recv()
		if err != nil {
			return nil, 0, err
		}
		chunk := msg.GetBody()
		if chunk == nil {
			return nil, 0, fmt.Errorf("expected a body chunk, got %v", msg)
		}
		body = append(body, chunk.GetData()...)
		largest = max(largest, len(chunk.GetData()))
		if chunk.GetEnd() {
			return body, largest, nil
		}
	}
}

func sendAll(stream coordinatorv1.CoordinatorService_RelayAIRequestServer, msgs ...*coordinatorv1.RelayServerMessage) error {
	for _, msg := range msgs {
		if err := stream.Send(msg); err != nil {
			return err
		}
	}
	return nil
}

func acceptedMessage(maxRequest int64) *coordinatorv1.RelayServerMessage {
	return &coordinatorv1.RelayServerMessage{Kind: &coordinatorv1.RelayServerMessage_Accepted{
		Accepted: &coordinatorv1.RelayAccepted{MaxRequestBytes: maxRequest},
	}}
}

// headMessage returns the head of a response with the header pairs.
func headMessage(code int, pairs ...string) *coordinatorv1.RelayServerMessage {
	head := &coordinatorv1.RelayResponseHead{Status: int32(code)} //nolint:gosec // an HTTP status
	for i := 0; i+1 < len(pairs); i += 2 {
		head.Headers = append(head.Headers, &coordinatorv1.RelayHeader{Name: pairs[i], Value: pairs[i+1]})
	}
	return &coordinatorv1.RelayServerMessage{Kind: &coordinatorv1.RelayServerMessage_Head{Head: head}}
}

func bodyMessage(data string) *coordinatorv1.RelayServerMessage {
	return &coordinatorv1.RelayServerMessage{Kind: &coordinatorv1.RelayServerMessage_Body{
		Body: &coordinatorv1.RelayBody{Data: []byte(data)},
	}}
}

func endMessage() *coordinatorv1.RelayServerMessage {
	return &coordinatorv1.RelayServerMessage{Kind: &coordinatorv1.RelayServerMessage_End{End: &coordinatorv1.RelayEnd{}}}
}

func errorMessage(reason aigateway.Reason, message string) *coordinatorv1.RelayServerMessage {
	return &coordinatorv1.RelayServerMessage{Kind: &coordinatorv1.RelayServerMessage_Error{
		Error: &coordinatorv1.RelayError{Reason: string(reason), Message: message},
	}}
}

// responseRecorder keeps the response that the client gives to the relay.
type responseRecorder struct {
	status int
	header http.Header
	body   bytes.Buffer
}

func (r *responseRecorder) Head(_ context.Context, status int, header http.Header) error {
	r.status, r.header = status, header
	return nil
}

func (r *responseRecorder) Write(_ context.Context, chunk []byte) error {
	r.body.Write(chunk)
	return nil
}

// streamOpener opens the same relay stream for each call.
type streamOpener struct {
	stream *endedStream
}

func (o streamOpener) CheckAIEndpointTo(context.Context, serviceregistry.HostInfo, *coordinatorv1.CheckAIEndpointRequest) (*coordinatorv1.CheckAIEndpointResponse, error) {
	return nil, status.Error(codes.Unimplemented, "unknown method")
}

func (o streamOpener) RelayAIRequestTo(ctx context.Context, _ serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_RelayAIRequestClient, error) {
	o.stream.ctx = ctx
	return o.stream, nil
}

// endedStream is a relay stream that the coordinator ends after the first sends
// sends: each later send returns io.EOF, as gRPC does. Recv gives the answers of
// the coordinator, then its status once answers is closed, and ends with the
// context of the stream.
type endedStream struct {
	grpc.ClientStream

	ctx     context.Context
	sends   int
	answers chan *coordinatorv1.RelayServerMessage
	status  error
}

func (s *endedStream) Send(*coordinatorv1.RelayClientMessage) error {
	if s.sends == 0 {
		return io.EOF
	}
	s.sends--
	return nil
}

func (s *endedStream) Recv() (*coordinatorv1.RelayServerMessage, error) {
	select {
	case <-s.ctx.Done():
		return nil, status.FromContextError(s.ctx.Err()).Err()
	case msg, ok := <-s.answers:
		if !ok {
			return nil, s.status
		}
		return msg, nil
	}
}
