// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"net/http"
	"testing"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/license"
	"github.com/dagucloud/dagu/internal/service/frontend"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
)

func TestNotificationChannels_RequireActiveLicense(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)
	server.Client().Get("/api/v1/notification-channels").
		ExpectStatus(http.StatusForbidden).Send(t)
}

func TestNotificationChannels_AcceptExistingLicenseWithoutFeatureClaim(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t,
		test.WithServerOptions(frontend.WithLicenseManager(license.NewTestManager())),
	)
	resp := server.Client().Get("/api/v1/notification-channels").
		ExpectStatus(http.StatusOK).Send(t)

	var result api.NotificationChannelListResponse
	resp.Unmarshal(t, &result)
	assert.Empty(t, result.Channels)
}
