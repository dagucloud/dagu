// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagsettings_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/dagsettings"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTrimsAndValidatesProfile(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	settings, err := dagsettings.New(dagsettings.UpdateInput{
		DAGName:   " example ",
		Profile:   " prod ",
		UpdatedBy: "user-1",
	}, now)

	require.NoError(t, err)
	assert.Equal(t, "example", settings.DAGName)
	assert.Equal(t, "prod", settings.Profile)
	assert.Equal(t, "user-1", settings.CreatedBy)
	assert.Equal(t, now, settings.CreatedAt)
	assert.Equal(t, "user-1", settings.UpdatedBy)
	assert.Equal(t, now, settings.UpdatedAt)
}

func TestNewAllowsEmptyProfile(t *testing.T) {
	settings, err := dagsettings.New(dagsettings.UpdateInput{
		DAGName: "example",
		Profile: " ",
	}, time.Time{})

	require.NoError(t, err)
	assert.Empty(t, settings.Profile)
	assert.False(t, settings.CreatedAt.IsZero())
}

func TestNewRejectsInvalidNames(t *testing.T) {
	_, err := dagsettings.New(dagsettings.UpdateInput{
		DAGName: "bad/name",
		Profile: "prod",
	}, time.Time{})
	require.ErrorIs(t, err, dagsettings.ErrInvalidDAGName)

	_, err = dagsettings.New(dagsettings.UpdateInput{
		DAGName: "example",
		Profile: "Prod",
	}, time.Time{})
	require.Error(t, err)
}
