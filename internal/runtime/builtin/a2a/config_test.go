// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package a2a

import (
	"context"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func step(op string, with obj) ir.Step {
	return ir.Step{
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: with},
		Commands:       []ir.CommandEntry{{Command: op}},
	}
}

// Load time accepts references and checks the rest; runtime checks the
// resolved values before anything is dialed.
func TestConfigForms(t *testing.T) {
	t.Parallel()
	const agent = "https://a.example/agent"
	cases := []struct {
		name    string
		op      string
		with    obj
		loadErr string
		runErr  string
	}{
		{name: "send", op: opSend, with: obj{"agent_url": agent, "message": "hi"}},
		{name: "send refs", op: opSend, with: obj{"agent_url": "${A2A_URL}", "message": "${params.PROMPT}", "headers": obj{"Authorization": "Bearer ${TOKEN}"}},
			runErr: "absolute https URL"},
		{name: "get refs", op: opGetTask, with: obj{"agent_url": "${A2A_URL}", "task_id": "${SEND.task_id}", "history_length": "${N}"},
			runErr: "integer"},
		{name: "get resolved", op: opGetTask, with: obj{"agent_url": agent, "task_id": "t1", "history_length": "5"}},
		{name: "unknown key", op: opSend, with: obj{"agent_url": agent, "message": "hi", "task_id": "x"}, loadErr: "invalid keys", runErr: "invalid keys"},
		{name: "send history ref", op: opSend, with: obj{"agent_url": agent, "message": "hi", "history_length": "${N}"}, loadErr: "invalid keys", runErr: "invalid keys"},
		{name: "unknown get key", op: opGetTask, with: obj{"agent_url": agent, "task_id": "x", "message": "hi"}, loadErr: "invalid keys", runErr: "invalid keys"},
		{name: "userinfo", op: opSend, with: obj{"agent_url": "https://u:p@a.example/", "message": "hi"}, loadErr: "credentials", runErr: "credentials"},
		{name: "query", op: opSend, with: obj{"agent_url": agent + "?api_key=abc", "message": "hi"}, loadErr: "query string", runErr: "query string"},
		{name: "fragment", op: opSend, with: obj{"agent_url": agent + "#x", "message": "hi"}, loadErr: "fragment", runErr: "fragment"},
		{name: "content type", op: opSend, with: obj{"agent_url": agent, "message": "m", "headers": obj{"Content-Type": "text/plain"}}, loadErr: "cannot be overridden", runErr: "cannot be overridden"},
		{name: "accept", op: opGetTask, with: obj{"agent_url": agent, "task_id": "t", "headers": obj{"accept": "x"}}, loadErr: "cannot be overridden", runErr: "cannot be overridden"},
		{name: "version", op: opSend, with: obj{"agent_url": agent, "message": "m", "headers": obj{"A2A-Version": "0.3"}}, loadErr: "cannot be overridden", runErr: "cannot be overridden"},
		{name: "host", op: opSend, with: obj{"agent_url": agent, "message": "m", "headers": obj{"Host": "evil"}}, loadErr: "cannot be overridden", runErr: "cannot be overridden"},
		{name: "length", op: opSend, with: obj{"agent_url": agent, "message": "m", "headers": obj{"Content-Length": "1"}}, loadErr: "cannot be overridden", runErr: "cannot be overridden"},
		{name: "idempotency", op: opSend, with: obj{"agent_url": agent, "message": "m", "headers": obj{"Idempotency-Key": "k"}}, loadErr: "cannot be overridden", runErr: "cannot be overridden"},
		{name: "x idempotency", op: opGetTask, with: obj{"agent_url": agent, "task_id": "t", "headers": obj{"x-idempotency-key": "k"}}, loadErr: "cannot be overridden", runErr: "cannot be overridden"},
		{name: "http", op: opSend, with: obj{"agent_url": "http://a.example/", "message": "hi"}, loadErr: "https", runErr: "https"},
		{name: "no message", op: opSend, with: obj{"agent_url": agent}, loadErr: "message is required", runErr: "message is required"},
		{name: "no task", op: opGetTask, with: obj{"agent_url": agent}, loadErr: "task_id is required", runErr: "task_id is required"},
		{name: "bad header name", op: opSend, with: obj{"agent_url": agent, "message": "m", "headers": obj{"Bad Name": "v"}}, loadErr: "header name", runErr: "header name"},
		{name: "header newline", op: opSend, with: obj{"agent_url": agent, "message": "m", "headers": obj{"X": "a\nb"}}, loadErr: "line breaks", runErr: "line breaks"},
		{name: "msg id ref", op: opSend, with: obj{"agent_url": agent, "message": "m", "message_id": "${DAG_RUN_ID}-delegate"}},
		{name: "msg id long", op: opSend, with: obj{"agent_url": agent, "message": "m", "message_id": strings.Repeat("a", 600)}, loadErr: "message_id must be at most", runErr: "message_id must be at most"},
		{name: "msg id control", op: opSend, with: obj{"agent_url": agent, "message": "m", "message_id": "a\x01b"}, loadErr: "message_id must be at most", runErr: "message_id must be at most"},
		{name: "msg id empty", op: opSend, with: obj{"agent_url": agent, "message": "m", "message_id": ""}, loadErr: "message_id must not be empty", runErr: "message_id must not be empty"},
		{name: "get msg id", op: opGetTask, with: obj{"agent_url": agent, "task_id": "t", "message_id": "x"}, loadErr: "invalid keys", runErr: "invalid keys"},
		{name: "task id control", op: opGetTask, with: obj{"agent_url": agent, "task_id": "a\x01b"}, loadErr: "control characters", runErr: "control characters"},
		{name: "task id long", op: opGetTask, with: obj{"agent_url": agent, "task_id": strings.Repeat("a", 600)}, loadErr: "at most", runErr: "at most"},
		{name: "negative history", op: opGetTask, with: obj{"agent_url": agent, "task_id": "t", "history_length": -1}, loadErr: "negative", runErr: "negative"},
		{name: "bad history", op: opGetTask, with: obj{"agent_url": agent, "task_id": "t", "history_length": "many"}, loadErr: "integer", runErr: "integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateStep(step(tc.op, tc.with))
			if tc.loadErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.loadErr)
			}
			_, err = newCaller(context.Background(), step(tc.op, tc.with))
			if tc.runErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.runErr)
			}
		})
	}
}

func TestOtherExecutorsIgnored(t *testing.T) {
	t.Parallel()
	s := step(opSend, obj{})
	s.ExecutorConfig.Type = "http"
	assert.NoError(t, validateStep(s))
}
