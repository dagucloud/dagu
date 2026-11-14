// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Without a configured time zone the server runs in the host's local zone,
// which has no name to show; each fire then shows its own offset, which
// stays right across half-hour zones and daylight saving changes.
func TestFireZoneLabel(t *testing.T) {
	t.Parallel()

	tokyo, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)
	fire := time.Date(2027, 1, 4, 9, 0, 0, 0, time.UTC)

	assert.Equal(t, "Asia/Tokyo", fireZoneLabel(fire.In(tokyo)))
	assert.Equal(t, "UTC", fireZoneLabel(fire))
	assert.Equal(t, "UTC+05:30", fireZoneLabel(fire.In(time.FixedZone("", 5*3600+30*60))))
	assert.Equal(t, "UTC-03:00", fireZoneLabel(fire.In(time.FixedZone("", -3*3600))))
}
