// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

const (
	token    = "tok-s3cr3t"
	cardBase = "/agents/diag"
	rpcPath  = "/rpc/v1"
)

type obj = map[string]any

// fixture is a deterministic A2A 1.0 JSON-RPC agent over HTTPS. The card is
// served below cardBase and advertises an RPC path that differs from it.
type fixture struct {
	srv   *httptest.Server
	sends atomic.Int32
	gets  atomic.Int32

	mu         sync.Mutex
	card       func(url string) any
	rpc        func(w http.ResponseWriter, r *http.Request, method string, params obj) bool
	send       func(params obj) any
	task       func(id string) (obj, bool)
	sentParams []obj
	headers    []http.Header
	needAuth   bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{needAuth: true}
	f.card = func(url string) any { return v1Card(url + rpcPath) }
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func v1Card(rpc string) obj {
	return obj{
		"name": "diag", "version": "1",
		"supportedInterfaces": []obj{
			{"url": rpc + "/grpc", "protocolBinding": "GRPC", "protocolVersion": "1.0"},
			{"url": rpc, "protocolBinding": "JSONRPC", "protocolVersion": "1.0"},
			{"url": rpc + "/rest", "protocolBinding": "HTTP+JSON", "protocolVersion": "1.0"},
		},
	}
}

func (f *fixture) url() string { return f.srv.URL + cardBase }

func (f *fixture) lastSent() obj {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sentParams[len(f.sentParams)-1]
}

func (f *fixture) record(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.headers = append(f.headers, r.Header.Clone())
}

func (f *fixture) serve(w http.ResponseWriter, r *http.Request) {
	f.record(r)
	f.mu.Lock()
	card, rpc, needAuth := f.card, f.rpc, f.needAuth
	f.mu.Unlock()
	if needAuth && r.Header.Get("Authorization") != "Bearer "+token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case cardBase + cardPath:
		_ = json.NewEncoder(w).Encode(card("https://" + r.Host))
	case rpcPath:
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
			Params obj    `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if rpc != nil && rpc(w, r, req.Method, req.Params) {
			return
		}
		f.dispatch(w, req.ID, req.Method, req.Params)
	default:
		http.NotFound(w, r)
	}
}

func (f *fixture) dispatch(w http.ResponseWriter, id any, method string, params obj) {
	f.mu.Lock()
	send, task := f.send, f.task
	f.mu.Unlock()
	switch method {
	case "SendMessage":
		f.sends.Add(1)
		f.mu.Lock()
		f.sentParams = append(f.sentParams, params)
		f.mu.Unlock()
		reply(w, id, send(params))
	case "GetTask":
		f.gets.Add(1)
		t, ok := task(params["id"].(string))
		if !ok {
			replyErr(w, id, -32001, "task not found")
			return
		}
		reply(w, id, t)
	default:
		replyErr(w, id, -32601, "method not found")
	}
}

func reply(w http.ResponseWriter, id, result any) {
	_ = json.NewEncoder(w).Encode(obj{"jsonrpc": "2.0", "id": id, "result": result})
}

func replyErr(w http.ResponseWriter, id any, code int, msg string) {
	_ = json.NewEncoder(w).Encode(obj{"jsonrpc": "2.0", "id": id, "error": obj{"code": code, "message": msg}})
}

func taskJSON(id, state string, extra obj) obj {
	t := obj{"id": id, "contextId": "ctx-1", "status": obj{"state": "TASK_STATE_" + state}}
	maps.Copy(t, extra)
	return t
}

func textPart(s string) obj { return obj{"text": s} }

// run executes one step against f, returning stdout and the step error.
func run(ctx context.Context, t *testing.T, f *fixture, op string, with obj, mutate ...func(*ir.Step)) (string, error) {
	t.Helper()
	return runTweaked(ctx, t, f, op, with, nil, mutate...)
}

// runTweaked is run with a hook on the caller before it runs.
func runTweaked(ctx context.Context, t *testing.T, f *fixture, op string, with obj, tweak func(*caller), mutate ...func(*ir.Step)) (string, error) {
	t.Helper()
	if _, ok := with["agent_url"]; !ok {
		with["agent_url"] = f.url()
	}
	step := ir.Step{
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: with},
		Commands:       []ir.CommandEntry{{Command: op}},
	}
	for _, m := range mutate {
		m(&step)
	}
	exec, err := newCaller(ctx, step)
	require.NoError(t, err)
	c := exec.(*caller)
	c.base = f.srv.Client().Transport
	var out bytes.Buffer
	c.SetStdout(&out)
	if tweak != nil {
		tweak(c)
	}
	err = c.Run(ctx)
	return out.String(), err
}

func authHeaders() obj { return obj{"Authorization": "Bearer " + token} }

func decodeOut(t *testing.T, out string) obj {
	t.Helper()
	var m obj
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(out)), &m), out)
	return m
}
