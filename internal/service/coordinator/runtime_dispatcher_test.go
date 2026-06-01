// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	coord "github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRuntimeDispatcherValidatesPeerConfig(t *testing.T) {
	t.Parallel()

	registry, err := coord.NewStaticRegistry([]string{"127.0.0.1:50055"})
	require.NoError(t, err)

	dispatcher, err := coord.NewRuntimeDispatcher(registry, config.Peer{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, coord.ErrMissingTLSConfig))
	assert.Nil(t, dispatcher)
}

func TestNewRuntimeDispatcherAllowsMissingRegistry(t *testing.T) {
	t.Parallel()

	dispatcher, err := coord.NewRuntimeDispatcher(nil, config.Peer{})
	require.NoError(t, err)
	assert.Nil(t, dispatcher)
}

func TestNewRuntimeDispatcherForwardsPeerRetryConfig(t *testing.T) {
	t.Parallel()

	registry, err := coord.NewStaticRegistry([]string{"127.0.0.1:50055"})
	require.NoError(t, err)

	dispatcher, err := coord.NewRuntimeDispatcher(registry, config.Peer{
		Insecure:      true,
		MaxRetries:    7,
		RetryInterval: 3 * time.Second,
	})
	require.NoError(t, err)
	require.NotNil(t, dispatcher)

	cfg, ok := coord.RuntimeDispatcherConfigForTest(dispatcher)
	require.True(t, ok)
	assert.Equal(t, 7, cfg.MaxRetries)
	assert.Equal(t, 3*time.Second, cfg.RetryInterval)
}
