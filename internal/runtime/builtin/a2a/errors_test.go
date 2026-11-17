// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package a2a

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func errBody(t *testing.T, out string) obj {
	t.Helper()
	return decodeOut(t, out)["error"].(obj)
}

func TestCardRejected(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		card obj
		want string
	}{
		"v03 only": {
			card: obj{"name": "x", "protocolVersion": "0.3.0", "url": "https://x.example/rpc", "preferredTransport": "JSONRPC"},
			want: "0.3",
		},
		"no jsonrpc": {
			card: obj{"supportedInterfaces": []obj{{"url": "https://x.example/g", "protocolBinding": "GRPC", "protocolVersion": "1.0"}}},
			want: "no JSONRPC interface",
		},
		"wrong version": {
			card: obj{"supportedInterfaces": []obj{{"url": "https://x.example/r", "protocolBinding": "JSONRPC", "protocolVersion": "0.3"}}},
			want: "unsupported protocol versions",
		},
		"empty": {card: obj{"supportedInterfaces": []obj{}}, want: "no JSONRPC interface"},
		"http rpc": {
			card: obj{"supportedInterfaces": []obj{{"url": "http://x.example/r", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}}},
			want: "https URL",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.card = func(string) any { return tc.card }

			out, err := run(context.Background(), t, f, opSend, sendWith(nil))

			require.Error(t, err)
			body := errBody(t, out)
			assert.Equal(t, kindDiscovery, body["kind"])
			assert.Contains(t, body["message"], tc.want)
			assert.Zero(t, f.sends.Load())
		})
	}
}

func TestCardMalformedAndMissing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.card = func(string) any { return "not an object" }
	out, err := run(context.Background(), t, f, opSend, sendWith(nil))
	require.Error(t, err)
	assert.Equal(t, kindDiscovery, errBody(t, out)["kind"])

	out, err = run(context.Background(), t, newFixture(t), opSend, sendWith(obj{"agent_url": f.srv.URL + "/nope"}))
	require.Error(t, err)
	assert.Equal(t, kindDiscovery, errBody(t, out)["kind"])
	assert.Contains(t, errBody(t, out)["message"], "404")
}

func TestAuthRejected(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		f := newFixture(t)
		f.needAuth = false
		f.rpc = func(w http.ResponseWriter, _ *http.Request, _ string, _ obj) bool {
			w.WriteHeader(status)
			return true
		}

		out, err := run(context.Background(), t, f, opSend, sendWith(nil))

		require.Error(t, err)
		body := errBody(t, out)
		assert.Equal(t, kindAuth, body["kind"])
		assert.NotContains(t, body["message"], "Outcome unknown", "a rejection is definitive")
	}

	f := newFixture(t)
	out, err := run(context.Background(), t, f, opSend, obj{"message": "x", "headers": obj{"Authorization": "Bearer wrong"}})
	require.Error(t, err)
	assert.Equal(t, kindAuth, errBody(t, out)["kind"])
}

// Redirects are refused and credentials never reach the redirect target.
func TestRedirectRefused(t *testing.T) {
	t.Parallel()
	other := newFixture(t)
	other.needAuth = false

	for _, onCard := range []bool{true, false} {
		f := newFixture(t)
		target := other.srv.URL + cardBase + cardPath
		if onCard {
			f.rpc = nil
			f.card = nil
		}
		f.mu.Lock()
		f.rpc = func(w http.ResponseWriter, r *http.Request, _ string, _ obj) bool {
			http.Redirect(w, r, other.srv.URL+rpcPath, http.StatusTemporaryRedirect)
			return true
		}
		f.mu.Unlock()
		agent := f.url()
		if onCard {
			f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target, http.StatusFound)
			})
		}

		out, err := run(context.Background(), t, f, opSend, sendWith(obj{"agent_url": agent}))

		require.Error(t, err)
		assert.Contains(t, errBody(t, out)["message"], "redirects are not followed")
		assert.Equal(t, kindProtocol, errBody(t, out)["kind"], "a redirect is a protocol error, also on the card")
	}
	assert.Zero(t, other.sends.Load())
	other.mu.Lock()
	defer other.mu.Unlock()
	assert.Empty(t, other.headers, "redirect target must not be contacted")
}

// A card that advertises another origin never receives credentials; without
// credentials the advertised endpoint is used.
func TestCrossOriginCard(t *testing.T) {
	t.Parallel()
	rpc := newFixture(t)
	rpc.needAuth = false
	rpc.send = func(obj) any { return obj{"task": taskJSON("t", "WORKING", nil)} }
	f := newFixture(t)
	f.card = func(string) any { return v1Card(rpc.srv.URL + rpcPath) }

	out, err := run(context.Background(), t, f, opSend, sendWith(nil))

	require.Error(t, err)
	assert.Equal(t, kindDiscovery, errBody(t, out)["kind"])
	assert.Contains(t, errBody(t, out)["message"], "another origin")
	rpc.mu.Lock()
	assert.Empty(t, rpc.headers)
	rpc.mu.Unlock()

	f.needAuth = false
	_, err = run(context.Background(), t, f, opSend, obj{"message": "x"})
	require.NoError(t, err)
	assert.Equal(t, int32(1), rpc.sends.Load())
}

// A response that never arrives is a failed step after exactly one
// submission, and says the outcome is unknown.
func TestDroppedResponse(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.rpc = func(w http.ResponseWriter, _ *http.Request, method string, params obj) bool {
		f.sends.Add(1)
		f.mu.Lock()
		f.sentParams = append(f.sentParams, params)
		f.mu.Unlock()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return true
	}

	out, err := run(context.Background(), t, f, opSend, sendWith(nil))

	require.Error(t, err)
	body := errBody(t, out)
	assert.Equal(t, kindTransport, body["kind"])
	assert.Contains(t, body["message"], "Outcome unknown")
	assert.Contains(t, body["message"], "duplicate remote work")
	assert.NotEmpty(t, body["message_id"])
	assert.Equal(t, int32(1), f.sends.Load())
}

func TestCancelDuringDiscovery(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	hit := make(chan struct{})
	f.srv.Config.Handler = http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(hit)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-hit
		cancel()
	}()

	out, err := run(ctx, t, f, opSend, sendWith(nil))

	require.Error(t, err)
	assert.Equal(t, kindCanceled, errBody(t, out)["kind"])
}

// A body that stalls after the headers is bounded by the step deadline.
func TestDeadlineDuringStalledBody(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.rpc = func(w http.ResponseWriter, r *http.Request, _ string, _ obj) bool {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{"task":`))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	out, err := run(ctx, t, f, opSend, sendWith(nil))

	require.Error(t, err)
	body := errBody(t, out)
	assert.Equal(t, kindTimeout, body["kind"])
	assert.Contains(t, body["message"], "Outcome unknown")
}

// Credentials echoed by the agent are masked in results and errors, short
// and overlapping values and JSON-escaped forms included, while types, keys
// and other headers stay intact.
func TestCredentialMasking(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.needAuth = false
	f.task = func(id string) (obj, bool) {
		return taskJSON(id, "COMPLETED", obj{
			"metadata":  obj{"count": 42, "auth": "Bearer " + token, "short": "xy", "q": `say "pw"d`, "region": "us", "custom": "cust-v4lue"},
			"artifacts": []obj{{"artifactId": "a", "parts": []obj{textPart("key=" + token + " and xy and tok")}}},
		}), true
	}
	headers := obj{"Authorization": "Bearer " + token, "X-Api-Key": "xy", "X-Secret": `"pw"d`, "X-Region": "us", "X-Custom": "cust-v4lue"}

	out, err := run(context.Background(), t, f, opGetTask, obj{"task_id": "t", "headers": headers})

	require.NoError(t, err)
	for _, secret := range []string{token, "xy", `"pw"d`, "cust-v4lue"} {
		assert.NotContains(t, out, secret)
	}
	got := decodeOut(t, out)
	md := got["metadata"].(obj)
	assert.Equal(t, float64(42), md["count"])
	assert.Equal(t, "us", md["region"], "values shorter than the minimum of a plain header are not masked")
	assert.Equal(t, "***", md["custom"], "any header value is masked, whatever its name")
	assert.Equal(t, "*** and *** and tok", got["artifacts"].([]any)[0].(obj)["parts"].([]any)[0].(obj)["text"].(string)[4:])
}

func TestCredentialMaskingInErrors(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.needAuth = false
	f.task = nil
	f.rpc = func(w http.ResponseWriter, _ *http.Request, _ string, _ obj) bool {
		replyErr(w, "1", -32000, `bad header Bearer `+token+` and "pw"d`)
		return true
	}
	headers := obj{"Authorization": "Bearer " + token, "X-Secret": `"pw"d`}

	out, err := run(context.Background(), t, f, opGetTask, obj{"task_id": "t", "headers": headers})

	require.Error(t, err)
	assert.NotContains(t, out, token)
	assert.NotContains(t, out, `pw`)
	assert.NotContains(t, err.Error(), token)
	assert.Equal(t, kindRemote, errBody(t, out)["kind"], out)
	assert.True(t, strings.Contains(out, "***"))
}

// A request without a deadline of its own is bounded by the default one.
func TestDefaultDeadline(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.rpc = func(_ http.ResponseWriter, r *http.Request, _ string, _ obj) bool {
		<-r.Context().Done()
		return true
	}

	out, err := runTweaked(context.Background(), t, f, opSend, sendWith(nil), func(c *caller) { c.deadline = 200 * time.Millisecond })

	require.Error(t, err)
	assert.Equal(t, kindTimeout, errBody(t, out)["kind"])
}

// An RPC failure is classified from the RPC response, not from the card
// request that came before it.
func TestObserversAreSeparate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.rpc = func(w http.ResponseWriter, _ *http.Request, _ string, _ obj) bool {
		w.WriteHeader(http.StatusBadGateway)
		return true
	}

	out, err := run(context.Background(), t, f, opGetTask, obj{"task_id": "t", "headers": authHeaders()})

	require.Error(t, err)
	body := errBody(t, out)
	assert.Equal(t, kindTransport, body["kind"])
	assert.Contains(t, body["message"], "HTTP 502")

	var card observer
	sess, err := connect(context.Background(), f.srv.Client().Transport, common{AgentURL: f.url(), Headers: map[string]string{"Authorization": "Bearer " + token}}, &card)
	require.NoError(t, err)
	before, _ := sess.obs.snapshot()
	assert.Zero(t, before, "card status must not leak into the RPC observer")
	_, _ = sess.transport.GetTask(context.Background(), sess.params(), &a2a.GetTaskRequest{ID: "t"})
	cardStatus, _ := card.snapshot()
	rpcStatus, _ := sess.obs.snapshot()
	assert.Equal(t, http.StatusOK, cardStatus)
	assert.Equal(t, http.StatusBadGateway, rpcStatus)
}

func TestOriginOf(t *testing.T) {
	t.Parallel()
	parse := func(s string) string {
		u, err := url.Parse(s)
		require.NoError(t, err)
		return originOf(u)
	}
	assert.Equal(t, parse("https://h"), parse("https://H:443/x"))
	assert.Equal(t, parse("https://h:8443/a"), parse("https://h:8443/b"))
	assert.NotEqual(t, parse("https://h"), parse("https://h:8443"))
	assert.NotEqual(t, parse("https://h"), parse("http://h"))
}

// Text from the card is masked in the error the step returns, not only on stdout.
func TestSecretInCardDiagnostic(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.card = func(string) any {
		return obj{"supportedInterfaces": []obj{{"url": "https://x.example/r", "protocolBinding": token, "protocolVersion": "1.0"}}}
	}

	out, err := run(context.Background(), t, f, opSend, sendWith(nil))

	require.Error(t, err)
	assert.NotContains(t, out, token)
	assert.NotContains(t, err.Error(), token)
	assert.Contains(t, err.Error(), "no JSONRPC interface")
}

// A token separated from its scheme by several spaces is masked when echoed alone.
func TestBearerWithExtraSpaces(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.needAuth = false
	f.task = func(id string) (obj, bool) {
		return taskJSON(id, "COMPLETED", obj{"metadata": obj{"echo": token}}), true
	}

	out, err := run(context.Background(), t, f, opGetTask, obj{"task_id": "t", "headers": obj{"Authorization": "Bearer   " + token}})

	require.NoError(t, err)
	assert.NotContains(t, out, token)
	assert.Equal(t, "***", decodeOut(t, out)["metadata"].(obj)["echo"])
}

// A result without a task id is a protocol error, for both actions.
func TestTaskWithoutIdentity(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.task = func(string) (obj, bool) { return nil, true }
	f.send = func(obj) any { return obj{"task": obj{}} }

	out, err := run(context.Background(), t, f, opGetTask, obj{"task_id": "t", "headers": authHeaders()})
	require.Error(t, err)
	assert.Equal(t, kindProtocol, errBody(t, out)["kind"])
	assert.Contains(t, errBody(t, out)["message"], "no id")

	out, err = run(context.Background(), t, f, opSend, sendWith(nil))
	require.Error(t, err)
	body := errBody(t, out)
	assert.Equal(t, kindProtocol, body["kind"])
	assert.Contains(t, body["message"], "no id")
	assert.NotEmpty(t, body["message_id"])
}
