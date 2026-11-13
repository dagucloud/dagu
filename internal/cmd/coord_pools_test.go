// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The start warns about coordinator.worker_wait_timeout 0 only for a coordinator
// with an agent pool.
func TestHasAgentPool(t *testing.T) {
	t.Parallel()
	peer := config.Peer{CertFile: "/tls/c.crt", KeyFile: "/tls/c.key", ClientCaFile: "/tls/ca.crt"}
	general, err := coordinator.NewWorkerPools([]config.WorkerPool{
		{Name: "general", Identities: []string{"g"}, Labels: map[string]string{"zone": "*"}},
	}, []string{"server"}, peer)
	require.NoError(t, err)
	assert.False(t, hasAgentPool(general))

	agent, err := coordinator.NewWorkerPools([]config.WorkerPool{
		{Name: "general", Identities: []string{"g"}, Labels: map[string]string{"zone": "*"}},
		{Name: "agent", Identities: []string{"a"}, Labels: map[string]string{"agent.provider.claude": "true"}},
	}, []string{"server"}, peer)
	require.NoError(t, err)
	assert.True(t, hasAgentPool(agent))
}
