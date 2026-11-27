// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package a2a

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// invocation builds the context a step execution sees.
func invocation(attempt, stepName, repeat string) context.Context {
	ctx := runtime.WithDAGContext(context.Background(), runtime.Context{AttemptID: attempt})
	env := runtime.NewEnv(ctx, ir.Step{Name: stepName})
	if repeat != "" {
		env = env.WithEnvVars(runenv.EnvKeyDAGRunStepRepeatIndex, repeat)
	}
	return runtime.WithEnv(ctx, env)
}

func TestDefaultID(t *testing.T) {
	t.Parallel()
	base := defaultMessageID(invocation("att-1", "delegate", "0"), "hi")

	// given the same inputs, as in a retry_policy retry
	assert.Equal(t, base, defaultMessageID(invocation("att-1", "delegate", "0"), "hi"))
	assert.Equal(t, base, defaultMessageID(invocation("att-1", "delegate", ""), "hi"), "no repeat policy means index 0")
	_, err := uuid.Parse(base)
	require.NoError(t, err)

	// when any one input differs
	for name, other := range map[string]string{
		"attempt": defaultMessageID(invocation("att-2", "delegate", "0"), "hi"),
		"step":    defaultMessageID(invocation("att-1", "other", "0"), "hi"),
		"repeat":  defaultMessageID(invocation("att-1", "delegate", "1"), "hi"),
		"text":    defaultMessageID(invocation("att-1", "delegate", "0"), "hi!"),
	} {
		// then the ID changes
		assert.NotEqual(t, base, other, name)
	}
}

// Iterations of a foreach body share attempt, step, and text.
func TestDefaultIDForeach(t *testing.T) {
	t.Parallel()
	item := func(v string) context.Context {
		ctx := invocation("att-1", "delegate", "")
		env := runtime.GetEnv(ctx)
		env.ForeachItem = v
		return runtime.WithEnv(ctx, env)
	}
	assert.NotEqual(t, defaultMessageID(item("a"), "hi"), defaultMessageID(item("b"), "hi"))
	assert.Equal(t, defaultMessageID(item("a"), "hi"), defaultMessageID(item("a"), "hi"))
}

// Pins the namespace and encoding: changing either changes the IDs of
// retries across an upgrade.
func TestDefaultIDGolden(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "19daf8ff-837c-5ac8-a36d-23622ab96666", defaultMessageID(invocation("att-1", "delegate", "0"), "hi"))
}

// Moving a character across a field boundary must not collide.
func TestDefaultIDUnambiguous(t *testing.T) {
	t.Parallel()
	a := defaultMessageID(invocation("ab", "c", "0"), "t")
	b := defaultMessageID(invocation("a", "bc", "0"), "t")
	c := defaultMessageID(invocation("a", "b", "0"), "c0t")
	assert.NotEqual(t, a, b)
	assert.NotEqual(t, b, c)
	assert.NotEqual(t, a, c)
}

func TestSentIDs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.send = func(obj) any { return obj{"task": taskJSON("task-1", "WORKING", nil)} }
	ctx := invocation("att-1", "delegate", "0")

	out, err := run(ctx, t, f, opSend, sendWith(nil))
	require.NoError(t, err)
	assert.Equal(t, defaultMessageID(ctx, "Diagnose it"), decodeOut(t, out)["sent_message_id"])
	assert.Equal(t, defaultMessageID(ctx, "Diagnose it"), f.lastSent()["message"].(obj)["messageId"])

	// an explicit value is sent as provided, even with surrounding spaces
	out, err = run(ctx, t, f, opSend, sendWith(obj{"message_id": " run-1-delegate "}))
	require.NoError(t, err)
	assert.Equal(t, " run-1-delegate ", decodeOut(t, out)["sent_message_id"])
	assert.Equal(t, " run-1-delegate ", f.lastSent()["message"].(obj)["messageId"])
}

// The first response is lost after the agent accepted the message; the
// retry resends the same ID and a deduplicating agent runs it once.
func TestLostResponseRetry(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var mu sync.Mutex
	started := map[string]int{}
	f.send = func(params obj) any {
		id := params["message"].(obj)["messageId"].(string)
		mu.Lock()
		defer mu.Unlock()
		if _, ok := started[id]; !ok {
			started[id] = len(started) + 1
		}
		return obj{"task": taskJSON("task-"+id, "WORKING", nil)}
	}
	dropped := false
	f.rpc = func(w http.ResponseWriter, _ *http.Request, method string, params obj) bool {
		if method != "SendMessage" || dropped {
			return false
		}
		dropped = true
		f.dispatch(httpDiscard{}, nil, method, params)
		panic(http.ErrAbortHandler)
	}
	ctx := invocation("att-1", "delegate", "0")

	_, err := run(ctx, t, f, opSend, sendWith(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Outcome unknown")
	first := f.lastSent()["message"].(obj)["messageId"]

	out, err := run(ctx, t, f, opSend, sendWith(nil))
	require.NoError(t, err)

	got := decodeOut(t, out)
	assert.Equal(t, first, got["sent_message_id"])
	assert.Equal(t, int32(2), f.sends.Load())
	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, started, 1, "one logical remote invocation")
	assert.Equal(t, "task-"+first.(string), got["task_id"])
}

type httpDiscard struct{}

func (httpDiscard) Header() http.Header         { return http.Header{} }
func (httpDiscard) Write(b []byte) (int, error) { return len(b), nil }
func (httpDiscard) WriteHeader(int)             {}
