// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package untrusted

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An untrusted step inside another untrusted step cannot get isolation: the
// parent step holds the restricted user.
func TestNestedUntrustedStep(t *testing.T) {
	t.Parallel()

	ctx := WithStep(context.Background(), &Step{name: "code"})
	_, err := Begin(ctx, Request{StepName: "verify", Config: config.UntrustedConfig{Enabled: true}})
	assert.EqualError(t, err, `untrusted step "verify" cannot run inside the untrusted step "code"`)

	step, err := Begin(ctx, Request{StepName: "verify", Config: config.UntrustedConfig{IsolationOff: true}, Log: io.Discard})
	assert.NoError(t, err)
	assert.False(t, step.Isolated())
}

// Without a restricted user, an untrusted step fails before it starts, unless
// the configuration turns the isolation off (Spec 103, "Without isolation").
func TestBeginWithoutRestrictedUser(t *testing.T) {
	t.Parallel()

	_, err := Begin(context.Background(), Request{StepName: "verify", Log: io.Discard})
	require.ErrorIs(t, err, ErrIsolationRequired)
	for _, part := range []string{`untrusted step "verify"`, reasonIsolationRequired, "untrusted.uid", "untrusted.gid", "untrusted.isolation"} {
		assert.Contains(t, err.Error(), part)
	}

	var log strings.Builder
	step, err := Begin(context.Background(), Request{StepName: "verify", Config: config.UntrustedConfig{IsolationOff: true}, Log: &log})
	require.NoError(t, err)
	assert.False(t, step.Isolated())
	assert.Equal(t, IsolationOffLine+"\n", log.String())
}

// This version of Dagu cannot run a step as the restricted user, so a
// restricted user in the configuration fails each untrusted step.
func TestBeginWithRestrictedUserFails(t *testing.T) {
	t.Parallel()

	step, err := Begin(context.Background(), Request{StepName: "verify", Config: config.UntrustedConfig{Enabled: true}, Log: io.Discard})
	assert.Nil(t, step)
	assert.ErrorContains(t, err, `untrusted step "verify"`)
}

// filterEnv drops the variables of Dagu, but DAGU_OUTPUT_FILE, and never
// returns nil.
func TestFilterEnv(t *testing.T) {
	t.Parallel()

	got := filterEnv([]string{"PATH=/bin", "DAGU_HOME=/root/.dagu", "dagu_lower=x", "DAGU_OUTPUT_FILE=/out", "DAG_RUN_ID=1", "DAGUX=kept"})
	assert.Equal(t, []string{"PATH=/bin", "DAGU_OUTPUT_FILE=/out", "DAG_RUN_ID=1", "DAGUX=kept"}, got)
	assert.NotNil(t, filterEnv(nil))
}
