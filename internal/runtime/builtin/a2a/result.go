// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package a2a

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// Error kinds reported in the step output and error message.
const (
	kindDiscovery   = "discovery"
	kindAuth        = "auth"
	kindNotFound    = "not_found"
	kindRemote      = "remote"
	kindProtocol    = "protocol"
	kindTransport   = "transport"
	kindTimeout     = "timeout"
	kindCanceled    = "canceled"
	kindOutputLimit = "output_limit"
)

type errorOutput struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	// MessageID names the message a failed a2a.send submitted, for finding
	// the task on the remote agent after an unknown outcome.
	MessageID string `json:"message_id,omitempty"`
}

type callError struct {
	action    string
	kind      string
	message   string
	messageID string
}

func (e *callError) Error() string {
	return e.action + ": " + e.kind + " error: " + e.message
}

// normalizeState turns TASK_STATE_INPUT_REQUIRED into input-required.
func normalizeState(s a2a.TaskState) string {
	name := strings.TrimPrefix(s.String(), "TASK_STATE_")
	return strings.ToLower(strings.ReplaceAll(name, "_", "-"))
}

func normalizeRole(r a2a.MessageRole) string {
	return strings.ToLower(strings.TrimPrefix(string(r), "ROLE_"))
}

// generic converts an SDK value to plain JSON data, keeping every field the
// SDK knows, such as message and artifact parts.
func generic(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	return out, json.Unmarshal(raw, &out)
}

func field(v any, name string) any {
	m, _ := v.(map[string]any)
	return m[name]
}

type route struct {
	agentURL string
	tenant   string
}

func (r route) base(kind string) map[string]any {
	out := map[string]any{"kind": kind, "agent_url": r.agentURL}
	if r.tenant != "" {
		out["tenant"] = r.tenant
	}
	return out
}

func taskOutput(r route, t *a2a.Task) (map[string]any, error) {
	tree, err := generic(t)
	if err != nil {
		return nil, fmt.Errorf("encoding task: %w", err)
	}
	out := r.base("task")
	out["task_id"] = string(t.ID)
	out["context_id"] = t.ContextID
	out["state"] = normalizeState(t.Status.State)
	out["status_message"] = field(field(tree, "status"), "message")
	out["history"] = orEmpty(field(tree, "history"))
	out["artifacts"] = orEmpty(field(tree, "artifacts"))
	if md := field(tree, "metadata"); md != nil {
		out["metadata"] = md
	}
	return out, nil
}

func messageOutput(r route, m *a2a.Message) (map[string]any, error) {
	tree, err := generic(m)
	if err != nil {
		return nil, fmt.Errorf("encoding message: %w", err)
	}
	out := r.base("message")
	out["message_id"] = m.ID
	out["context_id"] = m.ContextID
	out["task_id"] = string(m.TaskID)
	out["role"] = normalizeRole(m.Role)
	out["parts"] = orEmpty(field(tree, "parts"))
	out["text"] = messageText(tree)
	return out, nil
}

func orEmpty(v any) any {
	if v == nil {
		return []any{}
	}
	return v
}

// messageText joins the text parts of a message.
func messageText(tree any) string {
	parts, _ := field(tree, "parts").([]any)
	var texts []string
	for _, p := range parts {
		if t, ok := field(p, "text").(string); ok {
			texts = append(texts, t)
		}
	}
	return strings.Join(texts, "\n")
}
