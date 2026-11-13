// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aigateway_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/runtime/egress"
)

const realKey = "sk-real-0123456789-SECRET"

// fakeSource is an EndpointSource with one endpoint.
type fakeSource struct {
	mu      sync.Mutex
	ep      aiendpoint.Endpoint
	key     string
	resolve error
	readErr error
}

func (f *fakeSource) ResolveEndpoint(_ context.Context, name, _, _ string) (aiendpoint.Endpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.resolve != nil {
		return aiendpoint.Endpoint{}, f.resolve
	}
	if name != f.ep.Name {
		return aiendpoint.Endpoint{}, &aiendpoint.Error{Reason: aiendpoint.ReasonUnknown, Name: name}
	}
	return f.ep, nil
}

func (f *fakeSource) ReadKey(context.Context, aiendpoint.Endpoint) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.key, f.readErr
}

func (f *fakeSource) setKey(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.key = key
}

// recSink records the response and the time of each chunk.
type recSink struct {
	mu     sync.Mutex
	status int
	header http.Header
	chunks [][]byte
	times  []time.Time
	onChnk func(n int)
}

func (s *recSink) Head(_ context.Context, status int, header http.Header) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.header = status, header
	return nil
}

func (s *recSink) Write(_ context.Context, chunk []byte) error {
	s.mu.Lock()
	s.chunks = append(s.chunks, append([]byte(nil), chunk...))
	s.times = append(s.times, time.Now())
	n := len(s.chunks)
	cb := s.onChnk
	s.mu.Unlock()
	if cb != nil {
		cb(n)
	}
	return nil
}

func (s *recSink) body() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(bytes.Join(s.chunks, nil))
}

func testLimits() aigateway.Limits {
	return aigateway.Limits{
		MaxRequestBytes:       1 << 20,
		MaxResponseBytes:      1 << 20,
		MaxHeaderBytes:        4096,
		MaxStreamsPerRun:      2,
		MaxStreams:            8,
		ConnectTimeout:        2 * time.Second,
		ResponseHeaderTimeout: 2 * time.Second,
		IdleTimeout:           2 * time.Second,
		TotalTimeout:          10 * time.Second,
	}
}

func newGateway(t *testing.T, src *fakeSource, limits aigateway.Limits, audit aigateway.AuditFunc) *aigateway.Gateway {
	t.Helper()
	g, err := aigateway.New(aigateway.Config{Endpoints: src, Limits: limits, Audit: audit})
	require.NoError(t, err)
	return g
}

func endpointFor(url string, protocol aiendpoint.Protocol, auth aiendpoint.Auth) aiendpoint.Endpoint {
	return aiendpoint.Endpoint{
		Name: "cle1", Preset: "custom", Protocol: protocol, BaseURL: url, Auth: auth,
		SecretID: "secret-1", KeyFingerprint: "0123456789ab",
	}
}

func request(path string, body string) aigateway.Request {
	method := http.MethodPost
	if body == "" {
		method = http.MethodGet
	}
	return aigateway.Request{
		Subject:       aigateway.Subject{RunID: "run-1", DAGName: "fix-bug", StepName: "code", WorkerID: "w1", AttemptKey: "key-1"},
		Target:        aigateway.EndpointTarget("cle1"),
		Method:        method,
		Path:          path,
		Header:        http.Header{"Content-Type": {"application/json"}},
		ContentLength: int64(len(body)),
		Body:          strings.NewReader(body),
	}
}

func TestDo_AuthForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		auth     aiendpoint.Auth
		wantKey  string // the header that holds the key
		wantHdr  string // the value of that header
		wantBeta string
	}{
		{"x-api-key", aiendpoint.AuthXAPIKey, "X-Api-Key", realKey, ""},
		{"bearer", aiendpoint.AuthBearer, "Authorization", "Bearer " + realKey, ""},
		{"oauth", aiendpoint.AuthAnthropicOAuth, "Authorization", "Bearer " + realKey, "client-beta," + egress.AnthropicOAuthBeta},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got http.Header
			var gotPath, gotQuery, gotBody string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Clone()
				gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
				data, _ := io.ReadAll(r.Body)
				gotBody = string(data)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Set-Cookie", "a=b")
				w.Header().Set("Retry-After", "7")
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer upstream.Close()

			src := &fakeSource{ep: endpointFor(upstream.URL+"/base", aiendpoint.ProtocolAnthropic, tc.auth), key: realKey}
			var records []aigateway.AuditRecord
			g := newGateway(t, src, testLimits(), func(_ context.Context, r aigateway.AuditRecord) { records = append(records, r) })

			req := request("/v1/messages", `{"model":"m"}`)
			req.RawQuery = "beta=true"
			req.Header.Set("X-Api-Key", "placeholder-from-client")
			req.Header.Set("Authorization", "Bearer placeholder-from-client")
			req.Header.Set("Proxy-Authorization", "Basic x")
			req.Header.Set("X-Evil", "1")
			req.Header.Set("Anthropic-Version", "2023-06-01")
			req.Header.Set("Anthropic-Beta", "client-beta")
			sink := &recSink{}
			require.Nil(t, g.Do(context.Background(), req, sink))

			assert.Equal(t, "/base/v1/messages", gotPath)
			assert.Equal(t, "beta=true", gotQuery)
			assert.Equal(t, `{"model":"m"}`, gotBody)
			assert.Equal(t, "identity", got.Get("Accept-Encoding"))
			assert.Equal(t, "2023-06-01", got.Get("Anthropic-Version"))
			assert.Empty(t, got.Get("X-Evil"), "a header outside the allow list does not reach the provider")
			assert.Empty(t, got.Get("Proxy-Authorization"))
			assert.NotContains(t, strings.Join(got.Values("X-Api-Key"), ","), "placeholder")
			assert.NotContains(t, strings.Join(got.Values("Authorization"), ","), "placeholder")
			assert.Equal(t, tc.wantHdr, got.Get(tc.wantKey))
			if tc.wantBeta != "" {
				assert.Equal(t, tc.wantBeta, got.Get("Anthropic-Beta"))
			} else {
				assert.Equal(t, "client-beta", got.Get("Anthropic-Beta"))
			}

			assert.Equal(t, http.StatusOK, sink.status)
			assert.Equal(t, `{"ok":true}`, sink.body())
			assert.Equal(t, "7", sink.header.Get("Retry-After"))
			assert.Empty(t, sink.header.Get("Set-Cookie"))

			require.Len(t, records, 1)
			r := records[0]
			assert.Equal(t, "messages", r.Route)
			assert.Equal(t, http.StatusOK, r.Status)
			assert.Equal(t, "0123456789ab", r.Fingerprint)
			assert.Equal(t, int64(len(`{"model":"m"}`)), r.RequestBytes)
			assert.Equal(t, "run-1", r.RunID)
			assert.NotContains(t, strings.Join([]string{r.Name, r.Route, r.Method, r.Fingerprint, r.StepName, r.DAGName}, " "), realKey)
		})
	}
}

func TestDo_OpenAIPathDropsV1(t *testing.T) {
	t.Parallel()
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
	}))
	defer upstream.Close()
	src := &fakeSource{ep: endpointFor(upstream.URL+"/openai/v1", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer), key: realKey}
	g := newGateway(t, src, testLimits(), nil)
	require.Nil(t, g.Do(context.Background(), request("/v1/chat/completions", `{}`), &recSink{}))
	assert.Equal(t, "/openai/v1/chat/completions", gotPath)
}

func TestDo_UpstreamStatusIsNeverChanged(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusInternalServerError} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "30")
			w.Header().Set("Anthropic-Ratelimit-Requests-Remaining", "0")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"x"}`))
		}))
		src := &fakeSource{ep: endpointFor(upstream.URL, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: realKey}
		g := newGateway(t, src, testLimits(), nil)
		sink := &recSink{}
		require.Nil(t, g.Do(context.Background(), request("/v1/messages", `{}`), sink))
		assert.Equal(t, status, sink.status)
		assert.Equal(t, "30", sink.header.Get("Retry-After"))
		assert.Equal(t, "0", sink.header.Get("Anthropic-Ratelimit-Requests-Remaining"))
		upstream.Close()
	}
}

func TestDo_RefusalsNeverCallTheProvider(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	good := endpointFor(upstream.URL, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey)

	tests := []struct {
		name   string
		src    *fakeSource
		mutate func(*aigateway.Request)
		want   aigateway.Reason
	}{
		{"forbidden", &fakeSource{ep: good, key: realKey, resolve: &aiendpoint.Error{Reason: aiendpoint.ReasonForbidden, Name: "cle1", Detail: "the DAG is not in the allow list"}}, nil, aigateway.ReasonEndpointForbidden},
		{"unknown", &fakeSource{ep: good, key: realKey}, func(r *aigateway.Request) { r.Target.Name = "other" }, aigateway.ReasonEndpointUnknown},
		{"unavailable", &fakeSource{ep: good, key: realKey, resolve: &aiendpoint.Error{Reason: aiendpoint.ReasonUnavailable, Name: "cle1"}}, nil, aigateway.ReasonEndpointUnavailable},
		{"key missing", &fakeSource{ep: good, readErr: &aiendpoint.Error{Reason: aiendpoint.ReasonKeyMissing, Name: "cle1"}}, nil, aigateway.ReasonEndpointKeyMissing},
		{"other path", &fakeSource{ep: good, key: realKey}, func(r *aigateway.Request) { r.Path = "/v1/complete" }, aigateway.ReasonPathNotAllowed},
		{"openai path on anthropic", &fakeSource{ep: good, key: realKey}, func(r *aigateway.Request) { r.Path = "/v1/chat/completions" }, aigateway.ReasonPathNotAllowed},
		{"other method", &fakeSource{ep: good, key: realKey}, func(r *aigateway.Request) { r.Method = http.MethodGet }, aigateway.ReasonMethodNotAllowed},
		{"other query", &fakeSource{ep: good, key: realKey}, func(r *aigateway.Request) { r.RawQuery = "api_key=x" }, aigateway.ReasonPathNotAllowed},
		{"content-encoding", &fakeSource{ep: good, key: realKey}, func(r *aigateway.Request) { r.Header.Set("Content-Encoding", "gzip") }, aigateway.ReasonHostNotAllowed},
		{"headers too large", &fakeSource{ep: good, key: realKey}, func(r *aigateway.Request) { r.Header.Set("User-Agent", strings.Repeat("a", 5000)) }, aigateway.ReasonRequestTooLarge},
		{"content-length too large", &fakeSource{ep: good, key: realKey}, func(r *aigateway.Request) { r.ContentLength = 2 << 20 }, aigateway.ReasonRequestTooLarge},
		{"body too large", &fakeSource{ep: good, key: realKey}, func(r *aigateway.Request) {
			r.ContentLength = -1
			r.Body = strings.NewReader(strings.Repeat("a", 1<<20+1))
		}, aigateway.ReasonRequestTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := newGateway(t, tc.src, testLimits(), nil)
			req := request("/v1/messages", `{}`)
			if tc.mutate != nil {
				tc.mutate(&req)
			}
			sink := &recSink{}
			failure := g.Do(context.Background(), req, sink)
			require.NotNil(t, failure)
			assert.Equal(t, tc.want, failure.Reason)
			assert.Zero(t, sink.status, "no head is sent")
			assert.NotContains(t, failure.Error(), realKey)
		})
	}
	assert.Zero(t, calls.Load(), "no refused request reaches the provider")
}

func TestDo_RedirectIsAnUpstreamError(t *testing.T) {
	t.Parallel()
	var elsewhere atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { elsewhere.Add(1) }))
	defer other.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()
	src := &fakeSource{ep: endpointFor(upstream.URL, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: realKey}
	g := newGateway(t, src, testLimits(), nil)
	sink := &recSink{}
	failure := g.Do(context.Background(), request("/v1/messages", `{}`), sink)
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonUpstreamError, failure.Reason)
	assert.Zero(t, elsewhere.Load(), "the redirect is not followed")
	assert.Zero(t, sink.status)
}

func TestDo_TLSVerificationIsOn(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer upstream.Close()
	src := &fakeSource{ep: endpointFor(upstream.URL, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: realKey}
	g := newGateway(t, src, testLimits(), nil)
	failure := g.Do(context.Background(), request("/v1/messages", `{}`), &recSink{})
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonUpstreamError, failure.Reason)
	assert.Contains(t, failure.Message, "certificate")
}

func TestDo_UsesTheConfiguredProxyOnly(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	var viaProxy atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		viaProxy.Add(1)
		assert.Equal(t, "provider.example", r.Host)
		_, _ = w.Write([]byte("proxied"))
	}))
	defer proxy.Close()
	src := &fakeSource{ep: endpointFor("http://provider.example", aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: realKey}
	g, err := aigateway.New(aigateway.Config{Endpoints: src, Limits: testLimits(), ProxyURL: proxy.URL})
	require.NoError(t, err)
	sink := &recSink{}
	require.Nil(t, g.Do(context.Background(), request("/v1/messages", `{}`), sink))
	assert.Equal(t, "proxied", sink.body())
	assert.Equal(t, int32(1), viaProxy.Load())

	// With no proxy URL, the environment is not read: the call goes direct and fails.
	direct, err := aigateway.New(aigateway.Config{Endpoints: &fakeSource{ep: endpointFor("http://127.0.0.1:1", aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: realKey}, Limits: testLimits()})
	require.NoError(t, err)
	failure := direct.Do(context.Background(), request("/v1/messages", `{}`), &recSink{})
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonUpstreamError, failure.Reason)
	assert.Equal(t, int32(1), viaProxy.Load())
}

func TestDo_StreamsChunkByChunk(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "event: ping\ndata: 1\n\n")
		flusher.Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "event: ping\ndata: 2\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()
	src := &fakeSource{ep: endpointFor(upstream.URL, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: realKey}
	g := newGateway(t, src, testLimits(), nil)

	first := make(chan struct{})
	sink := &recSink{onChnk: func(n int) {
		if n == 1 {
			close(first)
		}
	}}
	done := make(chan *aigateway.Failure, 1)
	go func() { done <- g.Do(context.Background(), request("/v1/messages", `{}`), sink) }()

	select {
	case <-first:
	case <-time.After(3 * time.Second):
		t.Fatal("the first event did not arrive before the upstream finished")
	}
	select {
	case failure := <-done:
		t.Fatalf("the call ended before the upstream released the second event: %v", failure)
	default:
	}
	close(release)
	require.Nil(t, <-done)
	assert.Equal(t, "event: ping\ndata: 1\n\nevent: ping\ndata: 2\n\n", sink.body())
	assert.GreaterOrEqual(t, len(sink.chunks), 2)
}

// The audit record has the time of the first byte of the response. For a stream the
// first byte comes long before the end, so the two times show that the response
// streams.
func TestDo_AuditHasTheFirstByteTime(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "event: ping\ndata: 1\n\n")
		flusher.Flush()
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, "event: ping\ndata: 2\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()
	src := &fakeSource{ep: endpointFor(upstream.URL, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: realKey}
	records := make(chan aigateway.AuditRecord, 1)
	g := newGateway(t, src, testLimits(), func(_ context.Context, r aigateway.AuditRecord) { records <- r })

	require.Nil(t, g.Do(context.Background(), request("/v1/messages", `{}`), &recSink{}))
	record := <-records
	assert.Less(t, record.FirstByteMs, int64(250), "the first byte leaves before the second event")
	assert.GreaterOrEqual(t, record.DurationMs, int64(300), "the response lasts until the second event")
}

func TestDo_ResponseTooLargeCutsTheStream(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for range 100 {
			_, _ = w.Write(bytes.Repeat([]byte("x"), 1024))
		}
	}))
	defer upstream.Close()
	limits := testLimits()
	limits.MaxResponseBytes = 10 * 1024
	src := &fakeSource{ep: endpointFor(upstream.URL, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: realKey}
	g := newGateway(t, src, limits, nil)
	sink := &recSink{}
	failure := g.Do(context.Background(), request("/v1/messages", `{}`), sink)
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonResponseTooLarge, failure.Reason)
	assert.Equal(t, http.StatusOK, sink.status, "the head was sent before the cut")
	assert.LessOrEqual(t, len(sink.body()), 10*1024)
}

func TestDo_Timeouts(t *testing.T) {
	t.Parallel()

	t.Run("response header", func(t *testing.T) {
		t.Parallel()
		upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		}))
		defer upstream.Close()
		limits := testLimits()
		limits.ResponseHeaderTimeout = 200 * time.Millisecond
		src := &fakeSource{ep: endpointFor(upstream.URL, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: realKey}
		failure := newGateway(t, src, limits, nil).Do(context.Background(), request("/v1/messages", `{}`), &recSink{})
		require.NotNil(t, failure)
		assert.Equal(t, aigateway.ReasonUpstreamTimeout, failure.Reason)
	})

	t.Run("idle", func(t *testing.T) {
		t.Parallel()
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "data: 1\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		}))
		defer upstream.Close()
		limits := testLimits()
		limits.IdleTimeout = 300 * time.Millisecond
		src := &fakeSource{ep: endpointFor(upstream.URL, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: realKey}
		sink := &recSink{}
		start := time.Now()
		failure := newGateway(t, src, limits, nil).Do(context.Background(), request("/v1/messages", `{}`), sink)
		require.NotNil(t, failure)
		assert.Equal(t, aigateway.ReasonUpstreamTimeout, failure.Reason)
		assert.Equal(t, "data: 1\n\n", sink.body())
		assert.Less(t, time.Since(start), 3*time.Second)
	})

	t.Run("total", func(t *testing.T) {
		t.Parallel()
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for range 50 {
				_, _ = io.WriteString(w, "data: tick\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
					return
				case <-time.After(50 * time.Millisecond):
				}
			}
		}))
		defer upstream.Close()
		limits := testLimits()
		limits.TotalTimeout = 400 * time.Millisecond
		src := &fakeSource{ep: endpointFor(upstream.URL, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: realKey}
		failure := newGateway(t, src, limits, nil).Do(context.Background(), request("/v1/messages", `{}`), &recSink{})
		require.NotNil(t, failure)
		assert.Equal(t, aigateway.ReasonUpstreamTimeout, failure.Reason)
	})

	// A client that stops in the middle of its body holds a stream slot and its
	// buffer. The idle timeout and the total timeout also bound the read of the body.
	slowBody := func(t *testing.T, limits aigateway.Limits, feed func(*io.PipeWriter)) {
		t.Helper()
		var called atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { called.Add(1) }))
		defer upstream.Close()
		reader, writer := io.Pipe()
		defer func() { _ = writer.Close() }()
		go feed(writer)
		req := request("/v1/messages", `{}`)
		req.ContentLength = -1
		req.Body = reader
		g := newGateway(t, &fakeSource{ep: endpointFor(upstream.URL, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: realKey}, limits, nil)
		start := time.Now()
		failure := g.Do(context.Background(), req, &recSink{})
		require.NotNil(t, failure)
		assert.Equal(t, aigateway.ReasonUpstreamTimeout, failure.Reason)
		assert.Less(t, time.Since(start), 3*time.Second)
		assert.Zero(t, called.Load(), "no call goes to the upstream")
		assert.Zero(t, g.OpenStreams(), "the stream slot is free")
	}

	t.Run("request body idle", func(t *testing.T) {
		t.Parallel()
		limits := testLimits()
		limits.IdleTimeout = 300 * time.Millisecond
		slowBody(t, limits, func(w *io.PipeWriter) { _, _ = w.Write([]byte(`{"a":`)) })
	})

	t.Run("request body total", func(t *testing.T) {
		t.Parallel()
		limits := testLimits()
		limits.TotalTimeout = 400 * time.Millisecond
		slowBody(t, limits, func(w *io.PipeWriter) {
			for range 50 {
				if _, err := w.Write([]byte(" ")); err != nil {
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
		})
	})
}

func TestDo_UpstreamErrorHidesTheAddress(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	src := &fakeSource{ep: endpointFor("http://"+addr, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: realKey}
	failure := newGateway(t, src, testLimits(), nil).Do(context.Background(), request("/v1/messages", `{}`), &recSink{})
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonUpstreamError, failure.Reason)
	assert.NotContains(t, failure.Error(), "127.0.0.1")
	assert.NotContains(t, failure.Error(), "dial")
}

func TestDo_CancelEndsTheUpstreamCall(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	upstreamDone := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(upstreamDone)
		_, _ = io.WriteString(w, "data: 1\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer upstream.Close()
	src := &fakeSource{ep: endpointFor(upstream.URL, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: realKey}
	g := newGateway(t, src, testLimits(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *aigateway.Failure, 1)
	go func() { done <- g.Do(ctx, request("/v1/messages", `{}`), &recSink{}) }()
	<-started
	cancel()
	failure := <-done
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonCanceled, failure.Reason)
	select {
	case <-upstreamDone:
	case <-time.After(3 * time.Second):
		t.Fatal("the call to the provider kept running after the cancel")
	}
	assert.Zero(t, g.OpenStreams())
}

func TestDo_SlowClientSlowsTheUpstream(t *testing.T) {
	t.Parallel()
	// The sink blocks: the gateway must stop reading the upstream, not buffer it.
	var written atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chunk := bytes.Repeat([]byte("y"), 64*1024)
		for {
			n, err := w.Write(chunk)
			written.Add(int64(n))
			if err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	limits := testLimits()
	limits.MaxResponseBytes = 1 << 30
	limits.IdleTimeout = 500 * time.Millisecond
	src := &fakeSource{ep: endpointFor(upstream.URL, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: realKey}
	g := newGateway(t, src, limits, nil)

	block := make(chan struct{})
	sink := &blockingSink{block: block}
	failure := g.Do(context.Background(), request("/v1/messages", `{}`), sink)
	require.NotNil(t, failure)
	// The first chunk blocks until the idle timeout. The gateway reads no more.
	assert.Equal(t, aigateway.ReasonUpstreamTimeout, failure.Reason)
	assert.Less(t, written.Load(), int64(64<<20), "the upstream is not read without bound")
}

type blockingSink struct{ block chan struct{} }

func (s *blockingSink) Head(context.Context, int, http.Header) error { return nil }
func (s *blockingSink) Write(ctx context.Context, _ []byte) error {
	select {
	case <-s.block:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestDo_StreamLimits(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "x")
		w.(http.Flusher).Flush()
		select {
		case <-hold:
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	limits := testLimits()
	limits.MaxStreamsPerRun = 1
	limits.MaxStreams = 2
	src := &fakeSource{ep: endpointFor(upstream.URL, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: realKey}
	g := newGateway(t, src, limits, nil)

	opened := make(chan struct{}, 4)
	hold1 := &recSink{onChnk: func(int) { opened <- struct{}{} }}
	done := make(chan *aigateway.Failure, 3)
	run := func(attempt string, sink aigateway.ResponseSink) {
		req := request("/v1/messages", `{}`)
		req.AttemptKey = attempt
		done <- g.Do(context.Background(), req, sink)
	}
	go run("key-a", hold1)
	<-opened

	second := &recSink{}
	req := request("/v1/messages", `{}`)
	req.AttemptKey = "key-a"
	failure := g.Do(context.Background(), req, second)
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonTooManyStreams, failure.Reason, "one run has one stream")

	go run("key-b", &recSink{onChnk: func(int) { opened <- struct{}{} }})
	<-opened
	req.AttemptKey = "key-c"
	failure = g.Do(context.Background(), req, &recSink{})
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonTooManyStreams, failure.Reason, "the coordinator has two streams")

	close(hold)
	assert.Nil(t, <-done)
	assert.Nil(t, <-done)
	assert.Zero(t, g.OpenStreams())
}

func TestCheck(t *testing.T) {
	t.Parallel()
	ep := endpointFor("https://api.anthropic.com", aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey)
	ep.Preset = "anthropic"
	subject := aigateway.Subject{DAGName: "fix-bug"}

	g := newGateway(t, &fakeSource{ep: ep}, testLimits(), nil)
	result, failure := g.Check(context.Background(), subject, aigateway.EndpointTarget("cle1"), "claude")
	require.Nil(t, failure)
	assert.Equal(t, aiendpoint.ProtocolAnthropic, result.Protocol)
	assert.Equal(t, "anthropic", result.ChatType)

	_, failure = g.Check(context.Background(), subject, aigateway.EndpointTarget("other"), "claude")
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonEndpointUnknown, failure.Reason)

	openai := ep
	openai.Protocol, openai.Preset = aiendpoint.ProtocolOpenAI, "openai"
	_, failure = newGateway(t, &fakeSource{ep: openai}, testLimits(), nil).Check(context.Background(), subject, aigateway.EndpointTarget("cle1"), "claude")
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonEndpointProtocolMismatch, failure.Reason)

	nokey := ep
	nokey.SecretID = ""
	_, failure = newGateway(t, &fakeSource{ep: nokey}, testLimits(), nil).Check(context.Background(), subject, aigateway.EndpointTarget("cle1"), "claude")
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonEndpointKeyMissing, failure.Reason)

	_, failure = newGateway(t, &fakeSource{ep: ep, resolve: &aiendpoint.Error{Reason: aiendpoint.ReasonForbidden, Name: "cle1", Detail: "no"}}, testLimits(), nil).
		Check(context.Background(), subject, aigateway.EndpointTarget("cle1"), "claude")
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonEndpointForbidden, failure.Reason)
}

// A rotated key applies to the next request, and a deleted endpoint fails it.
func TestDo_ResolvesAgainForEachRequest(t *testing.T) {
	t.Parallel()
	var lastKey atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		lastKey.Store(r.Header.Get("X-Api-Key"))
	}))
	defer upstream.Close()
	src := &fakeSource{ep: endpointFor(upstream.URL, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: "key-one"}
	g := newGateway(t, src, testLimits(), nil)
	require.Nil(t, g.Do(context.Background(), request("/v1/messages", `{}`), &recSink{}))
	assert.Equal(t, "key-one", lastKey.Load())
	src.setKey("key-two")
	require.Nil(t, g.Do(context.Background(), request("/v1/messages", `{}`), &recSink{}))
	assert.Equal(t, "key-two", lastKey.Load())
	src.mu.Lock()
	src.resolve = &aiendpoint.Error{Reason: aiendpoint.ReasonUnknown, Name: "cle1"}
	src.mu.Unlock()
	failure := g.Do(context.Background(), request("/v1/messages", `{}`), &recSink{})
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonEndpointUnknown, failure.Reason)
}

// The error of a failed call never holds the key, also when the provider echoes it.
func TestFailureNeverHoldsTheKey(t *testing.T) {
	t.Parallel()
	err := errors.New("boom")
	f := aigateway.FailureOf(err)
	assert.Equal(t, aigateway.ReasonRelayUnavailable, f.Reason)
	_, body := aigateway.ErrorBody(aiendpoint.ProtocolAnthropic, aigateway.Fail(aigateway.ReasonEndpointForbidden, "no"))
	assert.Contains(t, string(body), "endpoint_forbidden")
	_, body = aigateway.ErrorBody(aiendpoint.ProtocolOpenAI, aigateway.Fail(aigateway.ReasonEndpointForbidden, "no"))
	assert.Contains(t, string(body), `"code":"endpoint_forbidden"`)
}
