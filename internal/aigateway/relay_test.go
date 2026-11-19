// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aigateway_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
)

// fakeClient is the Client behind a relay. It records the requests.
type fakeClient struct {
	mu       sync.Mutex
	requests []aigateway.HTTPRequest
	bodies   []string
	steps    []aigateway.StepRef
	targets  []aigateway.Target
	handler  func(ctx context.Context, body io.Reader, sink aigateway.ResponseSink) error
}

func (f *fakeClient) Check(context.Context, aigateway.StepRef, aigateway.Target, string) (aigateway.CheckResult, error) {
	return aigateway.CheckResult{Protocol: aiendpoint.ProtocolAnthropic}, nil
}

func (f *fakeClient) Relay(ctx context.Context, step aigateway.StepRef, target aigateway.Target, req aigateway.HTTPRequest, body io.Reader, sink aigateway.ResponseSink) error {
	data, _ := io.ReadAll(body)
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.bodies = append(f.bodies, string(data))
	f.steps = append(f.steps, step)
	f.targets = append(f.targets, target)
	f.mu.Unlock()
	if f.handler != nil {
		return f.handler(ctx, bytes.NewReader(data), sink)
	}
	_ = sink.Head(ctx, 200, http.Header{"Content-Type": {"application/json"}})
	return sink.Write(ctx, []byte(`{"ok":true}`))
}

func (f *fakeClient) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func startRelay(t *testing.T, client aigateway.Client, log io.Writer) *aigateway.Relay {
	t.Helper()
	relay, err := aigateway.StartRelay(context.Background(), aigateway.RelayOptions{
		Client: client, Step: aigateway.StepRef{RunID: "run-1", DAGName: "fix-bug", StepName: "code"},
		Endpoint: "cle1", Protocol: aiendpoint.ProtocolAnthropic, Log: log,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = relay.Close() })
	return relay
}

func do(t *testing.T, relay *aigateway.Relay, method, path, body string, header http.Header) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, relay.URL()+path, strings.NewReader(body))
	require.NoError(t, err)
	maps.Copy(req.Header, header)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	require.NoError(t, err)
	return resp
}

func withSecret(relay *aigateway.Relay) http.Header {
	return http.Header{"X-Api-Key": {relay.Secret()}, "Content-Type": {"application/json"}}
}

func TestRelay_ForwardsAnAllowedRequest(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	relay := startRelay(t, client, nil)
	assert.True(t, strings.HasPrefix(relay.URL(), "http://127.0.0.1:"))
	assert.NotContains(t, relay.URL(), relay.Secret())
	assert.GreaterOrEqual(t, len(relay.Secret()), 43, "32 random bytes")

	header := withSecret(relay)
	header.Set("X-Evil", "1")
	header.Set("Anthropic-Version", "2023-06-01")
	resp := do(t, relay, http.MethodPost, "/v1/messages?beta=true", `{"a":1}`, header)
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, `{"ok":true}`, string(data))

	require.Equal(t, 1, client.count())
	req := client.requests[0]
	assert.Equal(t, "/v1/messages", req.Path)
	assert.Equal(t, "beta=true", req.RawQuery)
	assert.Equal(t, `{"a":1}`, client.bodies[0])
	assert.Empty(t, req.Header.Get("X-Evil"))
	assert.Empty(t, req.Header.Get("X-Api-Key"), "the relay secret never leaves the worker")
	assert.Equal(t, "2023-06-01", req.Header.Get("Anthropic-Version"))
	assert.Equal(t, "fix-bug", client.steps[0].DAGName)
	assert.Equal(t, "code", client.steps[0].StepName)
}

func TestRelay_AcceptsTheSecretAsABearerToken(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	relay := startRelay(t, client, nil)
	resp := do(t, relay, http.MethodPost, "/v1/messages", `{}`, http.Header{"Authorization": {"Bearer " + relay.Secret()}})
	_ = resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
}

func TestRelay_RefusesAWrongOrMissingSecret(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	var log bytes.Buffer
	relay := startRelay(t, client, &syncBuffer{b: &log})
	other := startRelay(t, client, nil)

	for name, header := range map[string]http.Header{
		"none":                     {},
		"wrong":                    {"X-Api-Key": {"sk-ant-not-the-secret"}},
		"the secret of another":    {"X-Api-Key": {other.Secret()}},
		"basic scheme":             {"Authorization": {"Basic " + relay.Secret()}},
		"secret in two headers":    {"X-Api-Key": {relay.Secret()}, "Authorization": {"Bearer " + relay.Secret()}},
		"prefix of the secret":     {"X-Api-Key": {relay.Secret()[:20]}},
		"secret with extra suffix": {"X-Api-Key": {relay.Secret() + "x"}},
	} {
		resp := do(t, relay, http.MethodPost, "/v1/messages", `{}`, header)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, 401, resp.StatusCode, name)
		assert.Contains(t, string(body), "relay_unauthorized", name)
		assert.Contains(t, string(body), `"type":"error"`, "the error format of the protocol")
	}
	assert.Zero(t, client.count())
	assert.Contains(t, log.String(), "relay_unauthorized")
	assert.NotContains(t, log.String(), relay.Secret())
}

type syncBuffer struct {
	mu sync.Mutex
	b  *bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func TestRelay_RefusesPathMethodAndQuery(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	relay := startRelay(t, client, nil)
	tests := []struct {
		name, method, path string
		status             int
		reason             string
	}{
		{"other path", "POST", "/v1/complete", 404, "path_not_allowed"},
		{"admin", "GET", "/admin", 404, "path_not_allowed"},
		{"openai path", "POST", "/v1/chat/completions", 404, "path_not_allowed"},
		{"other method", "GET", "/v1/messages", 405, "method_not_allowed"},
		{"delete", "DELETE", "/v1/messages", 405, "method_not_allowed"},
		{"other query", "POST", "/v1/messages?api_key=x", 404, "path_not_allowed"},
		{"traversal", "POST", "/v1/messages/../models", 404, "path_not_allowed"},
	}
	for _, tc := range tests {
		resp := do(t, relay, tc.method, tc.path, "{}", withSecret(relay))
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, tc.status, resp.StatusCode, tc.name)
		assert.Contains(t, string(body), tc.reason, tc.name)
	}
	resp := do(t, relay, "POST", "/v1/messages", "{}", func() http.Header {
		h := withSecret(relay)
		h.Set("Content-Encoding", "gzip")
		return h
	}())
	_ = resp.Body.Close()
	assert.Equal(t, 403, resp.StatusCode, "a request with content-encoding is refused")
	assert.Zero(t, client.count(), "no refused request reaches the client")
}

// raw sends the bytes to the relay on a TCP connection and reads the answer.
func raw(t *testing.T, relay *aigateway.Relay, request string) (int, string) {
	t.Helper()
	addr := strings.TrimPrefix(relay.URL(), "http://")
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, err = io.WriteString(conn, request)
	require.NoError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestRelay_RefusesHostAndRequestForm(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	relay := startRelay(t, client, nil)
	addr := strings.TrimPrefix(relay.URL(), "http://")
	_, port, _ := net.SplitHostPort(addr)
	auth := "X-Api-Key: " + relay.Secret() + "\r\n"

	status, body := raw(t, relay, "POST /v1/messages HTTP/1.1\r\nHost: evil.example\r\n"+auth+"Content-Length: 2\r\n\r\n{}")
	assert.Equal(t, 403, status)
	assert.Contains(t, body, "host_not_allowed")

	status, body = raw(t, relay, "POST /v1/messages HTTP/1.1\r\nHost: 127.0.0.1\r\n"+auth+"Content-Length: 2\r\n\r\n{}")
	assert.Equal(t, 403, status, "a Host header with no port is not the address of the relay")
	assert.Contains(t, body, "host_not_allowed")

	status, body = raw(t, relay, "POST http://api.anthropic.com/v1/messages HTTP/1.1\r\nHost: api.anthropic.com\r\n"+auth+"Content-Length: 2\r\n\r\n{}")
	assert.Equal(t, 403, status, "a request in absolute form")
	assert.Contains(t, body, "host_not_allowed")

	status, body = raw(t, relay, "POST http://127.0.0.1:"+port+"/v1/messages HTTP/1.1\r\nHost: 127.0.0.1:"+port+"\r\n"+auth+"Content-Length: 2\r\n\r\n{}")
	assert.Equal(t, 403, status, "the absolute form is refused also for the relay itself")
	assert.Contains(t, body, "host_not_allowed")

	status, body = raw(t, relay, "CONNECT api.anthropic.com:443 HTTP/1.1\r\nHost: api.anthropic.com:443\r\n"+auth+"\r\n")
	assert.Equal(t, 403, status)
	assert.Contains(t, body, "host_not_allowed")

	status, _ = raw(t, relay, "POST /v1/messages HTTP/1.1\r\nHost: localhost:"+port+"\r\n"+auth+"Content-Length: 2\r\n\r\n{}")
	assert.Equal(t, 200, status, "localhost with the port is the relay")
	status, _ = raw(t, relay, "POST /v1/messages HTTP/1.1\r\nHost: 127.0.0.1:"+port+"\r\n"+auth+"Content-Length: 2\r\n\r\n{}")
	assert.Equal(t, 200, status)
	assert.Equal(t, 2, client.count())
}

func TestRelay_EachAttemptHasItsOwnSecretAndPort(t *testing.T) {
	t.Parallel()
	a := startRelay(t, &fakeClient{}, nil)
	b := startRelay(t, &fakeClient{}, nil)
	assert.NotEqual(t, a.Secret(), b.Secret())
	assert.NotEqual(t, a.URL(), b.URL())
}

func TestRelay_StreamsEachChunkAtOnce(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	client := &fakeClient{handler: func(ctx context.Context, _ io.Reader, sink aigateway.ResponseSink) error {
		_ = sink.Head(ctx, 200, http.Header{"Content-Type": {"text/event-stream"}})
		_ = sink.Write(ctx, []byte("event: a\n\n"))
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		return sink.Write(ctx, []byte("event: b\n\n"))
	}}
	relay := startRelay(t, client, nil)
	req, _ := http.NewRequest(http.MethodPost, relay.URL()+"/v1/messages", strings.NewReader("{}"))
	req.Header = withSecret(relay)
	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)
	first := make(chan string, 1)
	go func() {
		line, _ := reader.ReadString('\n')
		first <- line
	}()
	select {
	case line := <-first:
		assert.Equal(t, "event: a\n", line)
	case <-time.After(3 * time.Second):
		t.Fatal("the first event did not reach the client before the end of the response")
	}
	close(release)
	rest, _ := io.ReadAll(reader)
	assert.Contains(t, string(rest), "event: b")
}

func TestRelay_FailureBecomesAnErrorResponse(t *testing.T) {
	t.Parallel()
	var log bytes.Buffer
	client := &fakeClient{handler: func(context.Context, io.Reader, aigateway.ResponseSink) error {
		return aigateway.Fail(aigateway.ReasonEndpointForbidden, "the DAG is not in the allow list")
	}}
	relay := startRelay(t, client, &syncBuffer{b: &log})
	resp := do(t, relay, http.MethodPost, "/v1/messages", `{}`, withSecret(relay))
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, 403, resp.StatusCode)
	assert.Contains(t, string(body), "endpoint_forbidden")
	assert.Contains(t, log.String(), "ai relay: refuse POST /v1/messages: endpoint_forbidden")
}

// Any local process can connect to the port of the relay. A request with no
// secret, or with a path that holds a new line, does not change the reason of the
// attempt and adds no line to the step log.
func TestRelay_RequestWithNoSecretKeepsTheReasonOfTheAttempt(t *testing.T) {
	t.Parallel()
	var log bytes.Buffer
	client := &fakeClient{handler: func(context.Context, io.Reader, aigateway.ResponseSink) error {
		return aigateway.Fail(aigateway.ReasonModelUnknown, "the models list lacks the model")
	}}
	relay := startRelay(t, client, &syncBuffer{b: &log})
	resp := do(t, relay, http.MethodPost, "/v1/messages", `{}`, withSecret(relay))
	_ = resp.Body.Close()
	resp = do(t, relay, http.MethodPost, "/v1/messages", `{}`, nil)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.NotNil(t, relay.LastRefusal())
	assert.Equal(t, aigateway.ReasonModelUnknown, relay.LastRefusal().Reason)

	resp = do(t, relay, http.MethodPost, "/v1/x%0Aai relay: forged", `{}`, withSecret(relay))
	_ = resp.Body.Close()
	assert.NotContains(t, log.String(), "\nai relay: forged")
}

// Claude Code repeats a request that gets a 401 until its time ends, unless the answer
// has the header x-should-retry with the value false (measured with Claude Code 2.1.311).
// A refusal that no later request can change carries the header. A refusal that can pass
// later does not.
func TestRelay_RefusalHasTheRetryHeader(t *testing.T) {
	t.Parallel()
	for reason, retry := range map[aigateway.Reason]string{
		aigateway.ReasonEndpointForbidden:   "false",
		aigateway.ReasonEndpointUnknown:     "false",
		aigateway.ReasonEndpointKeyMissing:  "false",
		aigateway.ReasonRequestTooLarge:     "false",
		aigateway.ReasonTooManyStreams:      "",
		aigateway.ReasonRelayUnavailable:    "",
		aigateway.ReasonUpstreamError:       "",
		aigateway.ReasonUpstreamTimeout:     "",
		aigateway.ReasonEndpointUnavailable: "",
	} {
		client := &fakeClient{handler: func(context.Context, io.Reader, aigateway.ResponseSink) error {
			return aigateway.Fail(reason, "no")
		}}
		relay := startRelay(t, client, nil)
		resp := do(t, relay, http.MethodPost, "/v1/messages", `{}`, withSecret(relay))
		_ = resp.Body.Close()
		assert.Equal(t, retry, resp.Header.Get("X-Should-Retry"), string(reason))
	}

	// A wrong secret is refused before the gateway. It has the header too.
	relay := startRelay(t, &fakeClient{}, nil)
	resp := do(t, relay, http.MethodPost, "/v1/messages", `{}`, http.Header{"X-Api-Key": {"wrong"}})
	_ = resp.Body.Close()
	assert.Equal(t, 401, resp.StatusCode)
	assert.Equal(t, "false", resp.Header.Get("X-Should-Retry"))
}

func TestRelay_CutResponseBreaksTheConnection(t *testing.T) {
	t.Parallel()
	client := &fakeClient{handler: func(ctx context.Context, _ io.Reader, sink aigateway.ResponseSink) error {
		_ = sink.Head(ctx, 200, http.Header{"Content-Type": {"text/event-stream"}})
		_ = sink.Write(ctx, []byte("data: 1\n\n"))
		return aigateway.Fail(aigateway.ReasonResponseTooLarge, "cut")
	}}
	relay := startRelay(t, client, nil)
	resp := do(t, relay, http.MethodPost, "/v1/messages", `{}`, withSecret(relay))
	defer func() { _ = resp.Body.Close() }()
	_, err := io.ReadAll(resp.Body)
	assert.Error(t, err, "the client sees a broken stream, not a clean end")
}

func TestRelay_ClientDisconnectCancelsTheCall(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	canceled := make(chan struct{})
	client := &fakeClient{handler: func(ctx context.Context, _ io.Reader, sink aigateway.ResponseSink) error {
		_ = sink.Head(ctx, 200, http.Header{"Content-Type": {"text/event-stream"}})
		_ = sink.Write(ctx, []byte("data: 1\n\n"))
		close(started)
		<-ctx.Done()
		close(canceled)
		return aigateway.Fail(aigateway.ReasonCanceled, "gone")
	}}
	relay := startRelay(t, client, nil)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, relay.URL()+"/v1/messages", strings.NewReader("{}"))
	req.Header = withSecret(relay)
	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	<-started
	cancel()
	_ = resp.Body.Close()
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("the call did not end when the client went away")
	}
}

func TestRelay_CloseEndsOpenRequestsAndTheListener(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	ended := make(chan struct{})
	client := &fakeClient{handler: func(ctx context.Context, _ io.Reader, sink aigateway.ResponseSink) error {
		_ = sink.Head(ctx, 200, nil)
		_ = sink.Write(ctx, []byte("x"))
		close(started)
		<-ctx.Done()
		close(ended)
		return aigateway.Fail(aigateway.ReasonCanceled, "closed")
	}}
	relay, err := aigateway.StartRelay(context.Background(), aigateway.RelayOptions{
		Client: client, Endpoint: "cle1", Protocol: aiendpoint.ProtocolAnthropic,
	})
	require.NoError(t, err)
	addr := strings.TrimPrefix(relay.URL(), "http://")
	req, _ := http.NewRequest(http.MethodPost, relay.URL()+"/v1/messages", strings.NewReader("{}"))
	req.Header = withSecret(relay)
	go func() {
		if resp, err := (&http.Client{}).Do(req); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	<-started
	require.NoError(t, relay.Close())
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not end the open request")
	}
	_, err = net.DialTimeout("tcp", addr, time.Second)
	assert.Error(t, err, "the listener is closed")
	require.NoError(t, relay.Close(), "Close can run twice")
}

func TestRelay_EndsWithTheContextOfTheStep(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	relay, err := aigateway.StartRelay(ctx, aigateway.RelayOptions{Client: &fakeClient{}, Protocol: aiendpoint.ProtocolAnthropic})
	require.NoError(t, err)
	t.Cleanup(func() { _ = relay.Close() })
	cancel()
	if status := statusAfterStop(relay); status != 0 {
		assert.Equal(t, 401, status, "a stopped attempt gives no access")
	}
}

// statusAfterStop returns the status that the relay gives after the end of its context,
// or 0 when the connection fails.
func statusAfterStop(relay *aigateway.Relay) int {
	time.Sleep(50 * time.Millisecond)
	req, _ := http.NewRequest(http.MethodPost, relay.URL()+"/v1/messages", strings.NewReader("{}"))
	req.Header.Set("X-Api-Key", relay.Secret())
	resp, err := (&http.Client{Timeout: time.Second}).Do(req)
	if err != nil {
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestRelay_NeedsAClient(t *testing.T) {
	t.Parallel()
	_, err := aigateway.StartRelay(context.Background(), aigateway.RelayOptions{})
	require.Error(t, err)
	_ = fmt.Sprint
}
