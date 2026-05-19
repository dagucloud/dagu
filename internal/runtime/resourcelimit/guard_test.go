// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package resourcelimit

import (
	"context"
	"errors"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartWarnsAndContinuesWhenNativeEnforcementFails(t *testing.T) {
	limits, err := core.NewResourceLimits("1", "128Mi")
	require.NoError(t, err)

	oldStartNative := startNative
	t.Cleanup(func() { startNative = oldStartNative })
	startNative = func(context.Context, Options) (nativeGuard, error) {
		return nil, errors.New("not available")
	}

	guard := Start(context.Background(), Options{DAGName: "test", DAGRunID: "run", Limits: limits})

	result := guard.Result()
	assert.False(t, result.Enforced)
	assert.Equal(t, "none", result.Enforcer)
	assert.Contains(t, result.Warning, "not enforced")
	assert.Contains(t, result.Warning, "not available")
	require.NoError(t, guard.AssignProcess(123))
	require.NoError(t, guard.Close(context.Background()))
}

func TestStartReturnsEnforcedResultWhenNativeEnforcementSucceeds(t *testing.T) {
	limits, err := core.NewResourceLimits("500m", "64Mi")
	require.NoError(t, err)

	fake := &fakeNativeGuard{}
	oldStartNative := startNative
	t.Cleanup(func() { startNative = oldStartNative })
	startNative = func(context.Context, Options) (nativeGuard, error) {
		return fake, nil
	}

	guard := Start(context.Background(), Options{DAGName: "test", DAGRunID: "run", Limits: limits})

	result := guard.Result()
	assert.True(t, result.Enforced)
	assert.Equal(t, "fake", result.Enforcer)
	assert.Empty(t, result.Warning)
	require.NoError(t, guard.AssignProcess(456))
	assert.Equal(t, 456, fake.pid)
	require.NoError(t, guard.Close(context.Background()))
	assert.True(t, fake.closed)
}

type fakeNativeGuard struct {
	pid    int
	closed bool
}

func (f *fakeNativeGuard) AssignProcess(pid int) error {
	f.pid = pid
	return nil
}

func (f *fakeNativeGuard) Close(context.Context) error {
	f.closed = true
	return nil
}

func (*fakeNativeGuard) Enforcer() string {
	return "fake"
}
