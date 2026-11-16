// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package a2a

import (
	"context"
	"maps"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sendWith(extra obj) obj {
	w := obj{"message": "Diagnose it", "headers": authHeaders()}
	maps.Copy(w, extra)
	return w
}

// A task reply carries IDs and state, the request asks for nonblocking
// execution, and the advertised RPC path differs from the discovery URL.
func TestSendTask(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.send = func(obj) any { return obj{"task": taskJSON("task-1", "WORKING", nil)} }

	out, err := run(context.Background(), t, f, opSend, sendWith(obj{"context_id": "ctx-in"}))

	require.NoError(t, err)
	got := decodeOut(t, out)
	assert.Equal(t, "task", got["kind"])
	assert.Equal(t, "task-1", got["task_id"])
	assert.Equal(t, "ctx-1", got["context_id"])
	assert.Equal(t, "working", got["state"])
	assert.Equal(t, f.url(), got["agent_url"])
	assert.NotEmpty(t, got["sent_message_id"])

	sent := f.lastSent()
	assert.Equal(t, true, sent["configuration"].(obj)["returnImmediately"])
	msg := sent["message"].(obj)
	assert.Equal(t, "ctx-in", msg["contextId"])
	assert.Equal(t, "Diagnose it", msg["parts"].([]any)[0].(obj)["text"])
	assert.Equal(t, int32(1), f.sends.Load())
	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Equal(t, "1.0", f.headers[len(f.headers)-1].Get("A2A-Version"))
}

// A direct message reply exposes its parts and needs no task.
func TestSendMessage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.send = func(obj) any {
		return obj{"message": obj{"messageId": "m-1", "role": "ROLE_AGENT", "parts": []obj{textPart("it's \"done\"\nline2"), {"data": obj{"n": 3}}}}}
	}

	out, err := run(context.Background(), t, f, opSend, sendWith(nil))

	require.NoError(t, err)
	got := decodeOut(t, out)
	assert.Equal(t, "message", got["kind"])
	assert.Equal(t, "agent", got["role"])
	assert.Equal(t, "it's \"done\"\nline2", got["text"])
	parts := got["parts"].([]any)
	require.Len(t, parts, 2)
	assert.Equal(t, float64(3), parts[1].(obj)["data"].(obj)["n"])
	assert.Equal(t, "", got["task_id"])
}

// Every remote state is data: the call succeeds and reports the state, with
// multipart artifacts and history passed through.
func TestGetTaskStates(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	extra := obj{
		"artifacts": []obj{{"artifactId": "a1", "name": "report", "parts": []obj{textPart("part one"), {"data": obj{"k": []int{1, 2}}}}}},
		"history":   []obj{{"messageId": "h1", "role": "ROLE_USER", "parts": []obj{textPart("hi")}}},
	}
	f.task = func(id string) (obj, bool) {
		return taskJSON(id, strings.ToUpper(strings.ReplaceAll(id, "-", "_")), extra), true
	}

	for _, state := range []string{"submitted", "working", "completed", "failed", "canceled", "rejected", "input-required", "auth-required"} {
		out, err := run(context.Background(), t, f, opGetTask, obj{"task_id": state, "headers": authHeaders(), "history_length": "5"})

		require.NoError(t, err, state)
		got := decodeOut(t, out)
		assert.Equal(t, state, got["state"])
		assert.Equal(t, state, got["task_id"])
		arts := got["artifacts"].([]any)
		assert.Equal(t, "report", arts[0].(obj)["name"])
		assert.Len(t, arts[0].(obj)["parts"], 2)
		assert.Len(t, got["history"], 1)
	}
	assert.Equal(t, int32(8), f.gets.Load())
	assert.Zero(t, f.sends.Load())
}

func TestGetTaskNotFound(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.task = func(string) (obj, bool) { return nil, false }

	out, err := run(context.Background(), t, f, opGetTask, obj{"task_id": "gone", "headers": authHeaders()})

	require.Error(t, err)
	assert.Equal(t, kindNotFound, decodeOut(t, out)["error"].(obj)["kind"])
}

// The tenant of the selected interface and an explicit one are sent.
func TestTenantRouting(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.send = func(obj) any { return obj{"task": taskJSON("t", "WORKING", nil)} }

	out, err := run(context.Background(), t, f, opSend, sendWith(obj{"tenant": "acme"}))

	require.NoError(t, err)
	assert.Equal(t, "acme", f.lastSent()["tenant"])
	assert.Equal(t, "acme", decodeOut(t, out)["tenant"])
}

// A result over the capture limit fails with an actionable error instead of
// a truncated capture.
func TestOutputLimit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	big := strings.Repeat("x", 1<<20+10)
	f.task = func(id string) (obj, bool) {
		return taskJSON(id, "COMPLETED", obj{"artifacts": []obj{{"artifactId": "a", "parts": []obj{textPart(big)}}}}), true
	}
	with := obj{"task_id": "t", "headers": authHeaders()}

	out, err := run(context.Background(), t, f, opGetTask, with, func(s *ir.Step) { s.Output = "TASK" })

	require.Error(t, err)
	body := decodeOut(t, out)["error"].(obj)
	assert.Equal(t, kindOutputLimit, body["kind"])
	assert.Contains(t, body["message"], "max_output_size")

	out, err = run(context.Background(), t, f, opGetTask, with)
	require.NoError(t, err)
	assert.Contains(t, out, big)
}
