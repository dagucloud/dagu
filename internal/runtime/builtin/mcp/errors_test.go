// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolError(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	out, err := run(t.Context(), t, f.cfg("fail", nil))
	require.Error(t, err)
	assert.Equal(t, kindTool, errKind(t, err))
	assert.Equal(t, true, decode(t, out)["is_error"])
	assert.NotContains(t, err.Error(), token)
	assert.Contains(t, err.Error(), "***")
}

func TestProtocolError(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	out, err := run(t.Context(), t, f.cfg("no_such_tool", nil))
	require.Error(t, err)
	assert.Equal(t, kindProtocol, errKind(t, err))
	assert.Equal(t, kindProtocol, decode(t, out)["error"].(map[string]any)["kind"])
}

func TestTransportError(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	cfg := f.cfg("echo", nil)
	f.srv.Close()

	_, err := run(t.Context(), t, cfg)
	require.Error(t, err)
	assert.Equal(t, kindTransport, errKind(t, err))
	assert.NotContains(t, err.Error(), token)
}

func TestAuthFailure(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	cfg := f.cfg("echo", map[string]any{"headers": map[string]any{"Authorization": "Bearer wrong-token"}})

	out, err := run(t.Context(), t, cfg)
	require.Error(t, err)
	assert.Equal(t, kindAuth, errKind(t, err))
	assert.Contains(t, out, `"kind":"auth"`)
	assert.NotContains(t, err.Error()+out, "wrong-token")
}

func TestNoImplicitRetry(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	_, err := run(t.Context(), t, f.cfg("fail", nil))
	require.Error(t, err)
	assert.EqualValues(t, 1, f.calls.Load())
}

func TestTimeout(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := run(ctx, t, f.cfg("slow", nil))
	require.Error(t, err)
	assert.Equal(t, kindTimeout, errKind(t, err))
	assert.Less(t, time.Since(start), 8*time.Second)
}

func TestCancellation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(300*time.Millisecond, cancel)

	_, err := run(ctx, t, f.cfg("slow", nil))
	require.Error(t, err)
	assert.Equal(t, kindCanceled, errKind(t, err))
}

func TestRedactor(t *testing.T) {
	t.Parallel()
	r := newRedactor(map[string]string{"Authorization": "Bearer abc123", "X-Key": "k-999"}, "")
	got := r.apply("failed with Bearer abc123 / abc123 / k-999")
	assert.Equal(t, "failed with *** / *** / ***", got)
}
