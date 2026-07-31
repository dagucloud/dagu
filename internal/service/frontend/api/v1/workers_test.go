// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"net/http"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

func TestGetWorkers(t *testing.T) {
	t.Run("NoCoordinatorConfigured", func(t *testing.T) {
		// Set up a server without coordinator configuration
		server := test.SetupServer(t)

		// Make the request
		resp := server.Client().Get("/api/v1/workers").ExpectStatus(http.StatusOK).Send(t)

		var workersResp api.WorkersListResponse
		resp.Unmarshal(t, &workersResp)

		// Should return empty workers list with an explanatory error when no coordinators are available
		require.Empty(t, workersResp.Workers)
		require.Equal(t, []string{"no coordinators available"}, workersResp.Errors)
	})

	// Additional integration tests would require a real coordinator running
	// For now, the unit test approach with mocks would be better
	// but that would require refactoring the test setup
}
