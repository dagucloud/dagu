// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/service/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetResourceHistoryDuration(t *testing.T) {
	t.Parallel()

	a := &API{
		config: &config.Config{
			Monitoring: config.MonitoringConfig{Retention: 24 * time.Hour},
		},
		resourceService: resource.NewService(&config.Config{
			Monitoring: config.MonitoringConfig{Retention: 24 * time.Hour},
		}),
	}

	for _, tc := range []struct {
		name       string
		duration   *string
		wantStatus int
	}{
		{name: "Absent", duration: nil, wantStatus: http.StatusOK},
		{name: "Valid", duration: ptrOf("30m"), wantStatus: http.StatusOK},
		{name: "Invalid", duration: ptrOf("nonsense"), wantStatus: http.StatusBadRequest},
		{name: "Zero", duration: ptrOf("0"), wantStatus: http.StatusBadRequest},
		{name: "Negative", duration: ptrOf("-1m"), wantStatus: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := a.GetResourceHistory(context.Background(), api.GetResourceHistoryRequestObject{
				Params: api.GetResourceHistoryParams{Duration: tc.duration},
			})
			require.NoError(t, err)

			switch tc.wantStatus {
			case http.StatusOK:
				assert.IsType(t, api.GetResourceHistory200JSONResponse{}, resp)
			case http.StatusBadRequest:
				badReq, ok := resp.(api.GetResourceHistorydefaultJSONResponse)
				require.True(t, ok, "expected defaultJSONResponse, got %T", resp)
				assert.Equal(t, http.StatusBadRequest, badReq.StatusCode)
				assert.Equal(t, api.ErrorCodeBadRequest, badReq.Body.Code)
			}
		})
	}
}
