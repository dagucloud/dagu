// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package clickhouse

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

// request is a request received by the fake server.
type request struct {
	Method string
	Header http.Header
	Query  url.Values
	RawURL string
	Body   string
}

func (r request) isKill() bool { return strings.HasPrefix(r.Body, "KILL QUERY") }

// fakeServer is a ClickHouse HTTP endpoint that records its requests.
type fakeServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []request
}

// newFakeServer starts a server that calls handler for every request.
func newFakeServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, req request)) *fakeServer {
	t.Helper()
	fs := &fakeServer{}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req := request{
			Method: r.Method,
			Header: r.Header.Clone(),
			Query:  r.URL.Query(),
			RawURL: r.URL.String(),
			Body:   string(body),
		}
		fs.mu.Lock()
		fs.requests = append(fs.requests, req)
		fs.mu.Unlock()
		handler(w, r, req)
	}))
	t.Cleanup(fs.Close)
	return fs
}

// all returns the requests received so far.
func (fs *fakeServer) all() []request {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return append([]request(nil), fs.requests...)
}

// statements returns the requests that are not KILL QUERY.
func (fs *fakeServer) statements() []request {
	var out []request
	for _, r := range fs.all() {
		if !r.isKill() {
			out = append(out, r)
		}
	}
	return out
}

// kills returns the KILL QUERY requests.
func (fs *fakeServer) kills() []request {
	var out []request
	for _, r := range fs.all() {
		if r.isKill() {
			out = append(out, r)
		}
	}
	return out
}

// syncBuffer is a goroutine-safe buffer.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// testContext returns a context whose step working directory is dir.
func testContext(t *testing.T, dir string, step ir.Step) context.Context {
	t.Helper()
	dag := &ir.DAG{Name: "clickhouse-test", WorkingDir: dir, WorkingDirExplicit: true}
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "")
	return runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))
}

// newTestExecutor builds an executor whose step runs sql as its command.
func newTestExecutor(ctx context.Context, cfg map[string]any, sql string) (executor.Executor, error) {
	step := ir.Step{
		Name:           "clickhouse-step",
		Commands:       []ir.CommandEntry{{Command: strings.Fields(sql + " ")[0], CmdWithArgs: sql}},
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: cfg},
	}
	return newClickHouseExecutor(ctx, step)
}

// newTLSFakeServer starts an HTTPS server with a self-signed certificate that
// echoes statements.
func newTLSFakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write(append(body, '\n'))
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0) // the rejected handshake is expected
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}
