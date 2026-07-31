// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/service/frontend/metrics"
)

func (a *API) GetHealthStatus(_ context.Context, _ api.GetHealthStatusRequestObject) (api.GetHealthStatusResponseObject, error) {
	return &api.GetHealthStatus200JSONResponse{
		Status:    api.HealthResponseStatusHealthy,
		Version:   config.Version,
		Uptime:    int(metrics.GetUptime()),
		Timestamp: stringutil.FormatTime(time.Now()),
	}, nil
}
