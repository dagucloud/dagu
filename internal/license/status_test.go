// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusFor(t *testing.T) {
	t.Parallel()

	t.Run("nil checker reports community status", func(t *testing.T) {
		t.Parallel()

		status := StatusFor(nil)

		assert.True(t, status.Community)
		assert.False(t, status.Valid)
		assert.Empty(t, status.Features)
	})

	t.Run("active license includes its public status", func(t *testing.T) {
		t.Parallel()

		manager := NewTestManager(FeatureAudit, FeatureRBAC)
		status := StatusFor(manager.Checker())

		assert.False(t, status.Community)
		assert.True(t, status.Valid)
		assert.Equal(t, "pro", status.Plan)
		assert.Equal(t, []string{FeatureAudit, FeatureRBAC}, status.Features)
		assert.WithinDuration(t, status.Expiry.Add(gracePeriod), status.GraceEndsAt, time.Second)
	})
}

func TestManagerStatusIncludesFailureAndSource(t *testing.T) {
	t.Parallel()

	manager := NewExpiredTestManager(FeatureAudit)
	manager.setSource(SourceFileJWT)

	status := manager.Status()

	require.False(t, status.Community)
	assert.False(t, status.Valid)
	assert.False(t, status.GracePeriod)
	assert.Equal(t, SourceFileJWT, status.Source)
	assert.Equal(t, licenseExpiredFailure, status.Failure)
}
