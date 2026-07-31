// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"net/http"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
)

func TestGetResourceHistory_NoResourceService(t *testing.T) {
	t.Parallel()

	// SetupServer passes nil for resourceService, testing the error path
	server := test.SetupServer(t)

	resp := server.Client().Get("/api/v1/services/resources/history").
		ExpectStatus(http.StatusInternalServerError).
		Send(t)

	var errResp api.Error
	resp.Unmarshal(t, &errResp)

	assert.Equal(t, api.ErrorCodeInternalError, errResp.Code)
	assert.Contains(t, errResp.Message, "Resource service not available")
}
