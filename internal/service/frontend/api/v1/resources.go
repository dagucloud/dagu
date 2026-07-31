// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"math"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/service/resource"
)

func (a *API) GetResourceHistory(ctx context.Context, request api.GetResourceHistoryRequestObject) (api.GetResourceHistoryResponseObject, error) {
	if err := a.requireDeveloperOrAbove(ctx); err != nil {
		return nil, err
	}

	if a.resourceService == nil {
		return api.GetResourceHistorydefaultJSONResponse{
			Body: api.Error{
				Code:    api.ErrorCodeInternalError,
				Message: "Resource service not available",
			},
			StatusCode: 500,
		}, nil
	}

	// Default to 1 hour, capped at retention period
	maxDuration := a.config.Monitoring.Retention
	duration := time.Hour
	if request.Params.Duration != nil {
		if d, err := time.ParseDuration(*request.Params.Duration); err == nil && d > 0 {
			duration = min(d, maxDuration)
		} else if err != nil {
			logger.Warn(ctx, "Invalid duration parameter", tag.String("duration", *request.Params.Duration))
		}
	}

	history := a.resourceService.GetHistory(duration)

	cpu := convertMetrics(history.CPU)
	mem := convertMetrics(history.Memory)
	disk := convertMetrics(history.Disk)
	load := convertMetrics(history.Load)

	memoryTotalBytes := safeUint64ToInt64(history.MemoryTotalBytes)
	memoryUsedBytes := safeUint64ToInt64(history.MemoryUsedBytes)
	diskTotalBytes := safeUint64ToInt64(history.DiskTotalBytes)
	diskUsedBytes := safeUint64ToInt64(history.DiskUsedBytes)

	return api.GetResourceHistory200JSONResponse{
		Cpu:              &cpu,
		Memory:           &mem,
		Disk:             &disk,
		Load:             &load,
		MemoryTotalBytes: &memoryTotalBytes,
		MemoryUsedBytes:  &memoryUsedBytes,
		DiskTotalBytes:   &diskTotalBytes,
		DiskUsedBytes:    &diskUsedBytes,
	}, nil
}

func safeUint64ToInt64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

// convertMetrics converts a slice of resource.MetricPoint into a slice of api.MetricPoint by copying each point's Timestamp and Value.
// The resulting slice preserves the input order and has the same length as the input.
func convertMetrics(points []resource.MetricPoint) []api.MetricPoint {
	result := make([]api.MetricPoint, len(points))
	for i := range points {
		result[i] = api.MetricPoint{
			Timestamp: points[i].Timestamp,
			Value:     points[i].Value,
		}
	}
	return result
}
